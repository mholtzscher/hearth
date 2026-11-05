//go:build integration

package agent //nolint:testpackage // Tests inspect turn failure identity and persisted native messages.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func TestDashboardArtifactSurvivesRealSSEAndHistory(t *testing.T) {
	t.Parallel()
	args := `{"definition":` + string(dashboardDefinitionFixture(t, "SSE", true)) + `}`
	modelCalls := 0
	script := &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			modelCalls++
			if modelCalls == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("sse-id", "present_view", args)},
				), nil
			}
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil)))
	conversation := mustCreateConversation(t, service)
	router := echo.New()
	RegisterHTTPStream(router, service, nil)
	recorder := postStreamBodyForConversation(t, router, conversation.ID, `{"text":"Show it"}`)
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	events := parseStreamEvents(t, recorder.Body.String())
	if len(events) != 5 || events[2].Type != EventToolFinished || !strings.Contains(events[2].Result, "[truncated]") ||
		events[3].Type != EventViewPresented ||
		events[3].CallID != "sse-id" ||
		events[3].Artifact == nil ||
		len(events[3].Artifact.Definition) <= maxEventResult {
		t.Fatalf("events=%+v", events)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || len(history[2].Artifacts) != 1 ||
		history[2].Artifacts[0].ArtifactID != events[3].Artifact.ArtifactID ||
		string(history[2].Artifacts[0].Definition) != string(events[3].Artifact.Definition) {
		t.Fatalf("history=%+v", history)
	}
}

func TestCommittedViewDoesNotReloadUnboundedHistory(t *testing.T) {
	t.Parallel()
	args := `{"definition":` + string(dashboardDefinitionFixture(t, "View", false)) + `}`
	steps := 0
	script := &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			steps++
			if steps == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("view-id", "present_view", args)},
				), nil
			}
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil)))
	conversation := mustCreateConversation(t, service)
	if _, err := service.database.Exec(
		`INSERT INTO agent_messages (conversation_id, role, message_json, created_at) VALUES (?, 'user', '[]', 'invalid')`,
		conversation.ID,
	); err != nil {
		t.Fatal(err)
	}
	user, err := json.Marshal(schema.UserMessage("recent"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.database.Exec(
		`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < ?) INSERT INTO agent_messages (conversation_id, role, message_json, created_at) SELECT ?, 'user', ?, '2026-01-01T00:00:00Z' FROM n`,
		historyRowBudget,
		conversation.ID,
		string(user),
	); err != nil {
		t.Fatal(err)
	}
	if _, err = service.History(t.Context(), conversation.ID); err == nil {
		t.Fatal("fixture did not corrupt unbounded history")
	}
	turn, err := service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Create"})
	if err != nil || turn.Reply != "Done." || len(turn.Artifacts) != 1 {
		t.Fatalf("completed turn=%+v err=%v", turn, err)
	}
}

func TestInvalidViewContextCannotPersistOrRunModel(t *testing.T) {
	t.Parallel()
	modelCalls := 0
	script := &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			modelCalls++
			return schema.AssistantMessage("unexpected", nil), nil
		},
	}
	service := newTurnService(t, script)
	conversation := mustCreateConversation(t, service)
	definition, err := dashboards.ParseDefinition(dashboardDefinitionFixture(t, "Draft", false))
	if err != nil {
		t.Fatal(err)
	}
	id := dashboards.DashboardID("dash_018f0f4a-7b29-7000-8000-000000000001")
	for _, view := range []*ViewContext{{Definition: dashboards.Definition{}}, {Definition: definition, SourceDashboardID: &id}} {
		if _, sendErr := service.SendMessage(
			t.Context(),
			conversation.ID,
			MessageInput{Text: "Edit", ViewContext: view},
		); sendErr == nil {
			t.Fatal("invalid context accepted")
		}
	}
	var count int
	if queryErr := service.database.QueryRow(`SELECT count(*) FROM agent_messages WHERE conversation_id = ?`, conversation.ID).
		Scan(&count); queryErr != nil {
		t.Fatal(queryErr)
	}
	if count != 0 || modelCalls != 0 {
		t.Fatalf("persisted rows=%d model calls=%d", count, modelCalls)
	}
}

func TestUnsupportedRetainedDashboardDataRemainsRecoverable(t *testing.T) {
	t.Parallel()
	definition := strings.Replace(
		string(dashboardDefinitionFixture(t, "Future", false)),
		`"schema_version":1`,
		`"schema_version":2`,
		1,
	)
	definition = strings.Replace(
		definition,
		`"title":"Future"`,
		`"future_property":{"data":"opaque","precise":9007199254740993},"title":"Future"`,
		1,
	)
	contextRaw := json.RawMessage(`{"definition":` + definition + `}`)
	artifactRaw := `{"kind":"hearth.view","artifact_id":"view_018f0f4a-7b29-7000-8000-000000000001","definition":` + definition + `}`
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			for _, message := range messages {
				if message.Role == schema.User && strings.Contains(message.Content, "Future") {
					t.Error("unsupported view sent to model")
				}
			}
			return schema.AssistantMessage("OK", nil), nil
		},
	}
	service := newTurnService(t, script)
	conversation := mustCreateConversation(t, service)
	user := schema.UserMessage("old prompt")
	user.Extra = map[string]any{viewContextKey: contextRaw}
	for _, message := range []*schema.Message{user, schema.AssistantMessage("", []schema.ToolCall{nativeTestCall("old-id", "present_view", `{}`)}), {Role: schema.Tool, ToolCallID: "old-id", ToolName: "present_view", Content: artifactRaw}} {
		if err := service.persistMessage(t.Context(), conversation.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	before, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 3 || !strings.Contains(string(before[0].ViewContext), `"precise":9007199254740993`) ||
		len(before[2].Artifacts) != 1 ||
		!strings.Contains(string(before[2].Artifacts[0].Definition), `"future_property"`) {
		t.Fatalf("history=%+v", before)
	}
	if _, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	after, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(before[0].ViewContext) != string(after[0].ViewContext) ||
		string(before[2].Artifacts[0].Definition) != string(after[2].Artifacts[0].Definition) {
		t.Fatal("reading or continuation changed retained data")
	}
}

func TestCommittedLargeViewProjection(t *testing.T) {
	t.Parallel()
	elements := map[string]any{}
	children := make([]string, 10)
	for i := range children {
		children[i] = string(rune('a' + i))
		elements[children[i]] = map[string]any{
			"type":  "Note",
			"props": map[string]any{"text": strings.Repeat("x", 490)},
		}
	}
	elements["root"] = map[string]any{"type": "Stack", "props": map[string]any{"gap": "small"}, "children": children}
	raw, err := json.Marshal(
		map[string]any{
			"schema_version":  1,
			"catalog_version": "hearth.dashboard/v1",
			"title":           "Large",
			"spec":            map[string]any{"root": "root", "elements": elements},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := dashboards.ParseDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := dashboards.NewService(nil, nil).PresentView(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(artifact)
	if err != nil || len(output) <= maxEventResult {
		t.Fatalf("artifact length=%d err=%v", len(output), err)
	}
	calls := 0
	script := &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			calls++
			if calls == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{nativeTestCall("native-id", "present_view", `{}`)},
				), nil
			}
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	service.tools = []tool.BaseTool{
		callbackTool{
			name:   "present_view",
			invoke: func(context.Context, string) (string, error) { return string(output), nil },
		},
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Show it"},
		func(event TurnEvent) { events = append(events, event) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Artifacts) != 1 || turn.Artifacts[0].ArtifactID != artifact.ArtifactID {
		t.Fatalf("turn artifacts: %+v", turn.Artifacts)
	}
	if len(events) != 5 || events[1].CallID != "native-id" || events[2].Type != EventToolFinished ||
		events[3].Type != EventViewPresented ||
		events[3].CallID != "native-id" ||
		events[3].Artifact == nil ||
		len(events[3].Artifact.Definition) <= maxEventResult {
		t.Fatalf("events: %+v", events)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 || len(history[1].Calls) != 1 || history[1].Calls[0].ID != "native-id" ||
		history[2].ToolCallID != "native-id" ||
		len(history[2].Artifacts) != 1 {
		t.Fatalf("history: %+v", history)
	}
}

//nolint:gocognit // Exercises the complete parallel failure and retained evidence path.
func TestParallelViewCommitFailureKeepsSibling(t *testing.T) {
	t.Parallel()
	goodArgs := `{"definition":` + string(dashboardDefinitionFixture(t, "good", true)) + `}`
	badArgs := `{"definition":` + string(dashboardDefinitionFixture(t, "bad", false)) + `}`
	modelCalls := 0
	script := &scriptedTurnModel{
		generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			modelCalls++
			if modelCalls > 1 {
				results := nativeToolResults(messages)
				if results["bad"] != unknownToolOutcome || !strings.Contains(results["good"], "hearth.view") {
					t.Errorf("repaired results=%v", results)
				}
				return schema.AssistantMessage("The second result is unknown.", nil), nil
			}
			return schema.AssistantMessage(
				"",
				[]schema.ToolCall{
					nativeTestCall("good", "present_view", goodArgs),
					nativeTestCall("bad", "present_view", badArgs),
				},
			), nil
		},
	}
	service := newTurnService(t, script)
	goodDone := make(chan struct{})
	presenter := dashboards.NewService(nil, nil)
	var presented atomic.Int32
	service.tools = mustBridgeTools(t, dashboardMCPServer(coordinatedPresenter{
		present: func(ctx context.Context, definition dashboards.Definition) (dashboards.Artifact, error) {
			presented.Add(1)
			if definition.Title == "bad" {
				select {
				case <-goodDone:
				case <-ctx.Done():
					return dashboards.Artifact{}, ctx.Err()
				}
			}
			return presenter.PresentView(ctx, definition)
		},
	}))
	conversation := mustCreateConversation(t, service)
	if _, triggerErr := service.database.Exec(
		`CREATE TRIGGER fail_view BEFORE INSERT ON agent_messages WHEN NEW.role = 'tool' AND json_extract(NEW.message_json, '$.tool_call_id') = 'bad' BEGIN SELECT RAISE(ABORT, 'failed'); END`,
	); triggerErr != nil {
		t.Fatal(triggerErr)
	}
	var events []TurnEvent
	_, err := service.SendMessageWithEvents(
		t.Context(),
		conversation.ID,
		MessageInput{Text: "Show both"},
		func(event TurnEvent) {
			events = append(events, event)
			if event.Type == EventViewPresented && event.CallID == "good" {
				close(goodDone)
			}
		},
	)
	if !errors.As(err, &tracePersistError{}) || modelCalls != 1 {
		t.Fatalf("error=%v model calls=%d", err, modelCalls)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || len(history[1].Calls) != 2 || history[1].Calls[0].ID != "good" ||
		history[1].Calls[1].ID != "bad" ||
		history[2].ToolCallID != "good" ||
		len(history[2].Artifacts) != 1 ||
		len(history[2].Artifacts[0].Definition) <= maxEventResult {
		t.Fatalf("history=%+v", history)
	}
	for _, event := range events {
		if event.CallID == "bad" && (event.Type == EventToolFinished || event.Type == EventViewPresented) {
			t.Fatalf("uncommitted event=%+v", event)
		}
	}
	if _, err = service.SendMessage(t.Context(), conversation.ID, MessageInput{Text: "What happened?"}); err != nil {
		t.Fatal(err)
	}
	assertNoPersistedRepair(t, service, conversation.ID)
	if modelCalls != 2 || presented.Load() != 2 {
		t.Fatalf("model calls=%d presentations=%d; repair must not replay", modelCalls, presented.Load())
	}
}

func TestParallelToolFailureRetainsSiblingPersistenceFailure(t *testing.T) {
	t.Parallel()
	calls := []schema.ToolCall{
		nativeTestCall("transport", "broken", `{}`),
		nativeTestCall("persist", "successful", `{}`),
	}
	modelCalls := 0
	script := &scriptedTurnModel{generate: func(
		context.Context, []*schema.Message, ...model.Option,
	) (*schema.Message, error) {
		modelCalls++
		if modelCalls > 1 {
			return schema.AssistantMessage("fabricated final", nil), nil
		}
		return schema.AssistantMessage("", calls), nil
	}}
	service := newTurnService(t, script)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	siblingStarted := make(chan struct{})
	failedFinished := make(chan struct{})
	privateCause := errors.New("private transport failure")
	service.tools = []tool.BaseTool{
		callbackTool{name: "broken", invoke: func(ctx context.Context, _ string) (string, error) {
			select {
			case <-siblingStarted:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "", toolCallError{err: privateCause}
		}},
		callbackTool{name: "successful", invoke: func(ctx context.Context, _ string) (string, error) {
			close(siblingStarted)
			select {
			case <-failedFinished:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "completed effect", nil
		}},
	}
	conversation := mustCreateConversation(t, service)
	if _, err := service.database.Exec(`CREATE TRIGGER fail_parallel_result BEFORE INSERT ON agent_messages
		WHEN NEW.role = 'tool' AND json_extract(NEW.message_json, '$.tool_call_id') = 'persist'
		BEGIN SELECT RAISE(ABORT, 'result insert failed'); END`); err != nil {
		t.Fatal(err)
	}
	var events []TurnEvent
	turn, err := service.SendMessageWithEvents(
		ctx,
		conversation.ID,
		MessageInput{Text: "Run both."},
		func(event TurnEvent) {
			events = append(events, event)
			if event.Type == EventToolFinished && event.Name == "broken" {
				close(failedFinished)
			}
		},
	)
	var persist tracePersistError
	var transport toolCallError
	if !errors.As(err, &persist) || !errors.As(err, &transport) || !errors.Is(err, privateCause) {
		t.Fatalf("error = %v, want both persistence and private transport causes", err)
	}
	if stage := runnerFailureStage(err); stage != turnStagePersist {
		t.Errorf("failure stage = %q, want persistence precedence", stage)
	}
	if modelCalls != 1 || turn.Reply != "" {
		t.Errorf("model calls = %d, turn = %+v, want no final response", modelCalls, turn)
	}
	assertParallelFailedHistory(t, service, conversation.ID)
	if len(events) == 0 || events[len(events)-1].Type != EventTurnFailed {
		t.Errorf("events = %+v, want failed turn", events)
	}
}

func assertParallelFailedHistory(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	history, err := service.loadModelMessages(t.Context(), conversationID)
	if err != nil {
		t.Fatal(err)
	}
	results := nativeToolResults(history)
	if len(history) != 3 || len(results) != 1 || results["transport"] != failedToolOutcome {
		t.Fatalf("history = %+v, want only the failed transport result retained", history)
	}
	if !strings.Contains(strings.ToLower(results["transport"]), "unknown") ||
		!strings.Contains(strings.ToLower(results["transport"]), "do not") ||
		!strings.Contains(strings.ToLower(results["transport"]), "repeat") {
		t.Errorf("failed result does not warn against repeating an unknown outcome: %q", results["transport"])
	}
}

//nolint:cyclop,gocognit,gocyclo // Checks the complete synchronized commit and event ordering scenario.
func TestParallelDashboardCommitDoesNotWaitForBlockedCallback(t *testing.T) {
	t.Parallel()
	firstArgs := `{"definition":` + string(dashboardDefinitionFixture(t, "first", false)) + `}`
	secondArgs := `{"definition":` + string(dashboardDefinitionFixture(t, "second", true)) + `}`
	modelCalls := 0
	script := &scriptedTurnModel{
		generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			modelCalls++
			if modelCalls == 1 {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{
						nativeTestCall("first-id", "present_view", firstArgs),
						nativeTestCall("second-id", "present_view", secondArgs),
					},
				), nil
			}
			return schema.AssistantMessage("Done.", nil), nil
		},
	}
	service := newTurnService(t, script)
	secondEntered, firstFinished, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	presenter := dashboards.NewService(nil, nil)
	service.tools = mustBridgeTools(
		t,
		dashboardMCPServer(
			coordinatedPresenter{
				present: func(ctx context.Context, definition dashboards.Definition) (dashboards.Artifact, error) {
					if definition.Title == "first" {
						select {
						case <-secondEntered:
						case <-ctx.Done():
							return dashboards.Artifact{}, ctx.Err()
						}
					} else {
						close(secondEntered)
						select {
						case <-firstFinished:
						case <-ctx.Done():
							return dashboards.Artifact{}, ctx.Err()
						}
					}
					return presenter.PresentView(ctx, definition)
				},
			},
		),
	)
	conversation := mustCreateConversation(t, service)
	ctx, cancel := context.WithCancel(t.Context())
	var once sync.Once
	done := make(chan struct{})
	var events []TurnEvent
	var turn Turn
	var turnErr error
	t.Cleanup(func() {
		cancel()
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("turn failed to join")
		}
	})
	go func() {
		defer close(done)
		turn, turnErr = service.SendMessageWithEvents(
			ctx,
			conversation.ID,
			MessageInput{Text: "both"},
			func(event TurnEvent) {
				events = append(events, event)
				if event.Type == EventToolFinished && event.CallID == "first-id" {
					close(firstFinished)
					<-release
				}
			},
		)
	}()
	select {
	case <-firstFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("first callback did not block")
	}
	waitForSecondParallelResult(t, service, conversation.ID)
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn failed to finish")
	}
	if turnErr != nil || len(turn.Artifacts) != 2 {
		t.Fatalf("turn=%+v err=%v", turn, turnErr)
	}
	history, err := service.History(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 5 || history[2].ToolCallID != "first-id" || history[3].ToolCallID != "second-id" ||
		len(history[2].Artifacts) != 1 ||
		len(history[3].Artifacts) != 1 ||
		turn.Artifacts[0].ArtifactID != history[2].Artifacts[0].ArtifactID ||
		turn.Artifacts[1].ArtifactID != history[3].Artifacts[0].ArtifactID {
		t.Fatalf("history=%+v turn=%+v", history, turn)
	}
	for _, id := range []string{"first-id", "second-id"} {
		positions := map[string]int{}
		for i, event := range events {
			if event.CallID == id {
				positions[event.Type] = i + 1
			}
		}
		if positions[EventToolStarted] == 0 || positions[EventToolStarted] >= positions[EventToolFinished] ||
			positions[EventToolFinished] >= positions[EventViewPresented] {
			t.Errorf("event order for %s: %+v", id, events)
		}
	}
}

func TestParallelResultPersistsWhileFinishedCallbackBlocked(t *testing.T) {
	t.Parallel()
	calls := []schema.ToolCall{
		nativeTestCall("first-id", "first", `{}`),
		nativeTestCall("second-id", "second", `{}`),
	}
	modelCalls := 0
	script := &scriptedTurnModel{generate: func(
		context.Context, []*schema.Message, ...model.Option,
	) (*schema.Message, error) {
		modelCalls++
		if modelCalls == 1 {
			return schema.AssistantMessage("", calls), nil
		}
		return schema.AssistantMessage("Done.", nil), nil
	}}
	service := newTurnService(t, script)
	ctx, cancel := context.WithCancel(t.Context())
	secondStarted := make(chan struct{})
	firstFinished := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	service.tools = []tool.BaseTool{
		callbackTool{name: "first", invoke: func(ctx context.Context, _ string) (string, error) {
			select {
			case <-secondStarted:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "first result", nil
		}},
		callbackTool{name: "second", invoke: func(ctx context.Context, _ string) (string, error) {
			close(secondStarted)
			select {
			case <-firstFinished:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			return "second result", nil
		}},
	}
	conversation := mustCreateConversation(t, service)
	var events []TurnEvent
	var turn Turn
	var turnErr error
	exited := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("turn did not join during cleanup")
		}
	})
	go func() {
		defer close(exited)
		turn, turnErr = service.SendMessageWithEvents(
			ctx,
			conversation.ID,
			MessageInput{Text: "Run both."},
			func(event TurnEvent) {
				events = append(events, event)
				if event.Type == EventToolFinished && event.Name == "first" {
					close(firstFinished)
					<-release
				}
			},
		)
	}()
	select {
	case <-firstFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("first finished callback did not start")
	}
	waitForSecondParallelResult(t, service, conversation.ID)
	select {
	case <-exited:
		t.Fatal("turn returned while its callback was blocked")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish after callback release")
	}
	if turnErr != nil || turn.Reply != "Done." || modelCalls != 2 {
		t.Fatalf("turn = %+v, error = %v, model calls = %d", turn, turnErr, modelCalls)
	}
	assertParallelToolEventOrder(t, events)
	history, err := service.loadModelMessages(t.Context(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	results := nativeToolResults(history)
	if len(history) != 5 || results["first-id"] != "first result" || results["second-id"] != "second result" {
		t.Errorf("history = %+v, want both results and final", history)
	}
}

func waitForSecondParallelResult(t *testing.T, service *Service, conversationID string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := service.database.QueryRowContext(t.Context(), `SELECT count(*) FROM agent_messages
			WHERE conversation_id = ? AND role = 'tool' AND json_extract(message_json, '$.tool_call_id') = 'second-id'`,
			conversationID).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("second result was not persisted while the first callback was blocked")
		case <-ticker.C:
		}
	}
}

func assertParallelToolEventOrder(t *testing.T, events []TurnEvent) {
	t.Helper()
	if len(events) != 6 || events[0].Type != EventTurnStarted || events[len(events)-1].Type != EventTurnFinished {
		t.Fatalf("events = %+v", events)
	}
	positions := map[string]int{}
	for i, event := range events {
		if event.Type == EventToolStarted || event.Type == EventToolFinished {
			key := event.Type + ":" + event.Name
			if _, exists := positions[key]; exists {
				t.Fatalf("duplicate tool event: %+v", events)
			}
			positions[key] = i
		}
	}
	for _, name := range []string{"first", "second"} {
		started, hasStart := positions[EventToolStarted+":"+name]
		finished, hasFinish := positions[EventToolFinished+":"+name]
		if !hasStart || !hasFinish || started >= finished {
			t.Errorf("events for %s out of order: %+v", name, events)
		}
	}
}
