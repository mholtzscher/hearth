//go:build integration

package agent //nolint:testpackage // Verifies completion against the real runner and durable history.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

//nolint:gocognit // One coordinated journey checks joining for successful and failed siblings.
func TestPresentationCompletionWaitsForSibling(t *testing.T) {
	t.Parallel()
	for _, fails := range []bool{false, true} {
		t.Run(strconv.FormatBool(fails), func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			presented := make(chan struct{})
			ended := make(chan struct{})
			failure := errors.New("sibling failed")
			definition := string(dashboardDefinitionFixture(t, "Joined", false))
			service := newTurnService(
				t,
				&scriptedTurnModel{
					generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
						return schema.AssistantMessage("", []schema.ToolCall{
							nativeTestCall(
								"marked",
								"present_view",
								`{"definition":`+definition+`,"complete_turn":true}`,
							),
							nativeTestCall("sibling", "slow", `{}`),
						}), nil
					},
				},
			)
			service.tools = append(mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil))),
				callbackTool{name: "slow", invoke: func(context.Context, string) (string, error) {
					<-release
					defer close(ended)
					if fails {
						return "", failure
					}
					return "ok", nil
				}})
			conversation := mustCreateConversation(t, service)
			done := make(chan error, 1)
			go func() {
				_, err := service.SendMessageWithEvents(
					t.Context(),
					conversation.ID,
					MessageInput{Text: "Both"},
					func(event TurnEvent) {
						if event.Type == EventViewPresented {
							close(presented)
						}
					},
				)
				done <- err
			}()
			select {
			case <-presented:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("presentation deadline")
			}
			select {
			case err := <-done:
				close(release)
				t.Fatalf("completed before sibling: %v", err)
			default:
			}
			close(release)
			select {
			case err := <-done:
				if fails && !errors.Is(err, failure) || !fails && err != nil {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("turn did not join")
			}
			select {
			case <-ended:
			default:
				t.Fatal("sibling not joined")
			}
		})
	}
}

func TestPresentationCompletionPreservesIterationLimit(t *testing.T) {
	t.Parallel()
	definition := string(dashboardDefinitionFixture(t, "Limit", false))
	service := newTurnService(
		t,
		&scriptedTurnModel{
			generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
				return schema.AssistantMessage(
					"",
					[]schema.ToolCall{
						nativeTestCall("marked", "present_view", `{"definition":`+definition+`,"complete_turn":true}`),
					},
				), nil
			},
		},
	)
	service.tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil)))
	service.maxIterations = 1
	_, err := service.SendMessage(t.Context(), mustCreateConversation(t, service).ID, MessageInput{Text: "Show"})
	if !errors.Is(err, adk.ErrExceedMaxIterations) {
		t.Fatalf("error=%v", err)
	}
}

func TestMixedActionPresentationRetainsFinalReply(t *testing.T) {
	t.Parallel()
	definition := string(dashboardDefinitionFixture(t, "After command", false))
	generations, effects := 0, 0
	service := newTurnService(
		t,
		&scriptedTurnModel{
			generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
				generations++
				switch generations {
				case 1:
					return schema.AssistantMessage(
						"",
						[]schema.ToolCall{nativeTestCall("command", "execute_entity_command", `{}`)},
					), nil
				case 2:
					if nativeToolResults(messages)["command"] != "cmd-1 satisfied" {
						t.Error("missing Command result")
					}
					return schema.AssistantMessage(
						"",
						[]schema.ToolCall{
							nativeTestCall(
								"view",
								"present_view",
								`{"definition":`+definition+`,"complete_turn":false}`,
							),
						},
					), nil
				default:
					return schema.AssistantMessage("Command cmd-1 satisfied.", nil), nil
				}
			},
		},
	)
	service.tools = append(mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil))),
		callbackTool{name: "execute_entity_command", invoke: func(context.Context, string) (string, error) {
			effects++
			return "cmd-1 satisfied", nil
		}})
	turn, err := service.SendMessage(
		t.Context(),
		mustCreateConversation(t, service).ID,
		MessageInput{Text: "Act and show"},
	)
	if err != nil || turn.Reply != "Command cmd-1 satisfied." || len(turn.Artifacts) != 1 || effects != 1 ||
		generations != 3 {
		t.Fatalf("turn=%+v err=%v effects=%d generations=%d", turn, err, effects, generations)
	}
}

func TestMarkedPresentationCannotSuppressActionOutcome(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"execute_entity_command", "invoke_adapter_function", "send_notification", "start_automation_run", "delete_automation", "update_entity"} {
		for _, sameBatch := range []bool{false, true} {
			t.Run(action+"/same-batch="+strconv.FormatBool(sameBatch), func(t *testing.T) {
				t.Parallel()
				testMarkedActionOutcome(t, action, sameBatch)
			})
		}
	}
}

func testMarkedActionOutcome(t *testing.T, action string, sameBatch bool) {
	t.Helper()
	definition := string(dashboardDefinitionFixture(t, "After action", false))
	view := nativeTestCall("view", "present_view", `{"definition":`+definition+`,"complete_turn":true}`)
	generations, effects := 0, 0
	service := newTurnService(
		t,
		&scriptedTurnModel{
			generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
				generations++
				if generations == 1 {
					calls := []schema.ToolCall{nativeTestCall("action", action, `{}`)}
					if sameBatch {
						calls = append(calls, view)
					}
					return schema.AssistantMessage("", calls), nil
				}
				if nativeToolResults(messages)["action"] != "outcome retained" {
					t.Error("action outcome lost")
				}
				if generations == 2 && !sameBatch {
					return schema.AssistantMessage("", []schema.ToolCall{view}), nil
				}
				return schema.AssistantMessage("The action outcome was retained.", nil), nil
			},
		},
	)
	service.tools = append(
		mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil))),
		callbackTool{
			name:   action,
			invoke: func(context.Context, string) (string, error) { effects++; return "outcome retained", nil },
		},
	)
	turn, err := service.SendMessage(
		t.Context(),
		mustCreateConversation(t, service).ID,
		MessageInput{Text: "Act and show"},
	)
	wantGenerations := 3
	if sameBatch {
		wantGenerations = 2
	}
	if err != nil || turn.Reply != "The action outcome was retained." || len(turn.Artifacts) != 1 || effects != 1 ||
		generations != wantGenerations {
		t.Fatalf("turn=%+v error=%v effects=%d generations=%d", turn, err, effects, generations)
	}
}

//nolint:gocognit // Sequential and parallel journeys share the same durable completion assertions.
func TestPresentationCompletionAfterMultipleArtifacts(t *testing.T) {
	t.Parallel()
	for _, parallel := range []bool{false, true} {
		t.Run(strconv.FormatBool(parallel), func(t *testing.T) {
			t.Parallel()
			definition := string(dashboardDefinitionFixture(t, "Multiple", false))
			first := nativeTestCall("first", "present_view", `{"definition":`+definition+`}`)
			last := nativeTestCall("last", "present_view", `{"definition":`+definition+`,"complete_turn":true}`)
			generations := 0
			service := newTurnService(t, &scriptedTurnModel{
				generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
					generations++
					if generations == 1 {
						calls := []schema.ToolCall{first}
						if parallel {
							calls = append(calls, last)
						}
						return schema.AssistantMessage("hidden narration", calls), nil
					}
					if !parallel && generations == 2 {
						return schema.AssistantMessage("", []schema.ToolCall{last}), nil
					}
					return nil, errors.New("unexpected provider generation after final presentation")
				},
			})
			service.tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil)))
			service.maxIterations = 3
			wantGenerations, wantRows := 2, 6
			if parallel {
				service.maxIterations = 2
				wantGenerations, wantRows = 1, 5
			}
			conversation := mustCreateConversation(t, service)
			var events []TurnEvent
			turn, err := service.SendMessageWithEvents(t.Context(), conversation.ID, MessageInput{Text: "Show both"},
				func(event TurnEvent) { events = append(events, event) })
			if err != nil || turn.Reply != "" || len(turn.Artifacts) != 2 || generations != wantGenerations {
				t.Fatalf("turn=%+v error=%v generations=%d", turn, err, generations)
			}
			presented := 0
			for _, event := range events {
				if event.Type == EventViewPresented {
					presented++
				}
				if event.Type == EventTurnFinished && presented != 2 {
					t.Fatal("finished before both artifacts")
				}
			}
			if events[len(events)-1].Type != EventTurnFinished {
				t.Fatalf("events=%+v", events)
			}
			history, err := service.History(t.Context(), conversation.ID)
			if err != nil || len(history) != wantRows || history[wantRows-1].Role != "assistant" ||
				history[wantRows-1].Content != "" {
				t.Fatalf("history=%+v error=%v", history, err)
			}
		})
	}
}

func TestPresentationCompletionFailureRequiresFreshDecision(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"modeled", "invalid arguments"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			server := dashboardMCPServer(dashboards.NewService(nil, nil, nil))
			server.Raw().AddTool(&mcp.Tool{Name: "inspect", InputSchema: map[string]any{"type": "object"}},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: "reading unavailable"}},
					}, nil
				})
			definition := string(dashboardDefinitionFixture(t, "Retained", false))
			generations := 0
			service := newTurnService(t, &scriptedTurnModel{
				generate: func(_ context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
					generations++
					switch generations {
					case 1:
						arguments := `{}`
						if failure == "invalid arguments" {
							arguments = `[]`
						}
						return schema.AssistantMessage("", []schema.ToolCall{
							nativeTestCall(
								"marked",
								"present_view",
								`{"definition":`+definition+`,"complete_turn":true}`,
							),
							nativeTestCall("failed", "inspect", arguments),
						}), nil
					case 2:
						if nativeToolResults(messages)["failed"] == "" {
							t.Error("failure missing from continuation")
						}
						return schema.AssistantMessage(
							"",
							[]schema.ToolCall{
								nativeTestCall("unmarked", "present_view", `{"definition":`+definition+`}`),
							},
						), nil
					default:
						return schema.AssistantMessage("The other reading is unavailable.", nil), nil
					}
				},
			})
			service.tools = mustBridgeTools(t, server)
			turn, err := service.SendMessage(
				t.Context(),
				mustCreateConversation(t, service).ID,
				MessageInput{Text: "Show and inspect"},
			)
			if err != nil || turn.Reply != "The other reading is unavailable." || len(turn.Artifacts) != 2 ||
				generations != 3 {
				t.Fatalf("turn=%+v error=%v generations=%d", turn, err, generations)
			}
		})
	}
}

//nolint:gocognit // The failure matrix verifies terminal events and partial durable history together.
func TestPresentationCompletionCannotHideCancellationOrPersistenceFailure(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"cancel", "tool commit", "final commit"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			definition := string(dashboardDefinitionFixture(t, "Retained", false))
			generations := 0
			service := newTurnService(t, &scriptedTurnModel{
				generate: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
					generations++
					return schema.AssistantMessage(
						"",
						[]schema.ToolCall{
							nativeTestCall(
								"marked",
								"present_view",
								`{"definition":`+definition+`,"complete_turn":true}`,
							),
						},
					), nil
				},
			})
			service.tools = mustBridgeTools(t, dashboardMCPServer(dashboards.NewService(nil, nil, nil)))
			conversation := mustCreateConversation(t, service)
			if failure != "cancel" {
				condition := `NEW.role = 'tool'`
				if failure == "final commit" {
					condition = `NEW.role = 'assistant' AND json_extract(NEW.message_json, '$.tool_calls') IS NULL`
				}
				if _, err := service.database.Exec(
					`CREATE TRIGGER fail_completion BEFORE INSERT ON agent_messages WHEN ` + condition + ` BEGIN SELECT RAISE(ABORT, 'failed'); END`,
				); err != nil {
					t.Fatal(err)
				}
			}
			var events []TurnEvent
			_, err := service.SendMessageWithEvents(
				ctx,
				conversation.ID,
				MessageInput{Text: "Show"},
				func(event TurnEvent) {
					events = append(events, event)
					if event.Type == EventViewPresented && failure == "cancel" {
						cancel()
					}
				},
			)
			var persist tracePersistError
			if failure == "cancel" && !errors.Is(err, context.Canceled) ||
				failure != "cancel" && !errors.As(err, &persist) {
				t.Fatalf("failure=%s error=%v", failure, err)
			}
			if generations != 1 || events[len(events)-1].Type != EventTurnFailed {
				t.Fatalf("generations=%d events=%+v", generations, events)
			}
			history, historyErr := service.loadModelMessages(t.Context(), conversation.ID)
			if historyErr != nil {
				t.Fatal(historyErr)
			}
			if len(history) > 3 || len(history) < 2 ||
				!strings.Contains(history[1].ToolCalls[0].Function.Arguments, `"complete_turn":true`) {
				t.Fatalf("partial history=%+v", history)
			}
		})
	}
}
