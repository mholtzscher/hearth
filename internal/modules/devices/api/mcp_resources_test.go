package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func mcpResourceFixtureDevices() *stubDevices {
	observedAt := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	transition := devices.HealthTransition{
		Status: "available", Source: "entity_report", ObservedAt: observedAt, ReceiveOrder: 3,
	}
	return &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			return apiEntityWithState(nil), nil
		},
		listEntities: func(
			context.Context,
			devices.ListEntitiesParams,
		) (devices.Page[devices.EntityWithState], error) {
			return devices.Page[devices.EntityWithState]{
				Items: []devices.EntityWithState{apiEntityWithState(nil)},
			}, nil
		},
		listDevices: func(context.Context, devices.ListDevicesParams) (devices.Page[devices.Device], error) {
			return devices.Page[devices.Device]{Items: []devices.Device{{
				ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: "Office",
			}}}, nil
		},
		getDevice: func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error) {
			return devices.DeviceAggregate{
				Device: devices.Device{ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: "Office"},
				Entities: devices.Page[devices.EntityWithState]{
					Items: []devices.EntityWithState{apiEntityWithState(nil)},
				},
			}, nil
		},
		getCommand: func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
			return listableCommand(), nil
		},
		listCommands: func(
			context.Context,
			devices.ListCommandsParams,
		) (devices.Page[devices.CommandRecord], error) {
			return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
		},
		listEntityCommands: func(
			context.Context,
			devices.ListEntityCommandsParams,
		) (devices.Page[devices.CommandRecord], error) {
			return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
		},
		listEntityStateHistory: func(
			context.Context,
			devices.ListEntityStateHistoryParams,
		) (devices.Page[devices.EntityStateHistoryEntry], error) {
			return devices.Page[devices.EntityStateHistoryEntry]{
				Items: []devices.EntityStateHistoryEntry{apiStateHistoryEntry(3)},
			}, nil
		},
		listEntityEvents: func(
			context.Context,
			devices.ListEntityEventsParams,
		) (devices.Page[devices.EntityEventHistoryEntry], error) {
			return devices.Page[devices.EntityEventHistoryEntry]{
				Items: []devices.EntityEventHistoryEntry{apiEntityEventEntry(3)},
			}, nil
		},
		listEntityAvailabilityHistory: func(
			context.Context,
			devices.ListEntityAvailabilityParams,
		) (devices.Page[devices.HealthTransition], error) {
			return devices.Page[devices.HealthTransition]{Items: []devices.HealthTransition{transition}}, nil
		},
		listAdapters: func(
			context.Context,
			devices.ListAdaptersParams,
		) (devices.Page[devices.AdapterInstance], error) {
			return devices.Page[devices.AdapterInstance]{Items: []devices.AdapterInstance{{
				ID: apiAdapterID, Health: devices.AdapterHealth{Status: "healthy", Source: "adapter"},
			}}}, nil
		},
		getAdapter: func(context.Context, string) (devices.AdapterInstance, error) {
			return devices.AdapterInstance{
				ID: apiAdapterID, Health: devices.AdapterHealth{Status: "healthy", Source: "adapter"},
			}, nil
		},
		listAdapterHealthHistory: func(
			context.Context,
			devices.ListAdapterHealthParams,
		) (devices.Page[devices.HealthTransition], error) {
			return devices.Page[devices.HealthTransition]{Items: []devices.HealthTransition{transition}}, nil
		},
	}
}

// TestRegisterMCPRegistersEveryDeviceResourceTemplate proves the catalog exposes
// exactly the device-backed hearth:// templates with JSON contents.
func TestRegisterMCPRegistersEveryDeviceResourceTemplate(t *testing.T) {
	t.Parallel()
	session := mcpSession(t, &stubDevices{})
	listed, err := session.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	want := []string{
		"hearth://entity/{entity_id}",
		"hearth://entity/{entity_id}/state/history{?filter,cursor,limit}",
		"hearth://entity/{entity_id}/events{?cursor,limit}",
		"hearth://entity/{entity_id}/commands{?status,cursor,limit}",
		"hearth://entity/{entity_id}/availability/history{?cursor,limit}",
		"hearth://device/{device_id}",
		"hearth://adapter/{adapter_id}",
		"hearth://adapter/{adapter_id}/health/history{?cursor,limit}",
		"hearth://command/{command_id}",
		"hearth://commands{?entity_id,status,cursor,limit}",
		"hearth://entities{?device_id,cursor,limit}",
		"hearth://devices{?cursor,limit}",
		"hearth://adapters{?cursor,limit}",
	}
	if len(listed.ResourceTemplates) != len(want) {
		t.Fatalf("templates = %d, want %d", len(listed.ResourceTemplates), len(want))
	}
	registered := make(map[string]*mcp.ResourceTemplate, len(listed.ResourceTemplates))
	for _, template := range listed.ResourceTemplates {
		registered[template.URITemplate] = template
	}
	for _, uriTemplate := range want {
		template, present := registered[uriTemplate]
		if !present {
			t.Fatalf("template %q is missing", uriTemplate)
		}
		if template.Description == "" || template.MIMEType != "application/json" {
			t.Fatalf("template %q = %#v", uriTemplate, template)
		}
	}
}

// TestRegisterMCPRegistersCollectionResources proves the four parameterless
// collections are also served as concrete resources, so resources/list is
// non-empty for clients that materialize resources. Paged reads keep flowing
// through the templates.
func TestRegisterMCPRegistersCollectionResources(t *testing.T) {
	t.Parallel()
	stub := mcpResourceFixtureDevices()
	session := mcpSession(t, stub)
	listed, err := session.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	want := []string{
		"hearth://adapters",
		"hearth://entities",
		"hearth://devices",
		"hearth://commands",
	}
	if len(listed.Resources) != len(want) {
		t.Fatalf("resources = %d, want %d", len(listed.Resources), len(want))
	}
	registered := make(map[string]*mcp.Resource, len(listed.Resources))
	for _, resource := range listed.Resources {
		registered[resource.URI] = resource
	}
	for _, uri := range want {
		resource, present := registered[uri]
		if !present {
			t.Fatalf("resource %q is missing", uri)
		}
		if resource.Name == "" || resource.Description == "" || resource.MIMEType != "application/json" {
			t.Fatalf("resource %q = %#v", uri, resource)
		}
		read, readErr := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
		if readErr != nil {
			t.Fatalf("read %s: %v", uri, readErr)
		}
		if len(read.Contents) != 1 || read.Contents[0].URI != uri ||
			read.Contents[0].MIMEType != "application/json" {
			t.Fatalf("content = %#v", read.Contents)
		}
		var body map[string]any
		if decodeErr := json.Unmarshal([]byte(read.Contents[0].Text), &body); decodeErr != nil {
			t.Fatalf("decode %s: %v", uri, decodeErr)
		}
		if _, hasItems := body["items"]; !hasItems {
			t.Fatalf("resource %s body has no items page: %s", uri, read.Contents[0].Text)
		}
	}
}

// TestMCPResourcesReadTheSameBodiesAsTheHumaRoutes proves every device resource
// reads through the same service method and returns the same JSON body as its
// Huma GET counterpart.
//
//nolint:gocognit // One resource/route body contract per template, kept together.
func TestMCPResourcesReadTheSameBodiesAsTheHumaRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		uri  string
		path string
	}{
		{"entity", "hearth://entity/" + string(apiEntityID), "/v1/entities/" + string(apiEntityID)},
		{
			"entity state history",
			"hearth://entity/" + string(apiEntityID) + "/state/history?filter=all",
			"/v1/entities/" + string(apiEntityID) + "/state/history?disposition=all",
		},
		{
			"entity events",
			"hearth://entity/" + string(apiEntityID) + "/events",
			"/v1/entities/" + string(apiEntityID) + "/events",
		},
		{
			"entity commands",
			"hearth://entity/" + string(apiEntityID) + "/commands",
			"/v1/entities/" + string(apiEntityID) + "/commands",
		},
		{
			"entity commands filtered by status",
			"hearth://entity/" + string(apiEntityID) + "/commands?status=satisfied",
			"/v1/commands?entity_id=" + string(apiEntityID) + "&status=satisfied",
		},
		{
			"entity availability history",
			"hearth://entity/" + string(apiEntityID) + "/availability/history",
			"/v1/entities/" + string(apiEntityID) + "/availability/history",
		},
		{"device", "hearth://device/" + string(apiDeviceID), "/v1/devices/" + string(apiDeviceID)},
		{"adapter", "hearth://adapter/" + apiAdapterID, "/v1/adapters/" + apiAdapterID},
		{
			"adapter health history",
			"hearth://adapter/" + apiAdapterID + "/health/history",
			"/v1/adapters/" + apiAdapterID + "/health/history",
		},
		{"command", "hearth://command/" + string(apiCommandID), "/v1/commands/" + string(apiCommandID)},
		{"commands", "hearth://commands?status=satisfied", "/v1/commands?status=satisfied"},
		{"entities", "hearth://entities?limit=5", "/v1/entities?limit=5"},
		{"devices", "hearth://devices?limit=5", "/v1/devices?limit=5"},
		{"adapters", "hearth://adapters?limit=5", "/v1/adapters?limit=5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := mcpResourceFixtureDevices()
			session := mcpSession(t, stub)
			read, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: test.uri})
			if err != nil {
				t.Fatalf("read %s: %v", test.uri, err)
			}
			if len(read.Contents) != 1 {
				t.Fatalf("contents = %#v, want one", read.Contents)
			}
			if read.Contents[0].URI != test.uri || read.Contents[0].MIMEType != "application/json" {
				t.Fatalf("content = %#v", read.Contents[0])
			}
			router, _ := testAPI(t, stub)
			response := performRequest(router, test.path)
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, body = %s", test.path, response.Code, response.Body.String())
			}
			var fromRoute any
			if bodyErr := json.Unmarshal(response.Body.Bytes(), &fromRoute); bodyErr != nil {
				t.Fatalf("unmarshal route body: %v", bodyErr)
			}
			// Huma links its response bodies to the OpenAPI schema with a $schema
			// envelope key; the resource document is the body without it.
			if routeBody, ok := fromRoute.(map[string]any); ok {
				delete(routeBody, "$schema")
			}
			var fromResource any
			if textErr := json.Unmarshal([]byte(read.Contents[0].Text), &fromResource); textErr != nil {
				t.Fatalf("unmarshal resource text: %v", textErr)
			}
			if !reflect.DeepEqual(fromResource, fromRoute) {
				t.Fatalf("resource body %s != route body %s", read.Contents[0].Text, response.Body.String())
			}
		})
	}
}

// TestMCPResourcesForwardFiltersCursorsAndPageDefaults proves resources forward
// their filters and opaque cursors to the shared service calls.
func TestMCPResourcesForwardFiltersCursorsAndPageDefaults(t *testing.T) {
	t.Parallel()
	var stateParams devices.ListEntityStateHistoryParams
	var commandParams devices.ListCommandsParams
	var entityParams devices.ListEntitiesParams
	var deviceCursor *devices.DeviceID
	cursor, err := encodeDevicesCursor(apiDeviceID)
	if err != nil {
		t.Fatalf("encode device cursor: %v", err)
	}
	stub := mcpResourceFixtureDevices()
	stub.listEntityStateHistory = func(
		_ context.Context,
		params devices.ListEntityStateHistoryParams,
	) (devices.Page[devices.EntityStateHistoryEntry], error) {
		stateParams = params
		return devices.Page[devices.EntityStateHistoryEntry]{
			Items: []devices.EntityStateHistoryEntry{apiStateHistoryEntry(3)},
		}, nil
	}
	stub.listCommands = func(
		_ context.Context,
		params devices.ListCommandsParams,
	) (devices.Page[devices.CommandRecord], error) {
		commandParams = params
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
	}
	stub.listEntities = func(
		_ context.Context,
		params devices.ListEntitiesParams,
	) (devices.Page[devices.EntityWithState], error) {
		entityParams = params
		return devices.Page[devices.EntityWithState]{
			Items: []devices.EntityWithState{apiEntityWithState(nil)},
		}, nil
	}
	stub.listDevices = func(
		_ context.Context,
		params devices.ListDevicesParams,
	) (devices.Page[devices.Device], error) {
		deviceCursor = params.AfterID
		return devices.Page[devices.Device]{}, nil
	}
	session := mcpSession(t, stub)

	readMCPResource(t, session, "hearth://entity/"+string(apiEntityID)+"/state/history?filter=unchanged&limit=7")
	if stateParams.EntityID != apiEntityID || stateParams.Filter != devices.EntityStateHistoryFilterUnchanged ||
		stateParams.Limit != 7 {
		t.Fatalf("State history params = %#v", stateParams)
	}

	readMCPResource(t, session, "hearth://commands?entity_id="+string(apiEntityID)+"&status=satisfied")
	if commandParams.EntityID == nil || *commandParams.EntityID != apiEntityID ||
		commandParams.Status == nil || *commandParams.Status != devices.CommandStatusSatisfied ||
		commandParams.Limit != 50 {
		t.Fatalf("ListCommands params = %#v", commandParams)
	}

	readMCPResource(t, session, "hearth://entities?device_id="+string(apiDeviceID))
	if entityParams.DeviceID == nil || *entityParams.DeviceID != apiDeviceID ||
		entityParams.Limit != 50 {
		t.Fatalf("ListEntities params = %#v", entityParams)
	}

	readMCPResource(t, session, "hearth://devices?cursor="+cursor)
	if deviceCursor == nil || *deviceCursor != apiDeviceID {
		t.Fatalf("ListDevices cursor = %v", deviceCursor)
	}
}

// TestMCPResourcesFilterEntityCommandsByStatus proves the entity Command
// resource honors the catalog's status filter: a filtered read proves the Entity
// exists and then forwards the Entity and status to the household Command read,
// while an unfiltered read stays on ListEntityCommands.
func TestMCPResourcesFilterEntityCommandsByStatus(t *testing.T) {
	t.Parallel()
	var filtered devices.ListCommandsParams
	var unfiltered devices.ListEntityCommandsParams
	stub := mcpResourceFixtureDevices()
	stub.listCommands = func(
		_ context.Context,
		params devices.ListCommandsParams,
	) (devices.Page[devices.CommandRecord], error) {
		filtered = params
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
	}
	stub.listEntityCommands = func(
		_ context.Context,
		params devices.ListEntityCommandsParams,
	) (devices.Page[devices.CommandRecord], error) {
		unfiltered = params
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{listableCommand()}}, nil
	}
	session := mcpSession(t, stub)

	readMCPResource(t, session,
		"hearth://entity/"+string(apiEntityID)+"/commands?status=satisfied&limit=7")
	if filtered.EntityID == nil || *filtered.EntityID != apiEntityID ||
		filtered.Status == nil || *filtered.Status != devices.CommandStatusSatisfied ||
		filtered.Limit != 7 {
		t.Fatalf("filtered ListCommands params = %#v", filtered)
	}

	readMCPResource(t, session, "hearth://entity/"+string(apiEntityID)+"/commands")
	if unfiltered.EntityID != apiEntityID || unfiltered.Limit != 50 || unfiltered.BeforeID != nil {
		t.Fatalf("unfiltered ListEntityCommands params = %#v", unfiltered)
	}
}

// TestMCPResourcesReportMissingParentForStatusFilteredEntityCommands proves the
// status-filtered Entity Command resource keeps the unfiltered read's missing
// parent outcome by confirming the Entity exists first.
func TestMCPResourcesReportMissingParentForStatusFilteredEntityCommands(t *testing.T) {
	t.Parallel()
	var listCalled atomic.Bool
	stub := mcpResourceFixtureDevices()
	stub.getEntity = func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, devices.ErrEntityNotFound
	}
	stub.listCommands = func(
		context.Context,
		devices.ListCommandsParams,
	) (devices.Page[devices.CommandRecord], error) {
		listCalled.Store(true)
		return devices.Page[devices.CommandRecord]{}, nil
	}
	session := mcpSession(t, stub)
	_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "hearth://entity/" + string(apiEntityID) + "/commands?status=satisfied",
	})
	if err == nil || !strings.Contains(err.Error(), "Resource not found") {
		t.Fatalf("read missing Entity Command history = %v, want resource-not-found", err)
	}
	if listCalled.Load() {
		t.Fatal("ListCommands ran for an unknown parent Entity")
	}
}

func readMCPResource(t *testing.T, session *mcp.ClientSession, uri string) *mcp.ReadResourceResult {
	t.Helper()
	read, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read %s: %v", uri, err)
	}
	if len(read.Contents) != 1 {
		t.Fatalf("read %s contents = %#v, want one", uri, read.Contents)
	}
	return read
}

// TestMCPResourcesRejectUnreadableURIs proves a resource read rejects a
// parameter it cannot honor and an identifier it cannot resolve instead of
// returning a broader page.
func TestMCPResourcesRejectUnreadableURIs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		uri     string
		message string
	}{
		{"unsupported parameter", "hearth://entities?verbose=true", "unsupported query parameter"},
		{
			"invalid status on Entity commands",
			"hearth://entity/" + string(apiEntityID) + "/commands?status=exploded",
			"invalid status",
		},
		{"repeated parameter", "hearth://entities?limit=1&limit=2", "must appear once"},
		{"non-numeric limit", "hearth://entities?limit=lots", "limit must be an integer"},
		{"limit above the range", "hearth://entities?limit=201", "limit must be between 1 and 200"},
		{"malformed entity ID", "hearth://entity/not-an-id", "Resource not found"},
		{"malformed command ID", "hearth://command/" + string(apiEntityID), "Resource not found"},
		{"malformed adapter ID", "hearth://adapter/Not-A-Slug", "Resource not found"},
		{"unknown kind", "hearth://unknown", "Resource not found"},
		{"unknown entity suffix", "hearth://entity/" + string(apiEntityID) + "/bogus", "Resource not found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := mcpSession(t, &stubDevices{})
			_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: test.uri})
			if err == nil {
				t.Fatalf("read %s succeeded, want an error", test.uri)
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("read %s error = %q, want %q", test.uri, err.Error(), test.message)
			}
		})
	}
}

// TestMCPResourcesReportMissingParentsAsNotFound proves a domain 404 becomes the
// MCP resource-not-found error instead of an internal failure.
func TestMCPResourcesReportMissingParentsAsNotFound(t *testing.T) {
	t.Parallel()
	stub := mcpResourceFixtureDevices()
	stub.getEntity = func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, devices.ErrEntityNotFound
	}
	session := mcpSession(t, stub)
	_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: "hearth://entity/" + string(apiEntityID),
	})
	if err == nil || !strings.Contains(err.Error(), "Resource not found") {
		t.Fatalf("read missing entity = %v, want resource-not-found", err)
	}
}

// TestParseMCPResourceURI proves the parser splits accepted hearth:// URIs and
// rejects malformed ones.
//
//nolint:gocognit // One accepted shape and one rejected shape per subtest, kept together.
func TestParseMCPResourceURI(t *testing.T) {
	t.Parallel()
	accepted := []struct {
		uri      string
		kind     string
		segments []string
		query    map[string]string
	}{
		{
			"hearth://entity/" + string(apiEntityID), mcpResourceKindEntity,
			[]string{string(apiEntityID)}, map[string]string{},
		},
		{
			"hearth://entity/" + string(apiEntityID) + "/state/history?filter=all&limit=5&cursor=abc",
			mcpResourceKindEntity,
			[]string{string(apiEntityID), "state", "history"},
			map[string]string{"filter": "all", "limit": "5", "cursor": "abc"},
		},
		{"hearth://devices", mcpResourceKindDevices, []string{}, map[string]string{}},
		{
			"hearth://commands?status=satisfied", mcpResourceKindCommands,
			[]string{}, map[string]string{"status": "satisfied"},
		},
	}
	for _, test := range accepted {
		t.Run("accept "+test.uri, func(t *testing.T) {
			t.Parallel()
			resource, err := parseMCPResourceURI(test.uri)
			if err != nil {
				t.Fatalf("parse %s: %v", test.uri, err)
			}
			if resource.kind != test.kind || !reflect.DeepEqual(resource.segments, test.segments) {
				t.Fatalf("parse %s = %#v", test.uri, resource)
			}
			if len(resource.query) != len(test.query) {
				t.Fatalf("parse %s query = %#v, want %#v", test.uri, resource.query, test.query)
			}
			for key, value := range test.query {
				if resource.query.Get(key) != value {
					t.Fatalf("parse %s query = %#v, want %v=%q", test.uri, resource.query, key, value)
				}
			}
		})
	}

	rejected := []string{
		"https://entity/" + string(apiEntityID),
		"hearth://",
		"hearth://entity/" + string(apiEntityID) + "/../secret",
		"hearth://entity/" + string(apiEntityID) + "//events",
		"hearth://entity/" + string(apiEntityID) + "#fragment",
	}
	for _, uri := range rejected {
		t.Run("reject "+uri, func(t *testing.T) {
			t.Parallel()
			if _, err := parseMCPResourceURI(uri); err == nil {
				t.Fatalf("parse %s succeeded, want an error", uri)
			}
		})
	}
}
