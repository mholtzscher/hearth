//go:build e2e

package hearthd //nolint:testpackage // Public journeys use production Core, HTTP/MCP and the Adapter SDK.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type expressionJourneyExecution struct {
	Sequence int    `json:"sequence"`
	StepID   string `json:"step_id"`
	Kind     string `json:"kind"`
	Phase    string `json:"phase"`
	Evidence struct {
		Output         json.RawMessage `json:"output"`
		Values         json.RawMessage `json:"values"`
		Inputs         json.RawMessage `json:"inputs"`
		UnusableReason string          `json:"unusable_reason"`
		CommandID      string          `json:"command_id"`
		Failure        *struct {
			Code string `json:"failure_code"`
		} `json:"value_failure"`
	} `json:"evidence"`
}

type expressionJourneyRun struct {
	Status         string `json:"status"`
	FailureCode    string `json:"failure_code"`
	DataEvidence   string `json:"data_evidence"`
	Initialization struct {
		Kind   string          `json:"kind"`
		Values json.RawMessage `json:"values"`
	} `json:"value_initialization"`
	Executions []expressionJourneyExecution `json:"executions"`
}

//nolint:gocognit,gocyclo,cyclop // The journey checks one complete value flow and its required-output negative control through both transports.
func TestAutomationExpressionsPublicValueFlow(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"http", "mcp"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			broker := startLifecycleNATSServer(t)
			core := startFunctionCore(
				t,
				Config{
					HouseholdTimezone: "UTC",
					NATSURL:           broker.ClientURL(),
					SQLitePath:        filepath.Join(t.TempDir(), "expressions.db"),
					Agent:             requiredAgentConfig(t),
				},
			)
			client := newNonPoolingHTTPClient(t)
			mcpClient := connectAutomationFunctionsMCP(t, core)
			source := startSliceAdapter(t.Context(), t, broker, core.address)
			t.Cleanup(source.stop)
			waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
				raw := successfulFunctionCoreRequest(
					t,
					client,
					core,
					http.MethodGet,
					"/v1/entities/"+source.powerEntityID,
					"",
				)
				return strings.Contains(string(raw), `"value":false`), nil
			})
			session, err := adapter.Connect(
				t.Context(),
				adapter.Config{
					AdapterID:       "expression-functions",
					SoftwareName:    "synthetic-expression-journey",
					SoftwareVersion: "1",
					NATSURL:         broker.ClientURL(),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			calls := make(chan adapter.FunctionRequest, 2)
			notifications := make(chan adapter.NotificationRequest, 2)
			const output = `{"hours":[{"rain":true,"temperature":10},{"rain":false,"temperature":20}],"sequence":9007199254740993}`
			_, err = session.StartFunctionServer(
				t.Context(),
				[]adapter.Function{{Descriptor: adapter.FunctionDescriptor{
					Key:         "forecast",
					Description: "Synthetic household forecast",
					TimeoutMS:   60000,
					InputSchema: json.RawMessage(
						`{"type":"object","required":["hours"],"properties":{"hours":{"type":"integer","const":2}},"additionalProperties":false}`,
					),
					OutputSchema: json.RawMessage(
						`{"type":"object","required":["hours","sequence"],"properties":{"hours":{"type":"array","items":{"type":"object","required":["rain","temperature"],"properties":{"rain":{"type":"boolean"},"temperature":{"type":"integer"}},"additionalProperties":false}},"sequence":{"type":"integer"}},"additionalProperties":false}`,
					),
				}, Handler: func(_ context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
					calls <- request
					return adapter.FunctionSucceeded{Result: json.RawMessage(output)}
				}}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = session.StartNotificationServer(
				t.Context(),
				func(_ context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
					notifications <- request
					return adapter.NotificationOutcome{Outcome: "accepted"}
				},
			); err != nil {
				t.Fatal(err)
			}
			destination, err := session.RegisterNotificationDestination(t.Context(), "home", "Home")
			if err != nil {
				t.Fatal(err)
			}
			discovery := decodeNotification[automationFunctionDiscovery](
				t,
				successfulFunctionCoreRequest(
					t,
					client,
					core,
					http.MethodGet,
					"/v1/adapters/expression-functions/functions/forecast",
					"",
				),
			)
			definition := fmt.Sprintf(
				`{"name":"Forecast value flow","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"0 18 * * *"}],
				"variables":{"target":25},"variables_expr":{"seed":"trigger.kind == 'manual' ? 1 : 0"},"steps":[
				{"id":"forecast","kind":"function","adapter_id":"expression-functions","function_key":"forecast","contract_fingerprint":%q,"arguments_expr":"{hours: vars.seed + 1}"},
				{"id":"before","kind":"read_state","entity_id":%q,"value_pointer":""},
				{"id":"outside","kind":"read_state","entity_id":%q,"value_pointer":"/missing","on_unusable":"null"},
				{"id":"calculate","kind":"set_variables","assignments_expr":{"target":"len(map(filter(steps.forecast.output.hours, {.rain}), {.temperature})) == 1 ? 60 : 25","outside":"steps.outside.output ?? 20"}},
				{"id":"choose","kind":"if","conditions":{"id":"wet","kind":"expression","expr":"vars.target > 50 && vars.seed == 1 && vars.outside == 20 && all(steps.forecast.output.hours, {.temperature > 0})"},"then":[
				{"id":"apply","kind":"command","entity_id":%q,"operation":"set","parameters_expr":"{value: vars.target > 50}"},
				{"id":"notify","kind":"notification","destination_id":%q,"title_expr":"'Target ' + string(vars.target)","message_expr":"join(map(steps.forecast.output.hours, {string(.temperature)}), ',')"}]},
				{"id":"wait","kind":"delay","duration_ms":1}]}`,
				discovery.ContractFingerprint,
				source.powerEntityID,
				source.powerEntityID,
				source.powerEntityID,
				destination,
			)
			create := func(document string) string {
				t.Helper()
				if protocol == "http" {
					return createBranchingAutomation(t.Context(), t, core.address, document)
				}
				receipt := decodeNotification[struct {
					AutomationID string `json:"automation_id"`
				}](t, automationFunctionsMCP(t, mcpClient, "create_automation", `{"definition":`+document+`}`))
				return receipt.AutomationID
			}
			runManual := func(id string) string {
				t.Helper()
				if protocol == "mcp" {
					automationFunctionsMCP(
						t,
						mcpClient,
						"start_automation_run",
						fmt.Sprintf(`{"automation_id":%q}`, id),
					)
				} else {
					response := conditionsManualRun(t.Context(), t, core.address, id, "")
					defer response.Body.Close()
					if response.StatusCode != http.StatusAccepted {
						t.Fatalf("admission: %d %s", response.StatusCode, readSliceBody(t, response))
					}
				}
				return awaitAutomationFunctionRun(t, client, core, id, map[string]bool{})
			}
			automationID := create(definition)
			runID := runManual(automationID)
			httpRaw := successfulFunctionCoreRequest(
				t,
				client,
				core,
				http.MethodGet,
				"/v1/automations/"+automationID+"/history/"+runID,
				"",
			)
			run := decodeNotification[struct {
				Run expressionJourneyRun `json:"run"`
			}](t, httpRaw).Run
			if run.Status != "succeeded" || run.DataEvidence != "recorded" ||
				run.Initialization.Kind != "initialized" ||
				len(run.Executions) != 8 {
				t.Fatalf("Run does not prove unified value flow: %s", httpRaw)
			}
			for i, execution := range run.Executions {
				if execution.Sequence != i || execution.Phase != "succeeded" {
					t.Fatalf("invalid selected trace: %s", httpRaw)
				}
			}
			var captured, unusable, outputCopied, assigned, command, delay bool
			for _, execution := range run.Executions {
				if execution.Kind == "read_state" && execution.StepID == "before" {
					captured = string(execution.Evidence.Output) == "false"
				}
				if execution.Kind == "read_state" && execution.StepID == "outside" {
					unusable = string(execution.Evidence.Output) == "null" &&
						execution.Evidence.UnusableReason == "pointer_missing"
				}
				if execution.Kind == "function" {
					outputCopied = strings.Contains(string(execution.Evidence.Output), "9007199254740993") &&
						string(execution.Evidence.Inputs) == `{"arguments":{"hours":2}}`
				}
				if execution.Kind == "set_variables" {
					assigned = strings.Contains(string(execution.Evidence.Values), `"target":60`)
				}
				if execution.Kind == "command" {
					command = execution.Evidence.CommandID != "" &&
						string(execution.Evidence.Inputs) == `{"parameters":{"value":true}}`
				}
				if execution.Kind == "delay" {
					delay = true
				}
			}
			if !captured || !unusable || !outputCopied || !assigned || !command || !delay {
				t.Fatalf("retained values missing: %s", httpRaw)
			}
			select {
			case call := <-calls:
				if string(call.Arguments) != `{"hours":2}` {
					t.Fatalf("resolved Function arguments: %s", call.Arguments)
				}
			default:
				t.Fatal("Function was not invoked")
			}
			select {
			case notification := <-notifications:
				if notification.Message.Title != "Target 60" || notification.Message.Text != "10,20" {
					t.Fatalf("resolved notification: %+v", notification)
				}
			default:
				t.Fatal("notification was not sent")
			}
			mcpRaw := automationFunctionsMCP(
				t,
				mcpClient,
				"get_automation_history_entry",
				fmt.Sprintf(`{"automation_id":%q,"entry_id":%q}`, automationID, runID),
			)
			mcpRun := decodeNotification[expressionJourneyRun](t, mcpRaw)
			if mcpRun.DataEvidence != "recorded" || len(mcpRun.Executions) != len(run.Executions) ||
				!strings.Contains(string(mcpRaw), "9007199254740993") {
				t.Fatalf("MCP exact evidence parity: %s", mcpRaw)
			}
			state := successfulFunctionCoreRequest(
				t,
				client,
				core,
				http.MethodGet,
				"/v1/entities/"+source.powerEntityID,
				"",
			)
			if !strings.Contains(string(state), `"value":true`) {
				t.Fatalf("fixed-target effect did not change State: %s", state)
			}
			missing := fmt.Sprintf(
				`{"name":"Required unreached output","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"0 18 * * *"}],"steps":[{"id":"unselected","kind":"if","conditions":{"id":"false","kind":"expression","expr":"false"},"then":[{"id":"forecast","kind":"function","adapter_id":"expression-functions","function_key":"forecast","contract_fingerprint":%q,"arguments":{"hours":2}}]},{"id":"must-not-send","kind":"command","entity_id":%q,"operation":"set","parameters_expr":"{value: steps.forecast.output.sequence > 0}"}]}`,
				discovery.ContractFingerprint,
				source.powerEntityID,
			)
			missingID := create(missing)
			failedID := runManual(missingID)
			failedRaw := successfulFunctionCoreRequest(
				t,
				client,
				core,
				http.MethodGet,
				"/v1/automations/"+missingID+"/history/"+failedID,
				"",
			)
			failed := decodeNotification[struct {
				Run expressionJourneyRun `json:"run"`
			}](t, failedRaw).Run
			if failed.Status != "failed" || failed.FailureCode != "expression_evaluation" ||
				len(failed.Executions) != 2 ||
				failed.Executions[1].Phase != "failed" ||
				failed.Executions[1].Evidence.CommandID != "" ||
				failed.Executions[1].Evidence.Failure == nil ||
				failed.Executions[1].Evidence.Failure.Code != "expression_evaluation" {
				t.Fatalf("required missing output fabricated a reservation: %s", failedRaw)
			}
			select {
			case call := <-calls:
				t.Fatalf("unselected Function executed: %+v", call)
			default:
			}
		})
	}
}
