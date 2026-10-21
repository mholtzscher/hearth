package mcpapi //nolint:testpackage // Tests the registered Command receipt and failure contracts.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestCommandReceiptsKeepTerminalAlternativesAndExactEvidence(t *testing.T) {
	t.Parallel()
	obs := apiObservationID
	nullValue := devices.Value(`null`)
	exactValue := devices.Value(`{"large":9007199254740993,"huge":1e10000,"tiny":1e-10000,"nullable":null}`)
	malformed := devices.Value(`{`)
	for _, test := range []struct {
		name    string
		result  devices.CommandResult
		want    string
		failure bool
	}{
		{"dispatched omits even supplied evidence", devices.CommandResult{CommandID: apiCommandID, Outcome: devices.OutcomeDispatched, ObservationID: &obs, Value: &nullValue}, `{"command_id":"` + string(apiCommandID) + `","status":"dispatched"}`, false},
		{"satisfied null is present", devices.CommandResult{CommandID: apiCommandID, Outcome: devices.OutcomeObserved, ObservationID: &obs, Value: &nullValue}, `{"command_id":"` + string(apiCommandID) + `","status":"satisfied","observation_id":"` + string(apiObservationID) + `","value":null}`, false},
		{"satisfied exact evidence", devices.CommandResult{CommandID: apiCommandID, Outcome: devices.OutcomeObserved, ObservationID: &obs, Value: &exactValue}, `{"command_id":"` + string(apiCommandID) + `","status":"satisfied","observation_id":"` + string(apiObservationID) + `","value":` + string(exactValue) + `}`, false},
		{"missing observation", devices.CommandResult{CommandID: apiCommandID, Outcome: devices.OutcomeObserved, Value: &nullValue}, "", true},
		{"missing value", devices.CommandResult{CommandID: apiCommandID, Outcome: devices.OutcomeObserved, ObservationID: &obs}, "", true},
		{"malformed value", devices.CommandResult{CommandID: apiCommandID, Outcome: devices.OutcomeObserved, ObservationID: &obs, Value: &malformed}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
			Register(
				server,
				&stubDevices{
					executeCommand: func(context.Context, devices.CommandInput) (devices.CommandResult, error) {
						calls++
						return test.result, nil
					},
				},
			)
			result := callWire(
				t,
				server.HTTPHandler(),
				"execute_entity_command",
				`{"entity_id":"`+string(apiEntityID)+`","operation":"set","parameters":{"value":true}}`,
			)
			if calls != 1 {
				t.Fatalf("executions = %d", calls)
			}
			if !test.failure {
				assertWireJSON(t, result, test.want)
				return
			}
			assertInternalWireFailure(t, result)
		})
	}
}

func assertInternalWireFailure(t *testing.T, result wireResult) {
	t.Helper()
	if !result.IsError {
		t.Fatalf("result = %+v", result)
	}
	var failure struct {
		Code    string `json:"failure_code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(result.Structured, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "internal_error" || failure.Message != "internal error" {
		t.Fatalf("failure = %+v", failure)
	}
}
