//go:build e2e

package hearthd //nolint:testpackage // Public journeys use production Core, HTTP/MCP and the Adapter SDK.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
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
		CorrelationID  string          `json:"correlation_id"`
		CommandOutcome string          `json:"command_outcome"`
		AttemptID      string          `json:"attempt_id"`
		Outcome        string          `json:"outcome"`
		Failure        *struct {
			Field string `json:"field"`
			Code  string `json:"failure_code"`
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

type expressionExpectedStep struct {
	StepID            string
	Kind              string
	Phase             string
	Inputs            string
	Output            string
	Values            string
	UnusableReason    string
	BranchOutcome     string
	Outcome           string
	CommandOutcome    string
	FailureField      string
	FailureCode       string
	RequiresCommandID bool
	RequiresAttemptID bool
}

func expressionPositiveTrace() []expressionExpectedStep {
	return []expressionExpectedStep{
		{
			StepID: "forecast",
			Kind:   "function",
			Phase:  "succeeded",
			Inputs: `{"arguments":{"hours":2}}`,
			Output: `{"hours":[{"rain":true,"temperature":10},{"rain":false,"temperature":20}],"sequence":9007199254740993}`,
		},
		{StepID: "before", Kind: "read_state", Phase: "succeeded", Output: `false`},
		{
			StepID: "outside", Kind: "read_state", Phase: "succeeded",
			Output: `null`, UnusableReason: "pointer_missing",
		},
		{
			StepID: "calculate", Kind: "set_variables", Phase: "succeeded",
			Values: `{"target":60,"outside":20}`,
		},
		{StepID: "choose", Kind: "if", Phase: "succeeded", BranchOutcome: "then"},
		{
			StepID: "apply", Kind: "command", Phase: "succeeded",
			Inputs: `{"parameters":{"value":true}}`, CommandOutcome: "satisfied",
			RequiresCommandID: true,
		},
		{
			StepID: "notify", Kind: "notification", Phase: "succeeded",
			Inputs: `{"title":"Target 60","message":"10,20"}`, Outcome: "accepted",
			RequiresAttemptID: true,
		},
		{StepID: "wait", Kind: "delay", Phase: "succeeded"},
	}
}

func expressionFailedTrace() []expressionExpectedStep {
	return []expressionExpectedStep{
		{StepID: "unselected", Kind: "if", Phase: "succeeded", BranchOutcome: "no_match"},
		{
			StepID: "must-not-send", Kind: "command", Phase: "failed",
			FailureField: "parameters", FailureCode: "expression_evaluation",
		},
	}
}

func expressionJSONValue(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode JSON %s: %v", raw, err)
	}
	return value
}

func expressionJSONMatches(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	return reflect.DeepEqual(expressionJSONValue(t, got), expressionJSONValue(t, json.RawMessage(want)))
}

func assertExpressionTrace(
	t *testing.T,
	label string,
	run expressionJourneyRun,
	want []expressionExpectedStep,
) {
	t.Helper()
	if len(run.Executions) != len(want) {
		t.Fatalf("%s executions = %d, want %d: %+v", label, len(run.Executions), len(want), run)
	}
	for index, expected := range want {
		execution := run.Executions[index]
		if execution.Sequence != index || execution.StepID != expected.StepID ||
			execution.Kind != expected.Kind || execution.Phase != expected.Phase {
			t.Fatalf("%s execution %d = %+v, want %+v", label, index, execution, expected)
		}
		assertExpressionEvidence(t, label, execution, expected)
	}
}

func assertExpressionJSONField(
	t *testing.T,
	label, stepID, field string,
	got json.RawMessage,
	want string,
) {
	t.Helper()
	if want != "" && !expressionJSONMatches(t, got, want) {
		t.Fatalf("%s %s %s = %s, want %s", label, stepID, field, got, want)
	}
}

func assertExpressionEvidence(
	t *testing.T,
	label string,
	execution expressionJourneyExecution,
	expected expressionExpectedStep,
) {
	t.Helper()
	assertExpressionJSONField(t, label, expected.StepID, "inputs", execution.Evidence.Inputs, expected.Inputs)
	assertExpressionJSONField(t, label, expected.StepID, "output", execution.Evidence.Output, expected.Output)
	assertExpressionJSONField(t, label, expected.StepID, "values", execution.Evidence.Values, expected.Values)
	if execution.Evidence.UnusableReason != expected.UnusableReason {
		t.Fatalf("%s %s unusable reason = %q, want %q", label, expected.StepID,
			execution.Evidence.UnusableReason, expected.UnusableReason)
	}
	assertExpressionOutcome(t, label, execution, expected)
	assertExpressionLinks(t, label, execution, expected)
	if expected.StepID == "forecast" {
		var output struct {
			Sequence json.Number `json:"sequence"`
		}
		if err := json.Unmarshal(execution.Evidence.Output, &output); err != nil ||
			output.Sequence.String() != "9007199254740993" {
			t.Fatalf("%s precise sequence = %s", label, execution.Evidence.Output)
		}
	}
	if expected.FailureField != "" {
		failure := execution.Evidence.Failure
		if failure == nil || failure.Field != expected.FailureField ||
			failure.Code != expected.FailureCode {
			t.Fatalf("%s %s value failure = %+v", label, expected.StepID, failure)
		}
	}
}

func assertExpressionOutcome(
	t *testing.T,
	label string,
	execution expressionJourneyExecution,
	expected expressionExpectedStep,
) {
	t.Helper()
	if expected.BranchOutcome != "" && execution.Evidence.Outcome != expected.BranchOutcome {
		t.Fatalf("%s %s branch outcome = %q, want %q", label, expected.StepID,
			execution.Evidence.Outcome, expected.BranchOutcome)
	}
	if expected.Outcome != "" && execution.Evidence.Outcome != expected.Outcome {
		t.Fatalf("%s %s outcome = %q, want %q", label, expected.StepID,
			execution.Evidence.Outcome, expected.Outcome)
	}
	if expected.CommandOutcome != "" && execution.Evidence.CommandOutcome != expected.CommandOutcome {
		t.Fatalf("%s %s command outcome = %q, want %q", label, expected.StepID,
			execution.Evidence.CommandOutcome, expected.CommandOutcome)
	}
}

func assertExpressionLinks(
	t *testing.T,
	label string,
	execution expressionJourneyExecution,
	expected expressionExpectedStep,
) {
	t.Helper()
	if expected.RequiresCommandID &&
		(!strings.HasPrefix(execution.Evidence.CommandID, "cmd_") ||
			!strings.HasPrefix(execution.Evidence.CorrelationID, "cor_")) {
		t.Fatalf("%s %s command link = %+v", label, expected.StepID, execution.Evidence)
	}
	if expected.RequiresAttemptID && !strings.HasPrefix(execution.Evidence.AttemptID, "ntf_") {
		t.Fatalf("%s %s attempt = %+v", label, expected.StepID, execution.Evidence)
	}
}

func assertExpressionSucceededRun(t *testing.T, label string, run expressionJourneyRun) {
	t.Helper()
	if run.Status != "succeeded" || run.FailureCode != "" || run.DataEvidence != "recorded" {
		t.Fatalf("%s Run = %+v", label, run)
	}
	if run.Initialization.Kind != "initialized" ||
		!expressionJSONMatches(t, run.Initialization.Values, `{"seed":1,"target":25}`) {
		t.Fatalf("%s initialization = %+v", label, run.Initialization)
	}
	assertExpressionTrace(t, label, run, expressionPositiveTrace())
}

func assertExpressionFailedRun(t *testing.T, label string, run expressionJourneyRun) {
	t.Helper()
	if run.Status != "failed" || run.FailureCode != "expression_evaluation" ||
		run.DataEvidence != "recorded" {
		t.Fatalf("%s Run = %+v", label, run)
	}
	assertExpressionTrace(t, label, run, expressionFailedTrace())
	if run.Executions[1].Evidence.CommandID != "" {
		t.Fatalf("%s unreached Command fabricated a reservation: %+v", label, run.Executions[1])
	}
}

//nolint:gocognit // The journey checks one complete value flow and its required-output negative control through both transports.
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
					SQLitePath:        dbtest.MigratedPath(t),
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
			assertExpressionSucceededRun(t, "HTTP", run)
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
			assertExpressionSucceededRun(t, "MCP", mcpRun)
			if !reflect.DeepEqual(run, mcpRun) {
				t.Fatalf("HTTP/MCP positive evidence differs: %+v / %+v", run, mcpRun)
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
			assertExpressionFailedRun(t, "HTTP negative control", failed)
			failedMCPRaw := automationFunctionsMCP(
				t,
				mcpClient,
				"get_automation_history_entry",
				fmt.Sprintf(`{"automation_id":%q,"entry_id":%q}`, missingID, failedID),
			)
			failedMCP := decodeNotification[expressionJourneyRun](t, failedMCPRaw)
			assertExpressionFailedRun(t, "MCP negative control", failedMCP)
			if !reflect.DeepEqual(failed, failedMCP) {
				t.Fatalf("HTTP/MCP negative evidence differs: %+v / %+v", failed, failedMCP)
			}
			select {
			case call := <-calls:
				t.Fatalf("unselected Function executed: %+v", call)
			default:
			}
			if closeErr := session.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			source.stop()
		})
	}
}
