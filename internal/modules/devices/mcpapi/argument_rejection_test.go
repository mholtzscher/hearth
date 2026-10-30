package mcpapi //nolint:testpackage // Exercises actual SDK decoding through the module's wire fixture.

import (
	"context"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestOptionalIDNullRejectionsCarryFieldCodes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ tool, arguments, text string }{
		{"list_entities", `{"device_id":null}`, "invalid_device_id: device_id must not be null"},
		{"list_commands", `{"entity_id":null}`, "invalid_entity_id: entity_id must not be null"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			t.Parallel()
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
			// Any service call panics; null cannot become an omitted filter.
			Register(server, &stubDevices{})
			result := callWire(t, server.HTTPHandler(), test.tool, test.arguments)
			if !result.IsError || len(result.Structured) != 0 || len(result.Content) != 1 ||
				result.Content[0].Text != test.text {
				t.Fatalf("rejection=%+v, want unstructured error text %q", result, test.text)
			}
		})
	}
}

func TestOptionalIDFilterOmissionRemainsUnfiltered(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"list_entities", "list_commands"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			calls := 0
			service := &stubDevices{
				listEntities: func(_ context.Context, params devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
					calls++
					if params.DeviceID != nil || params.Limit != 50 {
						t.Fatalf("changed omitted-filter query: %+v", params)
					}
					return devices.Page[devices.EntityWithState]{}, nil
				},
				listCommands: func(_ context.Context, params devices.ListCommandsParams) (devices.Page[devices.CommandRecord], error) {
					calls++
					if params.EntityID != nil || params.Limit != 50 {
						t.Fatalf("changed omitted-filter query: %+v", params)
					}
					return devices.Page[devices.CommandRecord]{}, nil
				},
			}
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
			Register(server, service)
			assertWireJSON(t, callWire(t, server.HTTPHandler(), tool, `{}`), `{"items":[]}`)
			if calls != 1 {
				t.Fatalf("service calls=%d, want=1", calls)
			}
		})
	}
}
