package mcpapi //nolint:testpackage // Exercise the registered MCP surface.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestMCPReadRepresentations(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(&devices.State{Value: []byte(`{"active":true,"x":1000,"y":9007199254740993}`)})
	view.Entity.TypeID = devices.EntityTypeColorxyV1
	view.Entity.Support = []byte(`{}`)
	stub := &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) { return view, nil },
		listEntities: func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
			return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}}, nil
		},
		getDevice: func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error) {
			return devices.DeviceAggregate{
				Entities: devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}},
			}, nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, stub, testPresenter())
	for _, tc := range []struct{ name, args string }{
		{"get_entity", `"entity_id":"` + string(apiEntityID) + `"`},
		{"get_device", `"device_id":"` + string(apiDeviceID) + `"`},
		{"list_entities", ``},
	} {
		for _, mode := range []struct{ suffix, value, unit string }{{"", `"x":0.1`, `"unit":"1"`}, {`,"units":"canonical"`, `"y":9007199254740993`, `"unit":"10*-4"`}} {
			args := `{` + tc.args + mode.suffix + `}`
			if tc.args == "" && mode.suffix != "" {
				args = `{` + mode.suffix[1:] + `}`
			}
			result := callWire(t, server.HTTPHandler(), tc.name, args)
			require.False(t, result.IsError, string(result.Structured))
			require.Contains(t, string(result.Structured), mode.value)
			require.Contains(t, string(result.Structured), mode.unit)
		}
	}
	invalid := callWire(
		t,
		server.HTTPHandler(),
		"get_entity",
		`{"entity_id":"`+string(apiEntityID)+`","units":"private_sentinel"}`,
	)
	require.True(t, invalid.IsError)
	require.NotContains(t, string(invalid.Structured), "private_sentinel")
	view.State = nil
	view.Entity.TypeID = devices.EntityTypeColortempV1
	view.Entity.Support = []byte(`{"state":{"minimum":153,"maximum":500}}`)
	result := callWire(t, server.HTTPHandler(), "get_entity", `{"entity_id":"`+string(apiEntityID)+`"}`)
	require.False(t, result.IsError, string(result.Structured))
	require.Contains(t, string(result.Structured), `"state":null`)
	require.Contains(t, string(result.Structured), `"range":{"minimum":153,"maximum":500}`)
}
