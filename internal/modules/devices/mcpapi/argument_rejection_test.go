package mcpapi //nolint:testpackage // Exercises shared input decoding through the module's wire fixture.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestOptionalIDNullRejectionsCarryFieldCodes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ tool, arguments, code, message string }{
		{"list_entities", `{"device_id":null}`, "invalid_device_id", "device_id must not be null"},
		{"list_commands", `{"entity_id":null}`, "invalid_entity_id", "entity_id must not be null"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			t.Parallel()
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
			// Any service call panics; null cannot become an omitted filter.
			Register(server, &stubDevices{})
			result := callWire(t, server.HTTPHandler(), test.tool, test.arguments)
			var failure struct {
				Code    string `json:"failure_code"`
				Message string `json:"message"`
			}
			if !result.IsError || len(result.Content) != 1 || string(result.Structured) != result.Content[0].Text ||
				json.Unmarshal(result.Structured, &failure) != nil || failure.Code != test.code ||
				failure.Message != test.message {
				t.Fatalf("rejection=%+v, want %q / %q", result, test.code, test.message)
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

func TestPageLimitExactIntegerSpellings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		raw  string
		want bool
	}{
		{"1.0", true}, {"1e0", true}, {"1.0000000000000001", false},
	} {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()
			calls := 0
			service := &stubDevices{listEntities: func(
				_ context.Context, params devices.ListEntitiesParams,
			) (devices.Page[devices.EntityWithState], error) {
				calls++
				if params.Limit != 1 {
					t.Fatalf("limit = %d, want 1", params.Limit)
				}
				return devices.Page[devices.EntityWithState]{}, nil
			}}
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
			Register(server, service)
			result := callWire(t, server.HTTPHandler(), "list_entities", `{"limit":`+test.raw+`}`)
			if test.want {
				assertWireJSON(t, result, `{"items":[]}`)
				if calls != 1 {
					t.Fatalf("service calls = %d, want 1", calls)
				}
			} else if !result.IsError || calls != 0 {
				t.Fatalf("result = %+v, calls = %d", result, calls)
			}
		})
	}
}
