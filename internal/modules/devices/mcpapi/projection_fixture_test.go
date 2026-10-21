package mcpapi //nolint:testpackage // Plain recording fixtures for the registered result contracts.
import (
	"context"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func projectionEntityFixture(
	t *testing.T,
	stub *stubDevices,
	calls map[string]int,
	view devices.EntityWithState,
	opaque string,
) {
	t.Helper()
	obs := apiObservationID

	stub.getEntity = func(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
		calls["get_entity"]++
		if id != apiEntityID {
			t.Errorf("ID = %s", id)
		}
		return view, nil
	}
	stub.listEntities = func(_ context.Context, p devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
		calls["list_entities"]++
		if p.Limit != 50 || p.DeviceID != nil || p.AfterID != nil {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}}, nil
	}
	stub.setEntityEnabled = func(_ context.Context, id devices.EntityID, enabled bool) (devices.EntityWithState, error) {
		calls["update_entity"]++
		if id != apiEntityID || enabled {
			t.Errorf("update = %s %v", id, enabled)
		}
		// Deliberately differs from input: receipts describe the returned record.
		return view, nil
	}
	stub.executeCommand = func(_ context.Context, in devices.CommandInput) (devices.CommandResult, error) {
		calls["execute_entity_command"]++
		if in.EntityID != apiEntityID || in.OperationName != "set" || string(in.Parameters) != opaque ||
			in.ID != "" ||
			in.CorrelationID != "" {
			t.Errorf("command input = %+v", in)
		}
		value := devices.Value(`null`)
		return devices.CommandResult{
			CommandID:     apiCommandID,
			Outcome:       devices.OutcomeObserved,
			ObservationID: &obs,
			Value:         &value,
		}, nil
	}
}

func projectionReadFixture(
	t *testing.T,
	stub *stubDevices,
	calls map[string]int,
	view devices.EntityWithState,
	device devices.Device,
	command devices.CommandRecord,
) {
	t.Helper()

	stub.getDevice = func(_ context.Context, p devices.GetDeviceParams) (devices.DeviceAggregate, error) {
		calls["get_device"]++
		if p.ID != apiDeviceID || p.EntityLimit != 50 || p.AfterEntityID != nil {
			t.Errorf("params = %+v", p)
		}
		return devices.DeviceAggregate{
			Device:   device,
			Entities: devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}},
		}, nil
	}
	stub.listDevices = func(_ context.Context, p devices.ListDevicesParams) (devices.Page[devices.Device], error) {
		calls["list_devices"]++
		if p.Limit != 50 || p.AfterID != nil {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.Device]{Items: []devices.Device{device}}, nil
	}
	stub.getCommand = func(_ context.Context, id devices.CommandID) (devices.CommandRecord, error) {
		calls["get_command"]++
		if id != apiCommandID {
			t.Errorf("ID = %s", id)
		}
		return command, nil
	}
	stub.listCommands = func(_ context.Context, p devices.ListCommandsParams) (devices.Page[devices.CommandRecord], error) {
		calls["list_commands"]++
		if p.Limit != 50 || p.EntityID != nil || p.Status != nil {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{command}}, nil
	}
	stub.listEntityCommands = func(_ context.Context, p devices.ListEntityCommandsParams) (devices.Page[devices.CommandRecord], error) {
		calls["list_entity_commands"]++
		if p.EntityID != apiEntityID || p.Limit != 50 {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.CommandRecord]{Items: []devices.CommandRecord{command}}, nil
	}
}
func projectionHistoryFixture(t *testing.T, stub *stubDevices, calls map[string]int, now time.Time) {
	t.Helper()
	obs := apiObservationID

	stub.listEntityAvailabilityHistory = func(_ context.Context, p devices.ListEntityAvailabilityParams) (devices.Page[devices.HealthTransition], error) {
		calls["list_entity_availability_history"]++
		if p.EntityID != apiEntityID || p.Limit != 50 {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.HealthTransition]{
			Items: []devices.HealthTransition{
				{
					Status:           "unavailable",
					Source:           "adapter_health",
					ObservedAt:       now,
					SourceObservedAt: &now,
					Reason:           &devices.HealthReason{Code: "hearth.external_system_unavailable"},
				},
			},
		}, nil
	}
	stub.listEntityStateHistory = func(_ context.Context, p devices.ListEntityStateHistoryParams) (devices.Page[devices.EntityStateHistoryEntry], error) {
		calls["list_entity_state_history"]++
		if p.EntityID != apiEntityID || p.Limit != 50 || p.Filter != devices.EntityStateHistoryFilterUpdates {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.EntityStateHistoryEntry]{
			Items: []devices.EntityStateHistoryEntry{
				{
					ObservationID:     obs,
					Disposition:       devices.DispositionApplied,
					ObservedAt:        now,
					AdapterReceivedAt: now,
					Value:             devices.Value(`null`),
				},
			},
		}, nil
	}
	stub.listEntityEvents = func(_ context.Context, p devices.ListEntityEventsParams) (devices.Page[devices.EntityEventHistoryEntry], error) {
		calls["list_entity_events"]++
		if p.EntityID != apiEntityID || p.Limit != 50 {
			t.Errorf("params = %+v", p)
		}
		return devices.Page[devices.EntityEventHistoryEntry]{
			Items: []devices.EntityEventHistoryEntry{
				{
					EventID:     apiEntityEventID,
					EntityID:    apiEntityID,
					Name:        "single_press",
					Disposition: devices.EntityEventDispositionAccepted,
					EmittedAt:   now,
					ReceivedAt:  now,
					RecordedAt:  now,
				},
			},
		}, nil
	}
}
