package mcpapi //nolint:testpackage // Tests independent history projections through registered tools.

import (
	"context"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestStateHistoryOmitsRejectedValuesButKeepsAcceptedNullAndExactJSON(t *testing.T) {
	t.Parallel()
	rejected := apiStateHistoryEntry(3)
	rejected.Disposition = devices.DispositionRejected
	reason := devices.RejectionEntityDisabled
	rejected.Rejection = &reason
	// Never decode or expose a rejected raw value, even when malformed.
	rejected.Value = devices.Value(`private rejected bytes`)
	accepted := apiStateHistoryEntry(2)
	accepted.Value = devices.Value(`null`)
	exact := apiStateHistoryEntry(1)
	exact.Disposition = devices.DispositionUnchanged
	exact.Value = devices.Value(`{"large":9007199254740993,"huge":1e10000,"tiny":1e-10000,"nullable":null}`)
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	Register(
		server,
		&stubDevices{
			listEntityStateHistory: func(_ context.Context, params devices.ListEntityStateHistoryParams) (devices.Page[devices.EntityStateHistoryEntry], error) {
				if params.Filter != devices.EntityStateHistoryFilterAll {
					t.Errorf("filter = %s", params.Filter)
				}
				return devices.Page[devices.EntityStateHistoryEntry]{
					Items: []devices.EntityStateHistoryEntry{rejected, accepted, exact},
				}, nil
			},
		},
	)
	const prefix = `"observation_id":"` + string(apiObservationID) + `","adapter_received_at":"2026-08-25T10:00:00.123456789Z","source_updated_at":"2026-08-25T09:59:00.123456789Z","observed_at":"2026-08-25T10:00:01.123456789Z"`
	want := `{"items":[{` + prefix + `,"disposition":"rejected","rejection_code":"entity_disabled"},{` + prefix + `,"disposition":"applied","value":null},{` + prefix + `,"disposition":"unchanged","value":{"large":9007199254740993,"huge":1e10000,"tiny":1e-10000,"nullable":null}}]}`
	assertWireJSON(
		t,
		callWire(
			t,
			server.HTTPHandler(),
			"list_entity_state_history",
			`{"entity_id":"`+string(apiEntityID)+`","disposition":"all"}`,
		),
		want,
	)
}

func TestReadSuccessSchemasRejectInvalidRawDocuments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, tool, args string
		stub             *stubDevices
	}{
		{"support is not an object", "get_entity", `{"entity_id":"` + string(apiEntityID) + `"}`, &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			view := apiEntityWithState(nil)
			view.Entity.Support = devices.EntitySupport(`[]`)
			return view, nil
		}}},
		{"parameters are not an object", "get_command", `{"command_id":"` + string(apiCommandID) + `"}`, &stubDevices{getCommand: func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
			command := listableCommand()
			command.Parameters = devices.CommandParameters(`[]`)
			return command, nil
		}}},
		{"accepted history lacks value", "list_entity_state_history", `{"entity_id":"` + string(apiEntityID) + `"}`, &stubDevices{listEntityStateHistory: func(context.Context, devices.ListEntityStateHistoryParams) (devices.Page[devices.EntityStateHistoryEntry], error) {
			entry := apiStateHistoryEntry(1)
			entry.Value = nil
			return devices.Page[devices.EntityStateHistoryEntry]{Items: []devices.EntityStateHistoryEntry{entry}}, nil
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
			Register(server, test.stub)
			assertInternalWireFailure(t, callWire(t, server.HTTPHandler(), test.tool, test.args))
		})
	}
}
