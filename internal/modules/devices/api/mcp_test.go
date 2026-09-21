package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func mcpSession(t *testing.T, service Devices) *mcp.ClientSession {
	t.Helper()
	return mcpSessionWithServer(t, mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"}), service)
}

func mcpSessionWithLogs(t *testing.T, service Devices, logs *bytes.Buffer) *mcp.ClientSession {
	t.Helper()
	server := mcpapi.New(mcpapi.Config{
		Name: "hearth", Version: "1.0.0", Logger: slog.New(slog.NewJSONHandler(logs, nil)),
	})
	return mcpSessionWithServer(t, server, service)
}

func mcpSessionWithServer(t *testing.T, server *mcpapi.Server, service Devices) *mcp.ClientSession {
	t.Helper()
	RegisterMCP(server, service)
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "hearth-devices-api-test", Version: "1.0.0"}, nil,
	).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("close MCP session: %v", closeErr)
		}
	})
	return session
}

func mcpLogRecord(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	line, _, _ := bytes.Cut(bytes.TrimSpace(raw), []byte("\n"))
	if len(line) == 0 {
		t.Fatal("no log record was written")
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("decode log record %s: %v", line, err)
	}
	return record
}

func mcpCallTool(
	t *testing.T,
	session *mcp.ClientSession,
	name string,
	arguments map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result == nil {
		t.Fatalf("call %s returned no result", name)
	}
	return result
}

func mcpBody(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("result = %#v, want a successful tool result", result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if unmarshalErr := json.Unmarshal(raw, target); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
}

func mcpErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("result = %#v, want an isError tool result", result)
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("error result has no text content: %#v", result.Content)
	return ""
}

// TestRegisterMCPRegistersEveryDeviceTool proves the catalog exposes exactly the
// 15 Devices tools by name, each with a description and typed schemas.
func TestRegisterMCPRegistersEveryDeviceTool(t *testing.T) {
	t.Parallel()
	session := mcpSession(t, &stubDevices{})
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	want := []string{
		"list_entities",
		"get_entity",
		"update_entity",
		"execute_entity_command",
		"list_entity_commands",
		"list_devices",
		"get_device",
		"get_command",
		"list_commands",
		"list_adapters",
		"get_adapter",
		"list_adapter_health_history",
		"list_entity_availability_history",
		"list_entity_state_history",
		"list_entity_events",
	}
	if len(listed.Tools) != len(want) {
		t.Fatalf("tools = %d, want %d", len(listed.Tools), len(want))
	}
	registered := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		registered[tool.Name] = tool
	}
	for _, name := range want {
		tool, present := registered[name]
		if !present {
			t.Fatalf("tool %q is missing", name)
		}
		if tool.Description == "" || tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Fatalf("tool %q = %#v", name, tool)
		}
	}
}

// TestMCPEntityToolsTranslateToServiceCalls proves the Entity tools translate
// flat MCP arguments into the shared Huma requests and return their bodies.
func TestMCPEntityToolsTranslateToServiceCalls(t *testing.T) {
	t.Parallel()
	var listParams devices.ListEntitiesParams
	var requestedEntityID devices.EntityID
	var enabled bool
	var commandInput devices.CommandInput
	stub := &stubDevices{
		listEntities: func(
			_ context.Context,
			params devices.ListEntitiesParams,
		) (devices.Page[devices.EntityWithState], error) {
			listParams = params
			return devices.Page[devices.EntityWithState]{
				Items: []devices.EntityWithState{apiEntityWithState(nil)},
			}, nil
		},
		getEntity: func(_ context.Context, entityID devices.EntityID) (devices.EntityWithState, error) {
			requestedEntityID = entityID
			return apiEntityWithState(nil), nil
		},
		setEntityEnabled: func(
			_ context.Context,
			entityID devices.EntityID,
			next bool,
		) (devices.EntityWithState, error) {
			requestedEntityID = entityID
			enabled = next
			return apiEntityWithState(nil), nil
		},
		executeCommand: func(_ context.Context, input devices.CommandInput) (devices.CommandResult, error) {
			commandInput = input
			observationID := apiObservationID
			value := devices.Value(`true`)
			return devices.CommandResult{
				CommandID: apiCommandID, Outcome: devices.OutcomeObserved,
				ObservationID: &observationID, Value: &value,
			}, nil
		},
	}
	session := mcpSession(t, stub)

	listed := mcpCallTool(t, session, "list_entities", map[string]any{
		"device_id": string(apiDeviceID), "limit": 5,
	})
	var collection EntityCollectionBody
	mcpBody(t, listed, &collection)
	if len(collection.Items) != 1 || collection.Items[0].ID != string(apiEntityID) ||
		collection.Items[0].State != nil {
		t.Fatalf("list_entities body = %#v", collection)
	}
	if listParams.Limit != 5 || listParams.DeviceID == nil || *listParams.DeviceID != apiDeviceID {
		t.Fatalf("ListEntities params = %#v", listParams)
	}

	single := mcpCallTool(t, session, "get_entity", map[string]any{"entity_id": string(apiEntityID)})
	var entity EntityBody
	mcpBody(t, single, &entity)
	if entity.ID != string(apiEntityID) || entity.Name != "Power" || !entity.Enabled {
		t.Fatalf("get_entity body = %#v", entity)
	}
	if requestedEntityID != apiEntityID {
		t.Fatalf("GetEntity ID = %q", requestedEntityID)
	}

	patched := mcpCallTool(t, session, "update_entity", map[string]any{
		"entity_id": string(apiEntityID), "enabled": false,
	})
	var updated EntityBody
	mcpBody(t, patched, &updated)
	if updated.ID != string(apiEntityID) || enabled || requestedEntityID != apiEntityID {
		t.Fatalf("update_entity body = %#v, enabled = %v", updated, enabled)
	}

	executed := mcpCallTool(t, session, "execute_entity_command", map[string]any{
		"entity_id": string(apiEntityID), "operation": "set", "parameters": map[string]any{"value": true},
	})
	var result CommandResultBody
	mcpBody(t, executed, &result)
	if result.CommandID != string(apiCommandID) || result.Status != "satisfied" ||
		result.ObservationID == nil || *result.ObservationID != string(apiObservationID) ||
		result.Value == nil || *result.Value != true {
		t.Fatalf("execute_entity_command body = %#v", result)
	}
	if commandInput.EntityID != apiEntityID || commandInput.OperationName != devices.OperationName("set") ||
		string(commandInput.Parameters) != `{"value":true}` {
		t.Fatalf("ExecuteCommand input = %#v", commandInput)
	}
	if commandInput.ID != "" || commandInput.CorrelationID != "" {
		t.Fatalf("MCP supplied internal command identities: %#v", commandInput)
	}
}

// TestMCPEntityHistoryToolsTranslateToServiceCalls proves the Entity history
// tools forward their filters, limits, and pages to the shared reads.
func TestMCPEntityHistoryToolsTranslateToServiceCalls(t *testing.T) {
	t.Parallel()
	var commandsParams devices.ListEntityCommandsParams
	var stateParams []devices.ListEntityStateHistoryParams
	var eventsParams devices.ListEntityEventsParams
	var availabilityParams devices.ListEntityAvailabilityParams
	stub := &stubDevices{
		listEntityCommands: func(
			_ context.Context,
			params devices.ListEntityCommandsParams,
		) (devices.Page[devices.CommandRecord], error) {
			commandsParams = params
			return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
		},
		listEntityStateHistory: func(
			_ context.Context,
			params devices.ListEntityStateHistoryParams,
		) (devices.Page[devices.EntityStateHistoryEntry], error) {
			stateParams = append(stateParams, params)
			return devices.Page[devices.EntityStateHistoryEntry]{
				Items: []devices.EntityStateHistoryEntry{apiStateHistoryEntry(3)},
			}, nil
		},
		listEntityEvents: func(
			_ context.Context,
			params devices.ListEntityEventsParams,
		) (devices.Page[devices.EntityEventHistoryEntry], error) {
			eventsParams = params
			return devices.Page[devices.EntityEventHistoryEntry]{
				Items: []devices.EntityEventHistoryEntry{apiEntityEventEntry(3)},
			}, nil
		},
		listEntityAvailabilityHistory: func(
			_ context.Context,
			params devices.ListEntityAvailabilityParams,
		) (devices.Page[devices.HealthTransition], error) {
			availabilityParams = params
			return devices.Page[devices.HealthTransition]{Items: []devices.HealthTransition{{
				Status: "available", Source: "entity_report",
				ObservedAt: time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC), ReceiveOrder: 3,
			}}}, nil
		},
	}
	session := mcpSession(t, stub)

	entityArguments := map[string]any{"entity_id": string(apiEntityID)}
	commandPage := mcpCallTool(t, session, "list_entity_commands", entityArguments)
	var commands CommandCollectionBody
	mcpBody(t, commandPage, &commands)
	if commandsParams.EntityID != apiEntityID || commandsParams.Limit != 50 ||
		commandsParams.BeforeID != nil || len(commands.Items) != 1 {
		t.Fatalf("ListEntityCommands params = %#v, body = %#v", commandsParams, commands)
	}

	statePage := mcpCallTool(t, session, "list_entity_state_history", entityArguments)
	var history EntityStateHistoryCollectionBody
	mcpBody(t, statePage, &history)
	filteredPage := mcpCallTool(t, session, "list_entity_state_history", map[string]any{
		"entity_id": string(apiEntityID), "disposition": "all",
	})
	var filtered EntityStateHistoryCollectionBody
	mcpBody(t, filteredPage, &filtered)
	if len(stateParams) != 2 || stateParams[0].Filter != devices.EntityStateHistoryFilterUpdates ||
		stateParams[1].Filter != devices.EntityStateHistoryFilterAll {
		t.Fatalf("ListEntityStateHistory params = %#v", stateParams)
	}
	if len(history.Items) != 1 || history.Items[0].Disposition != "applied" {
		t.Fatalf("State history body = %#v", history)
	}

	eventPage := mcpCallTool(t, session, "list_entity_events", entityArguments)
	var events EntityEventCollectionBody
	mcpBody(t, eventPage, &events)
	if eventsParams.EntityID != apiEntityID || eventsParams.BeforeReceiveOrder != nil ||
		len(events.Items) != 1 || events.Items[0].Name != "single_press" {
		t.Fatalf("ListEntityEvents params = %#v, body = %#v", eventsParams, events)
	}

	availabilityPage := mcpCallTool(t, session, "list_entity_availability_history", entityArguments)
	var availability HealthTransitionCollectionBody
	mcpBody(t, availabilityPage, &availability)
	if availabilityParams.EntityID != apiEntityID || availabilityParams.Limit != 50 ||
		len(availability.Items) != 1 || availability.Items[0].Status != "available" {
		t.Fatalf("ListEntityAvailabilityHistory params = %#v, body = %#v", availabilityParams, availability)
	}
}

// TestMCPDeviceAndCommandToolsTranslateToServiceCalls proves the Device and
// household Command tools translate their filters and embedded page arguments.
func TestMCPDeviceAndCommandToolsTranslateToServiceCalls(t *testing.T) {
	t.Parallel()
	var deviceParams devices.ListDevicesParams
	var getDeviceParams devices.GetDeviceParams
	var commandID devices.CommandID
	var commandsParams devices.ListCommandsParams
	stub := &stubDevices{
		listDevices: func(
			_ context.Context,
			params devices.ListDevicesParams,
		) (devices.Page[devices.Device], error) {
			deviceParams = params
			return devices.Page[devices.Device]{Items: []devices.Device{{
				ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: "Office",
			}}}, nil
		},
		getDevice: func(_ context.Context, params devices.GetDeviceParams) (devices.DeviceAggregate, error) {
			getDeviceParams = params
			return devices.DeviceAggregate{
				Device: devices.Device{ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: "Office"},
				Entities: devices.Page[devices.EntityWithState]{
					Items: []devices.EntityWithState{apiEntityWithState(nil)},
				},
			}, nil
		},
		getCommand: func(_ context.Context, id devices.CommandID) (devices.CommandRecord, error) {
			commandID = id
			return listableCommand(), nil
		},
		listCommands: func(
			_ context.Context,
			params devices.ListCommandsParams,
		) (devices.Page[devices.CommandRecord], error) {
			commandsParams = params
			return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
		},
	}
	session := mcpSession(t, stub)

	devicesPage := mcpCallTool(t, session, "list_devices", map[string]any{"limit": 10})
	var collection DeviceCollectionBody
	mcpBody(t, devicesPage, &collection)
	if deviceParams.Limit != 10 || deviceParams.AfterID != nil || len(collection.Items) != 1 ||
		collection.Items[0].Name != "Office" {
		t.Fatalf("ListDevices params = %#v, body = %#v", deviceParams, collection)
	}

	deviceDetail := mcpCallTool(t, session, "get_device", map[string]any{
		"device_id": string(apiDeviceID), "entity_limit": 3,
	})
	var detail DeviceDetailBody
	mcpBody(t, deviceDetail, &detail)
	if getDeviceParams.ID != apiDeviceID || getDeviceParams.EntityLimit != 3 ||
		getDeviceParams.AfterEntityID != nil || detail.ID != string(apiDeviceID) ||
		len(detail.Entities) != 1 {
		t.Fatalf("GetDevice params = %#v, body = %#v", getDeviceParams, detail)
	}

	entityCursor, err := encodeDeviceEntitiesCursor(apiEntityID, apiDeviceID)
	if err != nil {
		t.Fatalf("encode embedded Entity cursor: %v", err)
	}
	mcpCallTool(t, session, "get_device", map[string]any{
		"device_id": string(apiDeviceID), "entity_cursor": entityCursor,
	})
	if getDeviceParams.AfterEntityID == nil || *getDeviceParams.AfterEntityID != apiEntityID {
		t.Fatalf("GetDevice embedded Entity cursor = %#v", getDeviceParams)
	}

	commandRecord := mcpCallTool(t, session, "get_command", map[string]any{
		"command_id": string(apiCommandID),
	})
	var record CommandRecordBody
	mcpBody(t, commandRecord, &record)
	if commandID != apiCommandID || record.ID != string(apiCommandID) || record.Status != "satisfied" {
		t.Fatalf("GetCommand ID = %q, body = %#v", commandID, record)
	}

	commandHistory := mcpCallTool(t, session, "list_commands", map[string]any{
		"entity_id": string(apiEntityID), "status": "satisfied", "limit": 7,
	})
	var history CommandCollectionBody
	mcpBody(t, commandHistory, &history)
	if commandsParams.Limit != 7 || commandsParams.EntityID == nil || *commandsParams.EntityID != apiEntityID ||
		commandsParams.Status == nil || *commandsParams.Status != devices.CommandStatusSatisfied ||
		len(history.Items) != 1 {
		t.Fatalf("ListCommands params = %#v, body = %#v", commandsParams, history)
	}

	unfiltered := mcpCallTool(t, session, "list_commands", map[string]any{})
	var unfilteredHistory CommandCollectionBody
	mcpBody(t, unfiltered, &unfilteredHistory)
	if commandsParams.EntityID != nil || commandsParams.Status != nil ||
		commandsParams.Limit != 50 {
		t.Fatalf("unfiltered ListCommands params = %#v", commandsParams)
	}
}

// TestMCPAdapterToolsTranslateToServiceCalls proves the Adapter tools forward
// their IDs and pages to the shared reads.
func TestMCPAdapterToolsTranslateToServiceCalls(t *testing.T) {
	t.Parallel()
	var adaptersParams devices.ListAdaptersParams
	var adapterID string
	var healthParams devices.ListAdapterHealthParams
	stub := &stubDevices{
		listAdapters: func(
			_ context.Context,
			params devices.ListAdaptersParams,
		) (devices.Page[devices.AdapterInstance], error) {
			adaptersParams = params
			return devices.Page[devices.AdapterInstance]{Items: []devices.AdapterInstance{{
				ID: apiAdapterID, Health: devices.AdapterHealth{Status: "healthy", Source: "adapter"},
			}}}, nil
		},
		getAdapter: func(_ context.Context, requested string) (devices.AdapterInstance, error) {
			adapterID = requested
			return devices.AdapterInstance{
				ID: apiAdapterID, Health: devices.AdapterHealth{Status: "healthy", Source: "adapter"},
			}, nil
		},
		listAdapterHealthHistory: func(
			_ context.Context,
			params devices.ListAdapterHealthParams,
		) (devices.Page[devices.HealthTransition], error) {
			healthParams = params
			return devices.Page[devices.HealthTransition]{Items: []devices.HealthTransition{{
				Status: "healthy", Source: "adapter",
				ObservedAt: time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC), ReceiveOrder: 3,
			}}}, nil
		},
	}
	session := mcpSession(t, stub)

	adapterPage := mcpCallTool(t, session, "list_adapters", map[string]any{"limit": 2})
	var adapters AdapterCollectionBody
	mcpBody(t, adapterPage, &adapters)
	if adaptersParams.Limit != 2 || len(adapters.Items) != 1 || adapters.Items[0].ID != apiAdapterID {
		t.Fatalf("ListAdapters params = %#v, body = %#v", adaptersParams, adapters)
	}

	adapter := mcpCallTool(t, session, "get_adapter", map[string]any{"adapter_id": apiAdapterID})
	var adapterBody AdapterBody
	mcpBody(t, adapter, &adapterBody)
	if adapterID != apiAdapterID || adapterBody.ID != apiAdapterID || adapterBody.Health.Status != "healthy" {
		t.Fatalf("GetAdapter ID = %q, body = %#v", adapterID, adapterBody)
	}

	healthPage := mcpCallTool(t, session, "list_adapter_health_history", map[string]any{
		"adapter_id": apiAdapterID, "limit": 4,
	})
	var health HealthTransitionCollectionBody
	mcpBody(t, healthPage, &health)
	if healthParams.AdapterID != apiAdapterID || healthParams.Limit != 4 ||
		healthParams.BeforeReceiveOrder != nil || len(health.Items) != 1 {
		t.Fatalf("ListAdapterHealthHistory params = %#v, body = %#v", healthParams, health)
	}
}

// TestMCPForwardsOpaqueCursorsAndAppliesTheSharedPageDefault proves tools return
// an opaque next_cursor, accept it uninterpreted on the next call, and apply the
// 50-row default when a client omits the limit.
func TestMCPForwardsOpaqueCursorsAndAppliesTheSharedPageDefault(t *testing.T) {
	t.Parallel()
	var requested []devices.ListEntitiesParams
	stub := &stubDevices{listEntities: func(
		_ context.Context,
		params devices.ListEntitiesParams,
	) (devices.Page[devices.EntityWithState], error) {
		requested = append(requested, params)
		if len(requested) == 1 {
			if params.AfterID != nil {
				t.Fatalf("first page AfterID = %v", params.AfterID)
			}
			return devices.Page[devices.EntityWithState]{
				Items:   []devices.EntityWithState{apiEntityWithState(nil)},
				HasMore: true,
			}, nil
		}
		return devices.Page[devices.EntityWithState]{}, nil
	}}
	session := mcpSession(t, stub)

	first := mcpCallTool(t, session, "list_entities", map[string]any{})
	var page EntityCollectionBody
	mcpBody(t, first, &page)
	if page.NextCursor == nil || *page.NextCursor == "" {
		t.Fatalf("first page body = %#v, want a next_cursor", page)
	}
	if requested[0].Limit != 50 {
		t.Fatalf("default limit = %d, want 50", requested[0].Limit)
	}

	second := mcpCallTool(t, session, "list_entities", map[string]any{"cursor": *page.NextCursor})
	var continued EntityCollectionBody
	mcpBody(t, second, &continued)
	if len(requested) != 2 {
		t.Fatalf("service calls = %d, want 2", len(requested))
	}
	if requested[1].AfterID == nil || *requested[1].AfterID != apiEntityID {
		t.Fatalf("second page params = %#v", requested[1])
	}
	if continued.NextCursor != nil {
		t.Fatalf("final page body = %#v, want no next_cursor", continued)
	}
}

// TestMCPRejectsMalformedArgumentsBeforeTheService proves decoding and schema
// validation reject malformed tool input without reaching the service. A
// malformed scalar the decode path rejects carries its stable failure code in
// the error text, because decode rejection publishes no structured content.
func TestMCPRejectsMalformedArgumentsBeforeTheService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
		code      string
	}{
		{"malformed entity ID", "get_entity", map[string]any{"entity_id": "not-an-id"}, "invalid_entity_id"},
		{
			"malformed entity ID in a command",
			"execute_entity_command",
			map[string]any{"entity_id": "not-an-id", "operation": "set", "parameters": map[string]any{"value": true}},
			"invalid_entity_id",
		},
		{"malformed device ID", "get_device", map[string]any{"device_id": "not-a-device"}, "invalid_device_id"},
		{"malformed adapter ID", "get_adapter", map[string]any{"adapter_id": "Not A Slug"}, "invalid_adapter_id"},
		{
			"malformed command ID",
			"get_command",
			map[string]any{"command_id": string(apiEntityID)},
			"invalid_command_id",
		},
		{"limit above the range", "list_entities", map[string]any{"limit": 201}, "invalid_limit"},
		{"limit below the range", "list_entities", map[string]any{"limit": 0}, "invalid_limit"},
		{
			"entity limit above the range",
			"get_device",
			map[string]any{"device_id": string(apiDeviceID), "entity_limit": 201},
			"invalid_limit",
		},
		// The SDK schema rejects these before the decode path, so they have no
		// field-specific code; they must still never reach the service.
		{"missing required argument", "get_entity", map[string]any{}, ""},
		{"unknown argument", "get_entity", map[string]any{"entity_id": string(apiEntityID), "verbose": true}, ""},
		{"wrong argument type", "list_entities", map[string]any{"cursor": 5}, ""},
		{
			"reserved command identity",
			"execute_entity_command",
			map[string]any{
				"entity_id": string(apiEntityID), "operation": "set",
				"parameters": map[string]any{"value": true}, "command_id": string(apiCommandID),
			},
			"",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// stubDevices panics on any call: invalid input must never reach it.
			session := mcpSession(t, &stubDevices{})
			result := mcpCallTool(t, session, test.tool, test.arguments)
			text := mcpErrorText(t, result)
			if text == "" {
				t.Fatalf("%s error text is empty", test.tool)
			}
			if test.code != "" && !strings.HasPrefix(text, test.code+": ") {
				t.Fatalf("%s error = %q, want the %q failure code", test.tool, text, test.code)
			}
			if result.StructuredContent != nil {
				t.Fatalf(
					"%s structured content = %#v, want none for invalid input",
					test.tool, result.StructuredContent,
				)
			}
		})
	}
}

// TestMCPMapsReadFailuresToStableFailureCodes proves each read's domain failure
// reaches the agent as an isError result carrying its stable failure code.
func TestMCPMapsReadFailuresToStableFailureCodes(t *testing.T) {
	t.Parallel()
	unavailable := errors.New("SQLite unavailable")
	tests := []struct {
		name      string
		service   *stubDevices
		tool      string
		arguments map[string]any
		code      string
	}{
		{
			"entity not found",
			&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
				return devices.EntityWithState{}, devices.ErrEntityNotFound
			}},
			"get_entity", map[string]any{"entity_id": string(apiEntityID)}, "entity_not_found",
		},
		{
			"device not found",
			&stubDevices{getDevice: func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error) {
				return devices.DeviceAggregate{}, devices.ErrDeviceNotFound
			}},
			"get_device", map[string]any{"device_id": string(apiDeviceID)}, "device_not_found",
		},
		{
			"adapter not found",
			&stubDevices{getAdapter: func(context.Context, string) (devices.AdapterInstance, error) {
				return devices.AdapterInstance{}, devices.ErrAdapterNotFound
			}},
			"get_adapter", map[string]any{"adapter_id": apiAdapterID}, "adapter_not_found",
		},
		{
			"command not found",
			&stubDevices{getCommand: func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
				return devices.CommandRecord{}, devices.ErrCommandNotFound
			}},
			"get_command", map[string]any{"command_id": string(apiCommandID)}, "command_not_found",
		},
		{
			"entity history parent not found",
			&stubDevices{listEntityEvents: func(
				context.Context,
				devices.ListEntityEventsParams,
			) (devices.Page[devices.EntityEventHistoryEntry], error) {
				return devices.Page[devices.EntityEventHistoryEntry]{}, devices.ErrEntityNotFound
			}},
			"list_entity_events", map[string]any{"entity_id": string(apiEntityID)}, "entity_not_found",
		},
		{
			"rejected page",
			&stubDevices{listEntities: func(
				context.Context,
				devices.ListEntitiesParams,
			) (devices.Page[devices.EntityWithState], error) {
				return devices.Page[devices.EntityWithState]{}, devices.ErrInvalidPage
			}},
			"list_entities", map[string]any{}, "invalid_request",
		},
		{
			"uninterpretable cursor",
			&stubDevices{},
			"list_entities", map[string]any{"cursor": "not-a-cursor"}, "invalid_request",
		},
		{
			"internal failure",
			&stubDevices{listAdapters: func(
				context.Context,
				devices.ListAdaptersParams,
			) (devices.Page[devices.AdapterInstance], error) {
				return devices.Page[devices.AdapterInstance]{}, unavailable
			}},
			"list_adapters", map[string]any{}, "internal_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := mcpSession(t, test.service)
			result := mcpCallTool(t, session, test.tool, test.arguments)
			text := mcpErrorText(t, result)
			if !strings.HasPrefix(text, test.code+": ") {
				t.Fatalf("%s error = %q, want the %q failure code", test.tool, text, test.code)
			}
		})
	}
}

// TestMCPMapsEveryCommandFailureToItsDurableCode proves each ExecuteCommand
// outcome crosses as the durable Command failure code.
func TestMCPMapsEveryCommandFailureToItsDurableCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cause error
		code  string
	}{
		{"disabled Entity", devices.ErrEntityDisabled, "entity_disabled"},
		{"unhealthy Adapter", devices.ErrAdapterUnhealthy, "adapter_unhealthy"},
		{"unavailable Entity", devices.ErrEntityUnavailable, "entity_unavailable"},
		{"upstream rejection", devices.ErrUpstreamRejected, "upstream_rejected"},
		{"outcome timeout", devices.ErrOutcomeTimeout, "outcome_timeout"},
		{"closed Command admission", devices.ErrCommandUnavailable, "command_unavailable"},
		{"invalid Command", devices.ErrInvalidCommand, "invalid_request"},
		{"unknown Entity", devices.ErrEntityNotFound, "entity_not_found"},
		{"internal failure", errors.New("SQLite unavailable"), "internal_error"},
	}
	arguments := map[string]any{
		"entity_id": string(apiEntityID), "operation": "set", "parameters": map[string]any{"value": true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := mcpSession(t, &stubDevices{executeCommand: func(
				context.Context,
				devices.CommandInput,
			) (devices.CommandResult, error) {
				return devices.CommandResult{}, &devices.CommandExecutionError{
					CommandID: apiCommandID, Err: test.cause,
				}
			}})
			result := mcpCallTool(t, session, "execute_entity_command", arguments)
			text := mcpErrorText(t, result)
			if !strings.HasPrefix(text, test.code+": ") {
				t.Fatalf("failure %v error = %q, want the %q code", test.cause, text, test.code)
			}
		})
	}
}

// mcpAssertInternalFailureLogged proves one tool call failed the way an
// unpublished internal failure must: the client receives only the generic
// internal_error result with no server detail, and the server records exactly one
// structured diagnostic with the fixed code and Go error type but never the
// retained cause text.
func mcpAssertInternalFailureLogged(
	t *testing.T,
	result *mcp.CallToolResult,
	logs *bytes.Buffer,
	tool string,
	cause string,
) {
	t.Helper()
	if text := mcpErrorText(t, result); text != "internal_error: internal error" {
		t.Fatalf("%s error text = %q, want the generic message", tool, text)
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s structured content = nil, want the failure fields", tool)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), cause) {
		t.Fatalf("%s structured content leaked the cause: %s", tool, raw)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if fields["failure_code"] != "internal_error" {
		t.Fatalf("%s failure_code = %#v, want internal_error", tool, fields["failure_code"])
	}

	record := mcpLogRecord(t, logs.Bytes())
	if record["level"] != "ERROR" || record["event"] != "mcp.tool_internal_failure" ||
		record["tool"] != tool {
		t.Fatalf("%s log record = %#v", tool, record)
	}
	if record["error_code"] != "internal_error" {
		t.Fatalf("%s log error_code = %#v, want internal_error", tool, record["error_code"])
	}
	if errorType, _ := record["error_type"].(string); errorType == "" {
		t.Fatalf("%s log error_type = %#v, want a non-empty Go error type name", tool, record["error_type"])
	}
	if _, ok := record["error"]; ok {
		t.Fatalf("%s log record = %#v, want no raw error field", tool, record)
	}
	if strings.Contains(logs.String(), cause) {
		t.Fatalf("%s logs leaked the cause: %s", tool, logs.Bytes())
	}
}

// TestMCPExecuteEntityCommandLogsInternalCauseWithoutLeakingIt proves a
// production internal failure emits one structured server diagnostic with safe
// metadata while the raw cause never reaches the client or the log.
func TestMCPExecuteEntityCommandLogsInternalCauseWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	const cause = "SQLite unavailable: /var/lib/hearth/hearth.db"
	var logs bytes.Buffer
	session := mcpSessionWithLogs(t, &stubDevices{executeCommand: func(
		context.Context,
		devices.CommandInput,
	) (devices.CommandResult, error) {
		return devices.CommandResult{}, errors.New(cause)
	}}, &logs)

	result := mcpCallTool(t, session, "execute_entity_command", map[string]any{
		"entity_id": string(apiEntityID), "operation": "set", "parameters": map[string]any{"value": true},
	})
	mcpAssertInternalFailureLogged(t, result, &logs, "execute_entity_command", "hearth.db")
}

// TestMCPReadLogsInternalServiceCauseWithoutLeakingIt proves a production
// internal failure on a Huma-backed read emits one structured server diagnostic
// with safe metadata while the raw service cause stays out of both halves. The
// shared Huma operation maps an unclassified service failure to a generic 500,
// so retaining the cause keeps the failure type diagnosable without logging the
// unknown message.
func TestMCPReadLogsInternalServiceCauseWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	const cause = "SQLite unavailable: /var/lib/hearth/hearth.db"
	var logs bytes.Buffer
	session := mcpSessionWithLogs(t, &stubDevices{getEntity: func(
		context.Context,
		devices.EntityID,
	) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, errors.New(cause)
	}}, &logs)

	result := mcpCallTool(t, session, "get_entity", map[string]any{"entity_id": string(apiEntityID)})
	mcpAssertInternalFailureLogged(t, result, &logs, "get_entity", "hearth.db")
}

// TestMCPReadLogsInternalMappingCauseWithoutLeakingIt proves a response-mapping
// failure, which the shared Huma operation reports only as a generic 500, also
// reaches the diagnostic log as safe metadata with its raw cause absent.
func TestMCPReadLogsInternalMappingCauseWithoutLeakingIt(t *testing.T) {
	t.Parallel()
	const cause = "decode entity support"
	var logs bytes.Buffer
	session := mcpSessionWithLogs(t, &stubDevices{getEntity: func(
		context.Context,
		devices.EntityID,
	) (devices.EntityWithState, error) {
		entity := apiEntityWithState(nil)
		entity.Entity.Support = devices.EntitySupport("{not a support document")
		return entity, nil
	}}, &logs)

	result := mcpCallTool(t, session, "get_entity", map[string]any{"entity_id": string(apiEntityID)})
	mcpAssertInternalFailureLogged(t, result, &logs, "get_entity", cause)
}

// TestMCPCommandFailureCarriesStructuredCodeAndStatus proves the tool error for a
// refused Command preserves the durable failure code, Command status, and
// Command ID that clients receive as structured content.
func TestMCPCommandFailureCarriesStructuredCodeAndStatus(t *testing.T) {
	t.Parallel()
	err := mcpCommandFailure(&devices.CommandExecutionError{
		CommandID: apiCommandID, Err: devices.ErrEntityDisabled,
	})
	var toolError *mcpapi.ToolError
	if !errors.As(err, &toolError) {
		t.Fatalf("mcpCommandFailure = %#v, want a ToolError", err)
	}
	if toolError.Code != "entity_disabled" || toolError.Message != "entity is disabled" {
		t.Fatalf("tool error = %#v", toolError)
	}
	if toolError.Details["status"] != string(devices.CommandStatusEntityDisabled) {
		t.Fatalf("tool error status = %#v", toolError.Details)
	}
	if toolError.Details["command_id"] != string(apiCommandID) {
		t.Fatalf("tool error command_id = %#v", toolError.Details)
	}
}

// TestMCPExecuteEntityCommandBlocksUntilTheTerminalOutcome proves the tool
// delegates the blocking ExecuteCommand unchanged: it returns only after the
// service reports the Command's terminal outcome.
func TestMCPExecuteEntityCommandBlocksUntilTheTerminalOutcome(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	released := make(chan struct{})
	stub := &stubDevices{executeCommand: func(
		_ context.Context,
		_ devices.CommandInput,
	) (devices.CommandResult, error) {
		close(started)
		<-released
		observationID := apiObservationID
		value := devices.Value(`true`)
		return devices.CommandResult{
			CommandID: apiCommandID, Outcome: devices.OutcomeObserved,
			ObservationID: &observationID, Value: &value,
		}, nil
	}}
	session := mcpSession(t, stub)
	done := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "execute_entity_command",
			Arguments: map[string]any{
				"entity_id": string(apiEntityID), "operation": "set",
				"parameters": map[string]any{"value": true},
			},
		})
		if err != nil {
			t.Errorf("call execute_entity_command: %v", err)
			done <- nil
			return
		}
		done <- result
	}()

	<-started
	select {
	case <-done:
		t.Fatal("execute_entity_command returned before the Command reached its terminal outcome")
	default:
	}
	close(released)
	result := <-done
	if result == nil {
		t.Fatal("execute_entity_command returned no result")
	}
	var body CommandResultBody
	mcpBody(t, result, &body)
	if body.CommandID != string(apiCommandID) || body.Status != "satisfied" {
		t.Fatalf("execute_entity_command body = %#v", body)
	}
}

// TestMCPReadInternalProblemRendersTheGenericHumaProblem proves the cause a read
// retains never changes the REST response: the shared read handlers still return
// the same generic 500 problem document, content type, and schema link, and the
// cause never appears in it.
func TestMCPReadInternalProblemRendersTheGenericHumaProblem(t *testing.T) {
	t.Parallel()
	const cause = "SQLite unavailable: /var/lib/hearth/hearth.db"
	router, _ := testAPI(t, &stubDevices{getEntity: func(
		context.Context,
		devices.EntityID,
	) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, errors.New(cause)
	}})

	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", contentType)
	}
	if link := response.Header().Get("Link"); !strings.Contains(link, "ErrorModel.json") {
		t.Fatalf("Link = %q, want the generic problem schema link", link)
	}
	body := response.Body.String()
	for _, want := range []string{
		`"$schema"`, `"title":"Internal Server Error"`, `"status":500`, `"detail":"internal error"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "hearth.db") || strings.Contains(body, "SQLite") {
		t.Fatalf("body leaked the cause: %s", body)
	}
}
