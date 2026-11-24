//go:build e2e

package agenteval //nolint:testpackage // Drives Run with the package's own options and records.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// judgeReply is the fake provider's answer to every judge request.
const judgeReply = `{"grades":[{"criterion":1,"grade":"pass","reason":"names the Simulated light"}]}`

// fakeProvider answers every Chat Completions request with one text reply,
// and judge requests with judgeReply. It is a synthetic peer: it proves the
// driver, workers, judge plumbing, and report, not any hosted model's behavior.
func fakeProvider(t *testing.T, reply string) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var requests, judged atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fakeProviderKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		answer := reply
		if bytes.Contains(body, []byte("agent-evaluation judge")) {
			judged.Add(1)
			answer = judgeReply
		} else {
			requests.Add(1)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		content := strings.ReplaceAll(answer, `"`, `\"`)
		_, _ = fmt.Fprintf(
			w,
			"data: {\"id\":\"fake\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"%s\"}}]}\n\n"+
				"data: {\"id\":\"fake\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: {\"id\":\"fake\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":7,\"total_tokens\":107,"+
				"\"prompt_tokens_details\":{\"cached_tokens\":40}}}\n\ndata: [DONE]\n\n",
			content,
		)
	}))
	t.Cleanup(server.Close)
	return server, &requests, &judged
}

func TestCompareBuildsAlternatesScoresAndResumes(t *testing.T) {
	t.Parallel()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scenarios := t.TempDir()
	source, err := os.ReadFile(filepath.Join(repo, "testdata", "agenteval", "scenarios", "question-temperature.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(scenarios, "question-temperature.yaml"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, requests, judged := fakeProvider(t, "The Simulated light reads 25 °C.")
	settings := ModelSettings{
		Provider:        "opencode-go",
		Model:           "fake",
		ReasoningEffort: "low",
		BaseURL:         provider.URL + "/v1",
	}
	judge, err := NewJudge(ModelSettings{Provider: "opencode-go", Model: "fake-judge", BaseURL: settings.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	options := func(trials int, resume bool) RunOptions {
		return RunOptions{
			Judge: judge,
			Repo:  repo, ScenarioDir: scenarios, Trials: trials, Resume: resume, Out: out,
			SimulatorConfig: filepath.Join(repo, "testdata", "agenteval", "household.simulator.yaml"),
			Arms: []Arm{
				{Name: ArmBaseline, Ref: WorktreeRef, Settings: settings},
				{Name: ArmCandidate, Ref: WorktreeRef, Settings: settings},
			},
			TrialMaxRequests: 5, RunMaxRequests: 100, Rule: DefaultRule(trials), Log: &bytes.Buffer{},
		}
	}
	summary, err := Run(context.Background(), options(2, false))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Verdict != VerdictPass || summary.Scenarios[0].MatchedPairs != 2 {
		t.Fatalf("summary = %+v", summary)
	}
	first := requests.Load()
	if first != 4 {
		t.Fatalf("fake provider saw %d requests, want one per trial", first)
	}
	lines := trialLines(t, out)
	if len(lines) != 4 {
		t.Fatalf("trials.jsonl has %d lines", len(lines))
	}
	assertJudged(t, judged.Load(), lines, summary)
	// Alternation: trial 0 runs baseline first, trial 1 candidate first.
	if !strings.Contains(lines[0], `"arm":"baseline"`) || !strings.Contains(lines[2], `"arm":"candidate"`) {
		t.Fatalf("arms did not alternate: %s / %s", lines[0][:40], lines[2][:40])
	}
	report, err := WriteReport(out)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecret(t, out)
	if raw, _ := os.ReadFile(report); !bytes.Contains(raw, []byte(`"verdict":"pass"`)) {
		t.Fatal("report lacks the verdict")
	}

	// Interrupt the run: mark the second trial a provider error, drop the last
	// trial, and leave half a line, as a crash mid-write would. Resume must
	// rerun both and replace the failed attempt.
	failed := strings.Replace(strings.Replace(lines[1], `"outcome":"completed"`, `"outcome":"provider_error"`, 1),
		`"scored":true`, `"scored":false`, 1)
	if err = os.WriteFile(
		filepath.Join(out, "trials.jsonl"),
		[]byte(lines[0]+"\n"+failed+"\n"+lines[2]+"\n"+lines[3][:20]),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(context.Background(), options(2, true)); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != first+2 {
		t.Fatalf("resume made %d requests, want the missing and the failed trial's 2", got-first)
	}
	assertAllScored(t, out, 4)
	if _, err = Run(
		context.Background(),
		options(3, true),
	); err == nil ||
		!strings.Contains(err.Error(), "trial count") {
		t.Fatalf("resume with changed setup: %v", err)
	}
	if _, err = Run(context.Background(), options(2, false)); err == nil {
		t.Fatal("a second run into the same directory was accepted")
	}
	selfJudged := options(1, false)
	selfJudged.Out = t.TempDir()
	selfJudged.Judge = &Judge{Settings: settings}
	assertJudgeRefused(t, selfJudged)
}

// assertJudged checks that every trial was judged and the summary tallies the
// grades, which come from the fake provider's judgeReply.
func assertJudged(t *testing.T, judged int32, lines []string, summary Summary) {
	t.Helper()
	if judged != int32(len(lines)) || !strings.Contains(lines[0], `"judgment":{"grades":[{"criterion"`) {
		t.Fatalf("judge graded %d of %d trials; first line: %.600s", judged, len(lines), lines[0])
	}
	if tally := summary.Scenarios[0].Arms[ArmCandidate].Judge; len(tally) != 1 || tally[0].Pass != len(lines)/2 {
		t.Fatalf("candidate judge tally = %+v", tally)
	}
}

func assertAllScored(t *testing.T, out string, want int) {
	t.Helper()
	_, trials, err := LoadRun(out)
	if err != nil || len(trials) != want ||
		slices.ContainsFunc(trials, func(trial ArmTrial) bool { return !trial.Scored.Scored }) {
		t.Fatalf("run holds %d trials, err %v; want %d, all scored", len(trials), err, want)
	}
}

func assertJudgeRefused(t *testing.T, options RunOptions) {
	t.Helper()
	if _, err := Run(context.Background(), options); err == nil || !strings.Contains(err.Error(), "judge model") {
		t.Fatalf("a judge sharing the Agent model was accepted: %v", err)
	}
}

func trialLines(t *testing.T, out string) []string {
	t.Helper()
	file, err := os.Open(filepath.Join(out, "trials.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func assertNoSecret(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() ||
			strings.Contains(path, string(filepath.Separator)+"bin"+string(filepath.Separator)) {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(raw, []byte(fakeProviderKey)) {
			t.Errorf("%s contains the provider credential", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
