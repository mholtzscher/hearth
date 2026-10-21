package mcpapi //nolint:testpackage // Tests registered tool cursors with domain recording doubles.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func continuationPage[T any](calls map[string]int, tool string, item T) devices.Page[T] {
	calls[tool]++
	if calls[tool] == 1 {
		return devices.Page[T]{Items: []T{item}, HasMore: true}
	}
	return devices.Page[T]{}
}

//nolint:paralleltest,tparallel // Cursor follow-ups share one recording server and run in request order.
func TestEveryCollectionContinuesItsOwnDomainPosition(t *testing.T) {
	t.Parallel()
	apiDeviceIDValue := apiDeviceID
	apiEntityIDValue := apiEntityID
	satisfiedStatusValue := devices.CommandStatusSatisfied
	positions := map[string]any{}
	calls := map[string]int{}
	stub := &stubDevices{}
	paginationFixture(stub, positions, calls)
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	Register(server, stub)
	entityArgs := `{"entity_id":"` + string(apiEntityID) + `","limit":1}`
	command := listableCommand()
	beforeTime := command.RequestedAt
	beforeID := command.ID
	receiveOrder := int64(3)
	for _, test := range []struct {
		tool, arguments, cursorField, terminal string
		want                                   any
	}{
		{"list_entities", `{"device_id":"` + string(apiDeviceID) + `","limit":1}`, "next_cursor", `{"items":[]}`, devices.ListEntitiesParams{DeviceID: &apiDeviceIDValue, AfterID: &apiEntityIDValue, Limit: 1}},
		{"list_devices", `{"limit":1}`, "next_cursor", `{"items":[]}`, devices.ListDevicesParams{AfterID: &apiDeviceIDValue, Limit: 1}},
		{"get_device", `{"device_id":"` + string(apiDeviceID) + `","entity_limit":1}`, "next_entity_cursor", `{"device_id":"` + string(apiDeviceID) + `","name":"Office","kind":"light","entities":[]}`, devices.GetDeviceParams{ID: apiDeviceID, AfterEntityID: &apiEntityIDValue, EntityLimit: 1}},
		{"list_commands", `{"entity_id":"` + string(apiEntityID) + `","status":"satisfied","limit":1}`, "next_cursor", `{"items":[]}`, devices.ListCommandsParams{EntityID: &apiEntityIDValue, Status: &satisfiedStatusValue, BeforeRequestedAt: &beforeTime, BeforeID: &beforeID, Limit: 1}},
		{"list_entity_commands", entityArgs, "next_cursor", `{"items":[]}`, devices.ListEntityCommandsParams{EntityID: apiEntityID, BeforeRequestedAt: &beforeTime, BeforeID: &beforeID, Limit: 1}},
		{"list_entity_state_history", entityArgs, "next_cursor", `{"items":[]}`, devices.ListEntityStateHistoryParams{EntityID: apiEntityID, Filter: devices.EntityStateHistoryFilterUpdates, BeforeReceiveOrder: &receiveOrder, Limit: 1}},
		{"list_entity_events", entityArgs, "next_cursor", `{"items":[]}`, devices.ListEntityEventsParams{EntityID: apiEntityID, BeforeReceiveOrder: &receiveOrder, Limit: 1}},
		{"list_entity_availability_history", entityArgs, "next_cursor", `{"items":[]}`, devices.ListEntityAvailabilityParams{EntityID: apiEntityID, BeforeReceiveOrder: &receiveOrder, Limit: 1}},
	} {
		t.Run(test.tool, func(t *testing.T) {
			continueWirePage(t, server.HTTPHandler(), test.tool, test.arguments, test.cursorField, test.terminal)
			if !reflect.DeepEqual(positions[test.tool], test.want) {
				t.Fatalf("position = %#v, want %#v", positions[test.tool], test.want)
			}
			if calls[test.tool] != 2 {
				t.Fatalf("calls = %d", calls[test.tool])
			}
		})
	}
}

func TestWrongCursorScopeNeverReachesService(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	Register(server, &stubDevices{})
	wrongDevice := devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	wrongEntity := devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ac")
	entityCursor, err := apicursor.EncodeEntitiesCursor(apiEntityID, &wrongDevice)
	if err != nil {
		t.Fatal(err)
	}
	deviceEntityCursor, err := apicursor.EncodeDeviceEntitiesCursor(apiEntityID, wrongDevice)
	if err != nil {
		t.Fatal(err)
	}
	commandCursor, err := apicursor.EncodeCommandCursor(listableCommand())
	if err != nil {
		t.Fatal(err)
	}
	commandListCursor, err := apicursor.EncodeCommandListCursor(listableCommand(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stateCursor, err := apicursor.EncodeEntityStateHistoryCursor(apiEntityID, devices.EntityStateHistoryFilterAll, 3)
	if err != nil {
		t.Fatal(err)
	}
	eventCursor, err := apicursor.EncodeEntityEventCursor(wrongEntity, 3)
	if err != nil {
		t.Fatal(err)
	}
	availabilityCursor, err := apicursor.EncodeEntityAvailabilityCursor(wrongEntity, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tool, key, cursor string
		arguments         map[string]any
	}{
		{"list_entities", "cursor", entityCursor, map[string]any{"device_id": string(apiDeviceID)}},
		{"list_devices", "cursor", entityCursor, map[string]any{}},
		{"get_device", "entity_cursor", deviceEntityCursor, map[string]any{"device_id": string(apiDeviceID)}},
		{"list_commands", "cursor", commandListCursor, map[string]any{"entity_id": string(apiEntityID)}},
		{"list_entity_commands", "cursor", commandCursor, map[string]any{"entity_id": string(wrongEntity)}},
		{"list_entity_state_history", "cursor", stateCursor, map[string]any{"entity_id": string(apiEntityID)}},
		{"list_entity_events", "cursor", eventCursor, map[string]any{"entity_id": string(apiEntityID)}},
		{"list_entity_availability_history", "cursor", availabilityCursor, map[string]any{"entity_id": string(apiEntityID)}},
	} {
		t.Run(test.tool, func(t *testing.T) {
			t.Parallel()
			test.arguments[test.key] = test.cursor
			raw, marshalErr := json.Marshal(test.arguments)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			result := callWire(t, server.HTTPHandler(), test.tool, string(raw))
			if !result.IsError {
				t.Fatalf("result = %+v", result)
			}
			var failure struct {
				Code string `json:"failure_code"`
			}
			if decodeErr := json.Unmarshal(result.Structured, &failure); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if failure.Code != "invalid_request" {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}

func paginationFixture(stub *stubDevices, positions map[string]any, calls map[string]int) {
	stub.listEntities = func(_ context.Context, params devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
		positions["list_entities"] = params
		return continuationPage(calls, "list_entities", apiEntityWithState(nil)), nil
	}
	stub.listDevices = func(_ context.Context, params devices.ListDevicesParams) (devices.Page[devices.Device], error) {
		positions["list_devices"] = params
		return continuationPage(
			calls,
			"list_devices",
			devices.Device{ID: apiDeviceID, Name: "Office", Kind: devices.DeviceKindLight},
		), nil
	}
	stub.getDevice = func(_ context.Context, params devices.GetDeviceParams) (devices.DeviceAggregate, error) {
		positions["get_device"] = params
		return devices.DeviceAggregate{
			Device:   devices.Device{ID: apiDeviceID, Name: "Office", Kind: devices.DeviceKindLight},
			Entities: continuationPage(calls, "get_device", apiEntityWithState(nil)),
		}, nil
	}
	stub.listCommands = func(_ context.Context, params devices.ListCommandsParams) (devices.Page[devices.CommandRecord], error) {
		positions["list_commands"] = params
		return continuationPage(calls, "list_commands", listableCommand()), nil
	}
	stub.listEntityCommands = func(_ context.Context, params devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error) {
		positions["list_entity_commands"] = params
		return continuationPage(calls, "list_entity_commands", listableCommand()), nil
	}
	stub.listEntityStateHistory = func(_ context.Context, params devices.ListEntityStateHistoryParams) (devices.Page[devices.EntityStateHistoryEntry], error) {
		positions["list_entity_state_history"] = params
		return continuationPage(calls, "list_entity_state_history", apiStateHistoryEntry(3)), nil
	}
	stub.listEntityEvents = func(_ context.Context, params devices.ListEntityEventsParams) (devices.Page[devices.EntityEventHistoryEntry], error) {
		positions["list_entity_events"] = params
		return continuationPage(calls, "list_entity_events", apiEntityEventEntry(3)), nil
	}
	stub.listEntityAvailabilityHistory = func(_ context.Context, params devices.ListEntityAvailabilityParams) (devices.Page[devices.HealthTransition], error) {
		positions["list_entity_availability_history"] = params
		return continuationPage(
			calls,
			"list_entity_availability_history",
			devices.HealthTransition{ReceiveOrder: 3, Status: "available", Source: "entity_report"},
		), nil
	}
}

func continueWirePage(t *testing.T, handler http.Handler, tool, input, cursorField, terminal string) {
	t.Helper()
	first := callWire(t, handler, tool, input)
	if first.IsError {
		t.Fatalf("first page = %+v", first)
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(first.Structured, &page); err != nil {
		t.Fatal(err)
	}
	var cursor string
	if err := json.Unmarshal(page[cursorField], &cursor); err != nil || cursor == "" {
		t.Fatalf("cursor = %q: %v", cursor, err)
	}
	var arguments map[string]any
	if err := json.Unmarshal([]byte(input), &arguments); err != nil {
		t.Fatal(err)
	}
	key := "cursor"
	if tool == "get_device" {
		key = "entity_cursor"
	}
	arguments[key] = cursor
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	assertWireJSON(t, callWire(t, handler, tool, string(raw)), terminal)
}
