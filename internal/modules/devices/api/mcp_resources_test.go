package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
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
			adapters.ListAdaptersParams,
		) (adapters.Page[adapters.AdapterInstance], error) {
			return adapters.Page[adapters.AdapterInstance]{Items: []adapters.AdapterInstance{{
				ID: apiAdapterID, Health: adapters.AdapterHealth{Status: "healthy", Source: "adapter"},
			}}}, nil
		},
		getAdapter: func(context.Context, string) (adapters.AdapterInstance, error) {
			return adapters.AdapterInstance{
				ID: apiAdapterID, Health: adapters.AdapterHealth{Status: "healthy", Source: "adapter"},
			}, nil
		},
		listAdapterHealthHistory: func(
			context.Context,
			adapters.ListAdapterHealthParams,
		) (adapters.Page[adapters.HealthTransition], error) {
			return adapters.Page[adapters.HealthTransition]{Items: []adapters.HealthTransition{{
				Status: "healthy", Source: "adapter", ObservedAt: observedAt, ReceiveOrder: 3,
			}}}, nil
		},
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
