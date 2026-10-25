//go:build e2e

package hearthd //nolint:testpackage // Exercises the production Core through public HTTP/MCP and the Adapter SDK.

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

type expressionJourneyData struct {
	Sequence       int             `json:"sequence"`
	StepID         string          `json:"step_id"`
	Kind           string          `json:"kind"`
	Values         json.RawMessage `json:"values"`
	Output         json.RawMessage `json:"output"`
	FailureCode    string          `json:"failure_code"`
	UnusableReason string          `json:"unusable_reason"`
}

type expressionJourneyRun struct {
	Status       string                  `json:"status"`
	FailureCode  string                  `json:"failure_code"`
	DataEvidence string                  `json:"data_evidence"`
	Data         []expressionJourneyData `json:"data"`
	Steps        []struct {
		Status string `json:"status"`
	} `json:"steps"`
	Delays []json.RawMessage `json:"delays"`
}

//nolint:gocognit,gocyclo,cyclop // One public journey checks retained values after real effects and an unreached-output failure.
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
				"variables":{"seed":{"expr":"trigger.kind == 'manual' ? 1 : 0"},"target":{"literal":25}},"steps":[
				{"id":"forecast","kind":"function","adapter_id":"expression-functions","function_key":"forecast","contract_fingerprint":%q,"arguments":{"expr":"{hours: vars.seed + 1}"}},
				{"id":"before","kind":"read_state","entity_id":%q,"value_pointer":""},
				{"id":"outside","kind":"read_state","entity_id":%q,"value_pointer":"/missing","on_unusable":"null"},
				{"id":"calculate","kind":"set_variables","assignments":{"target":{"expr":"len(map(filter(steps.forecast.output.hours, {.rain}), {.temperature})) == 1 ? 60 : 25"},"outside":{"expr":"steps.outside.output ?? 20"}}},
				{"id":"choose","kind":"if","conditions":{"id":"wet","kind":"expression","value":{"expr":"vars.target > 50 && vars.seed == 1 && vars.outside == 20 && all(steps.forecast.output.hours, {.temperature > 0})"}},"then":[
				{"id":"apply","kind":"command","entity_id":%q,"operation":"set","parameters":{"expr":"{value: vars.target > 50}"}},
				{"id":"notify","kind":"notification","destination_id":%q,"title":{"expr":"'Target ' + string(vars.target)"},"message":{"expr":"join(map(steps.forecast.output.hours, {string(.temperature)}), ',')"}}]},
				{"id":"wait","kind":"delay","duration_ms":{"literal":1}}]}`,
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
				response := conditionsManualRun(t.Context(), t, core.address, id, "")
				defer response.Body.Close()
				if response.StatusCode != http.StatusAccepted {
					t.Fatalf("admission: %d %s", response.StatusCode, readSliceBody(t, response))
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
			entry := decodeNotification[struct {
				Run expressionJourneyRun `json:"run"`
			}](t, httpRaw)
			run := entry.Run
			if run.Status != "succeeded" || run.DataEvidence != "recorded" || len(run.Data) != 8 ||
				len(run.Delays) != 1 ||
				len(run.Steps) != 1 ||
				run.Steps[0].Status != "satisfied" {
				t.Fatalf("Run does not prove value flow: %s", httpRaw)
			}
			for i, record := range run.Data {
				if record.Sequence != i {
					t.Fatal("journal order not contiguous")
				}
			}
			var captured, unusable, outputCopied, assigned bool
			for _, record := range run.Data {
				if record.Kind == "state_captured" && record.StepID == "before" {
					captured = string(record.Output) == "false"
				}
				if record.Kind == "state_captured" && record.StepID == "outside" {
					unusable = string(record.Output) == "null" && record.UnusableReason == "pointer_missing"
				}
				if record.Kind == "function_output" {
					outputCopied = strings.Contains(string(record.Output), "9007199254740993")
				}
				if record.Kind == "variables_assigned" {
					assigned = strings.Contains(string(record.Values), `"target":60`)
				}
			}
			if !captured || !unusable || !outputCopied || !assigned {
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
			if mcpRun.DataEvidence != "recorded" || len(mcpRun.Data) != len(run.Data) ||
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
			// A statically valid output reference may be absent at runtime. It must
			// fail before reserving or dispatching the downstream Command.
			missing := fmt.Sprintf(
				`{"name":"Required unreached output","enabled":true,"triggers":[{"id":"clock","kind":"cron","expression":"0 18 * * *"}],"steps":[{"id":"unselected","kind":"if","conditions":{"id":"false","kind":"expression","value":{"literal":false}},"then":[{"id":"forecast","kind":"function","adapter_id":"expression-functions","function_key":"forecast","contract_fingerprint":%q,"arguments":{"literal":{"hours":2}}}]},{"id":"must-not-send","kind":"command","entity_id":%q,"operation":"set","parameters":{"expr":"{value: steps.forecast.output.sequence > 0}"}}]}`,
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
			if failed.Status != "failed" || failed.FailureCode != "expression_missing" || len(failed.Data) != 2 ||
				failed.Steps[0].Status != "not_attempted" {
				t.Fatalf("required missing output silently dispatched: %s", failedRaw)
			}
			select {
			case call := <-calls:
				t.Fatalf("unselected Function executed: %+v", call)
			default:
			}
		})
	}
}
