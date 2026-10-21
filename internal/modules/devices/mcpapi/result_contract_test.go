package mcpapi //nolint:testpackage // Exercises the module's registered tools with recording domain doubles.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type wireResult struct {
	IsError    bool            `json:"isError"`
	Structured json.RawMessage `json:"structuredContent"`
	Content    []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func callWire(t *testing.T, handler http.Handler, name, arguments string) wireResult {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+arguments+`}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	payload := response.Body.String()
	for line := range strings.SplitSeq(payload, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			payload = data
			break
		}
	}
	var envelope struct {
		Result wireResult      `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Error) != 0 {
		t.Fatalf("RPC error: %s", envelope.Error)
	}
	return envelope.Result
}

func assertWireJSON(t *testing.T, result wireResult, want string) {
	t.Helper()
	if result.IsError {
		t.Fatalf("tool error: %+v", result)
	}
	if len(result.Content) != 1 || result.Content[0].Text != string(result.Structured) {
		t.Fatalf("structured/text differ: %+v", result)
	}
	decode := func(raw []byte) any {
		t.Helper()
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if !reflect.DeepEqual(decode(result.Structured), decode([]byte(want))) {
		t.Fatalf("result = %s\nwant = %s", result.Structured, want)
	}
}

// The literals below define the new protocol, independently of HTTP models or
// production mappers. Complete equality also pins closed projections/omissions.
//
//nolint:paralleltest,tparallel // One recording server pins sequential calls and exactly one call per operation.
func TestRegisteredToolsReturnIndependentProjections(t *testing.T) {
	t.Parallel()
	const timestamp = "2026-10-21T12:00:00Z"
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	const opaque = `{"large":9007199254740993,"tiny":1e-10000,"nullable":null}`
	view := apiEntityWithState(&devices.State{ObservationID: apiObservationID,
		Value: devices.Value(opaque), ObservedAt: now, AdapterReceivedAt: now, SourceUpdatedAt: &now})
	view.Entity.AdapterName = "private adapter name"
	view.Availability = devices.EntityAvailability{Status: devices.EntityAvailabilityUnavailable,
		Source: "adapter_health", Since: now, EvidenceAt: now, SourceObservedAt: &now,
		Reason: &devices.HealthReason{Code: "hearth.external_system_unavailable"}}
	command := listableCommand()
	command.Parameters = devices.CommandParameters(opaque)
	command.RequestedAt = now
	command.DeadlineAt = now
	command.AcceptedAt = &now
	command.CompletedAt = &now
	obs := apiObservationID
	command.OutcomeObservationID = &obs
	device := devices.Device{
		ID:          apiDeviceID,
		Name:        "Effective name",
		AdapterName: "private",
		Kind:        devices.DeviceKindLight,
	}
	calls := map[string]int{}
	stub := &stubDevices{}
	projectionEntityFixture(t, stub, calls, view, opaque)
	projectionReadFixture(t, stub, calls, view, device, command)
	projectionHistoryFixture(t, stub, calls, now)

	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	Register(server, stub)
	replace := strings.NewReplacer(
		"$E",
		string(apiEntityID),
		"$D",
		string(apiDeviceID),
		"$C",
		string(apiCommandID),
		"$O",
		string(apiObservationID),
		"$V",
		string(apiEntityEventID),
		"$T",
		timestamp,
	)
	const entitySummary = `{"entity_id":"$E","device_id":"$D","name":"Power","type":"hearth.power/v1","enabled":true,"availability":{"status":"unavailable","reason_code":"hearth.external_system_unavailable"},"state":{"value":` + opaque + `,"observed_at":"$T"}}`
	const commandSummary = `{"command_id":"$C","entity_id":"$E","operation":"set","status":"satisfied","requested_at":"$T","completed_at":"$T"}`
	for _, test := range []struct{ tool, arguments, want string }{
		{"list_entities", `{}`, `{"items":[` + entitySummary + `]}`},
		{"get_entity", `{"entity_id":"$E"}`, `{"entity_id":"$E","device_id":"$D","name":"Power","type":"hearth.power/v1","enabled":true,"adapter_id":"simulator","support":{"state":{},"operations":{"set":{}}},"availability":{"status":"unavailable","reason_code":"hearth.external_system_unavailable","source":"adapter_health","since":"$T","evidence_at":"$T","source_observed_at":"$T"},"state":{"value":` + opaque + `,"observed_at":"$T","observation_id":"$O","adapter_received_at":"$T","source_updated_at":"$T"}}`},
		{"update_entity", `{"entity_id":"$E","enabled":false}`, `{"entity_id":"$E","enabled":true}`},
		{"execute_entity_command", `{"entity_id":"$E","operation":"set","parameters":` + opaque + `}`, `{"command_id":"$C","status":"satisfied","observation_id":"$O","value":null}`},
		{"list_entity_commands", `{"entity_id":"$E"}`, `{"items":[` + commandSummary + `]}`},
		{"list_devices", `{}`, `{"items":[{"device_id":"$D","name":"Effective name","kind":"light"}]}`},
		{"get_device", `{"device_id":"$D"}`, `{"device_id":"$D","name":"Effective name","kind":"light","entities":[` + entitySummary + `]}`},
		{"get_command", `{"command_id":"$C"}`, `{"command_id":"$C","entity_id":"$E","operation":"set","status":"satisfied","requested_at":"$T","completed_at":"$T","parameters":` + opaque + `,"deadline_at":"$T","accepted_at":"$T","outcome_observation_id":"$O"}`},
		{"list_commands", `{}`, `{"items":[` + commandSummary + `]}`},
		{"list_entity_availability_history", `{"entity_id":"$E"}`, `{"items":[{"status":"unavailable","source":"adapter_health","observed_at":"$T","source_observed_at":"$T","reason_code":"hearth.external_system_unavailable"}]}`},
		{"list_entity_state_history", `{"entity_id":"$E"}`, `{"items":[{"observation_id":"$O","disposition":"applied","observed_at":"$T","adapter_received_at":"$T","value":null}]}`},
		{"list_entity_events", `{"entity_id":"$E"}`, `{"items":[{"event_id":"$V","entity_id":"$E","name":"single_press","disposition":"accepted","emitted_at":"$T","received_at":"$T","recorded_at":"$T"}]}`},
	} {
		t.Run(test.tool, func(t *testing.T) {
			assertWireJSON(
				t,
				callWire(t, server.HTTPHandler(), test.tool, replace.Replace(test.arguments)),
				replace.Replace(test.want),
			)
		})
	}
	for tool, count := range calls {
		if count != 1 {
			t.Fatalf("%s called %d times", tool, count)
		}
	}
	if len(calls) != 12 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestNullArgumentsNeverReachService(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	Register(server, &stubDevices{})
	for _, test := range []struct{ tool, arguments string }{
		{"get_entity", `{"entity_id":null}`},
		{"get_device", `{"device_id":null}`},
		{"get_command", `{"command_id":null}`},
		{"list_entities", `{"device_id":null}`},
		{"list_commands", `{"entity_id":null}`},
		{"list_entities", `{"limit":null}`},
		{"list_entities", `{"cursor":null}`},
		{"update_entity", `{"entity_id":"` + string(apiEntityID) + `","enabled":null}`},
		{"execute_entity_command", `{"entity_id":"` + string(apiEntityID) + `","operation":"set","parameters":null}`},
	} {
		t.Run(test.tool+test.arguments, func(t *testing.T) {
			t.Parallel()
			result := callWire(t, server.HTTPHandler(), test.tool, test.arguments)
			if !result.IsError || len(result.Structured) != 0 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}
