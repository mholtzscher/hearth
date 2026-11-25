package agent //nolint:testpackage // Tests exercise package-private reply validation.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func starterReply(items ...StarterQuestion) string {
	type item struct {
		Title  string `json:"title"`
		Prompt string `json:"prompt"`
	}
	body := struct {
		StarterQuestions []item `json:"starter_questions"`
	}{}
	for _, question := range items {
		body.StarterQuestions = append(body.StarterQuestions, item(question))
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func question(title string) StarterQuestion {
	return StarterQuestion{Title: title, Prompt: title + " Please explain."}
}

func titles(questions []StarterQuestion) []string {
	out := make([]string, len(questions))
	for index, question := range questions {
		out[index] = question.Title
	}
	return out
}

func TestParseStarterQuestionsKeepsFirstFourValidInReplyOrder(t *testing.T) {
	t.Parallel()
	reply := starterReply(
		question("Which devices are offline?"),
		question("Show the temperatures"),
		question("Why did Porch lights fail?"),
		question("What happened overnight?"),
		question("Show humidity today"),
		question("Is the garage door open?"),
	)
	for name, content := range map[string]string{
		"bare":   reply,
		"fenced": "```json\n" + reply + "\n```",
		"prose":  "Here you go: " + reply + " Enjoy.",
	} {
		got, err := parseStarterQuestions(content)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := []string{
			"Which devices are offline?", "Show the temperatures",
			"Why did Porch lights fail?", "What happened overnight?",
		}
		if strings.Join(titles(got), "|") != strings.Join(want, "|") {
			t.Fatalf("%s: titles = %q, want %q", name, titles(got), want)
		}
		if got[0].Prompt != "Which devices are offline? Please explain." {
			t.Fatalf("%s: prompt = %q", name, got[0].Prompt)
		}
	}
}

func TestParseStarterQuestionsDropsInvalidItems(t *testing.T) {
	t.Parallel()
	valid := []StarterQuestion{
		question("Which devices are offline?"),
		question("Show the temperatures"),
		question("What happened overnight?"),
	}
	cases := map[string]StarterQuestion{
		"command title":             {Title: "Turn off the porch light", Prompt: "Can you help with the porch?"},
		"command prompt":            {Title: "Porch light", Prompt: "Unlock the front door."},
		"command title punctuation": {Title: `"Lock" the doors`, Prompt: "Are the doors locked?"},
		"command prompt case":       {Title: "Thermostat", Prompt: "SET the thermostat to 20."},
		"empty title":               {Title: "  ", Prompt: "What is the temperature?"},
		"empty prompt":              {Title: "Temperature", Prompt: ""},
		"long title":                {Title: strings.Repeat("a", maxStarterQuestionTitleRunes+1), Prompt: "Why?"},
		"long prompt":               {Title: "Why?", Prompt: strings.Repeat("é", maxStarterQuestionPromptRunes+1)},
		"duplicate title":           {Title: "  SHOW THE TEMPERATURES ", Prompt: "Show them again."},
	}
	for name, invalid := range cases {
		got, err := parseStarterQuestions(starterReply(append(append([]StarterQuestion{}, valid...), invalid)...))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != len(valid) {
			t.Fatalf("%s: kept %q, want only the three valid questions", name, titles(got))
		}
	}
	got, err := parseStarterQuestions(starterReply(StarterQuestion{
		Title: strings.Repeat("é", maxStarterQuestionTitleRunes), Prompt: "Is a full-length title accepted?",
	}, valid[0], valid[1]))
	if err != nil || len(got) != 3 {
		t.Fatalf("title at the rune limit: got %d questions, err %v", len(got), err)
	}
}

func TestParseStarterQuestionsRejectsUnusableReplies(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"no object":     "I cannot help with that.",
		"invalid json":  `{"starter_questions":[{"title":}]}`,
		"empty list":    `{"starter_questions":[]}`,
		"two survivors": starterReply(question("Show the temperatures"), question("What happened overnight?"), question("Stop the music")),
	} {
		if got, err := parseStarterQuestions(content); err == nil {
			t.Fatalf("%s: parsed %q, want an error", name, titles(got))
		}
	}
}

func TestListStarterQuestionsRouteServesCache(t *testing.T) {
	t.Parallel()
	generatedAt := time.Date(2026, 11, 24, 19, 0, 0, 0, time.UTC)
	cases := map[string]struct {
		cache StarterQuestions
		want  string
	}{
		"before first pass": {
			cache: StarterQuestions{Enabled: true},
			want:  `{"starter_questions":[],"enabled":true,"generated_at":null}`,
		},
		"off": {
			want: `{"starter_questions":[],"enabled":false,"generated_at":null}`,
		},
		"cached": {
			cache: StarterQuestions{
				Questions: []StarterQuestion{
					{Title: "Which devices are offline?", Prompt: "Which devices are offline right now?"},
				},
				GeneratedAt: generatedAt,
				Enabled:     true,
			},
			want: `{"starter_questions":[{"title":"Which devices are offline?","prompt":"Which devices are offline right now?"}],` +
				`"enabled":true,"generated_at":"2026-11-24T19:00:00Z"}`,
		},
	}
	for name, test := range cases {
		router := newOperationsRouter(&stubOperations{starterQuestions: test.cache})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agent/starter-questions", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
		var got, want map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		delete(got, "$schema")
		if err := json.Unmarshal([]byte(test.want), &want); err != nil {
			t.Fatal(err)
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%s: body=%s want %s", name, response.Body.String(), test.want)
		}
	}
}

func TestRefreshStarterQuestionsRouteMapsOutcomes(t *testing.T) {
	t.Parallel()
	staged := func(stage string) error {
		return starterQuestionError(stage, errors.New("private cause"))
	}
	cases := map[string]struct {
		err        error
		wantStatus int
		wantDetail string
	}{
		"refreshed": {wantStatus: http.StatusOK},
		"off": {
			err:        ErrStarterQuestionsOff,
			wantStatus: http.StatusServiceUnavailable,
			wantDetail: "starter questions are off",
		},
		"closed": {
			err:        ErrAdmissionUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantDetail: "agent admission is unavailable",
		},
		"model": {
			err:        staged(StarterQuestionStageModel),
			wantStatus: http.StatusBadGateway,
			wantDetail: "starter question generation failed",
		},
		"reply": {
			err:        staged(StarterQuestionStageReply),
			wantStatus: http.StatusBadGateway,
			wantDetail: "starter question generation failed",
		},
		"timeout": {
			err:        staged(StarterQuestionStageTimeout),
			wantStatus: http.StatusBadGateway,
			wantDetail: "starter question generation failed",
		},
		"digest read":  {err: staged(StarterQuestionStageDigestRead), wantStatus: http.StatusInternalServerError},
		"unclassified": {err: errors.New("private cause"), wantStatus: http.StatusInternalServerError},
	}
	for name, test := range cases {
		stub := &stubOperations{
			starterQuestions: StarterQuestions{
				Questions: []StarterQuestion{{Title: "Which devices are offline?", Prompt: "Which are offline?"}},
				Enabled:   true,
			},
			refreshStarters: func(context.Context) error { return test.err },
		}
		response := httptest.NewRecorder()
		newOperationsRouter(stub).ServeHTTP(
			response, httptest.NewRequest(http.MethodPost, "/v1/agent/starter-questions/refresh", nil),
		)
		if response.Code != test.wantStatus {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private cause") {
			t.Fatalf("%s: response exposes the cause: %s", name, response.Body.String())
		}
		var body struct {
			Detail           string `json:"detail"`
			StarterQuestions []struct {
				Title string `json:"title"`
			} `json:"starter_questions"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if test.wantDetail != "" && body.Detail != test.wantDetail {
			t.Fatalf("%s: detail = %q, want %q", name, body.Detail, test.wantDetail)
		}
		if test.err == nil &&
			(len(body.StarterQuestions) != 1 || body.StarterQuestions[0].Title != "Which devices are offline?") {
			t.Fatalf("%s: body = %s", name, response.Body.String())
		}
	}
}

func TestRefreshStarterQuestionsRouteReportsCallerCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	stub := &stubOperations{refreshStarters: func(context.Context) error {
		cancel()
		return starterQuestionError(StarterQuestionStageModel, context.Canceled)
	}}
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/agent/starter-questions/refresh", nil)
	newOperationsRouter(stub).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(response.Body.String(), "starter question refresh canceled") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
