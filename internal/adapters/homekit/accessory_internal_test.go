package homekit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHAPWriteUsesCanonicalStateAndReturnsLocalRejections(t *testing.T) {
	t.Parallel()
	const id = "ent_01900000-0000-7000-8000-000000000001"
	entity := Entity{ID: id, DeviceID: "dev_01900000-0000-7000-8000-000000000001", Type: "hearth.power/v1",
		Enabled: true, Availability: "available", State: &EntityState{Value: json.RawMessage("true")},
		Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`)}
	exposure := Exposure{
		DeviceID: entity.DeviceID, Kind: KindLightbulb, Name: "Test light", AID: 2,
		Entities: []RoleAssignment{{Role: "on", EntityID: id}}, Slots: map[string]uint16{"on/" + id: 1},
	}
	var writes []writeIntent
	b, err := buildAccessory(exposure, Inventory{Entities: map[string]Entity{id: entity}}, "celsius",
		func(string) Entity { return entity }, func(_ uint64, intent writeIntent) int {
			writes = append(writes, intent)
			return 0
		})
	require.NoError(t, err)
	require.Len(t, b.accessory.Ss, 2)
	require.Equal(t, service.TypeLightbulb, b.accessory.Ss[1].Type)
	on := b.accessory.Ss[1].C(characteristic.TypeOn)
	require.NotNil(t, on)
	request := httptest.NewRequest(http.MethodPut, "/characteristics", http.NoBody)
	_, status := on.SetValueRequest(true, request)
	assert.Zero(t, status)
	_, status = on.SetValueRequest(false, request)
	assert.Zero(t, status)
	require.Len(t, writes, 2, "same-value writes are distinct requests")
	assert.Equal(t, map[string]any{"value": false}, writes[1].Parameters)
	value, status := on.ValueRequest(request)
	assert.Zero(t, status)
	assert.Equal(t, true, value, "admission does not replace observed State")
	assert.Equal(t, true, on.Value(), "no requested value is published to event subscribers")
	_, status = on.SetValueRequest("false", request)
	assert.Equal(t, hapInvalidValue, status)
	entity.Availability = "unavailable"
	_, status = on.SetValueRequest(false, request)
	assert.Equal(t, hapCommunicationFailure, status)
	_, status = on.ValueRequest(request)
	assert.Equal(t, hapCommunicationFailure, status)
	assert.Len(t, writes, 2)
}

func TestHAPCoverTargetRollsBackToObservedPositionOnCoreTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const id = "ent_01900000-0000-7000-8000-000000000001"
		entity := Entity{ID: id, DeviceID: "dev_01900000-0000-7000-8000-000000000001", Type: "hearth.cover/v1",
			Enabled: true, Availability: "available",
			State:   &EntityState{Value: json.RawMessage(`{"position":15,"motion":"opening"}`)},
			Support: json.RawMessage(`{"state":{},"operations":{"set":{},"stop":{}}}`)}
		exposure := Exposure{
			DeviceID: entity.DeviceID, Kind: KindWindowCovering, Name: "Cover", AID: 2,
			Entities: []RoleAssignment{{Role: "position", EntityID: id}}, Slots: map[string]uint16{"position/" + id: 1},
		}
		commands := make(chan executedWrite)
		release := make(chan struct{})
		core := heldTimeoutExecutor{commands: commands, release: release}
		lookup := func(string) Entity { return entity }
		runner := newCommandRunner(t.Context(), core, lookup, slog.New(slog.DiscardHandler))
		defer runner.close()
		built, err := buildAccessory(exposure, Inventory{Entities: map[string]Entity{id: entity}}, "celsius",
			lookup, runner.submit)
		require.NoError(t, err)
		cover := built.accessory.Ss[1]
		require.Equal(t, service.TypeWindowCovering, cover.Type)
		target := cover.C(characteristic.TypeTargetPosition)
		current := cover.C(characteristic.TypeCurrentPosition)
		motion := cover.C(characteristic.TypePositionState)
		events := make(chan any, 3)
		target.OnCValueUpdate(func(_ *characteristic.C, next, _ any, _ *http.Request) { events <- next })
		request := httptest.NewRequest(http.MethodPut, "/characteristics", http.NoBody)
		_, status := target.SetValueRequest(float64(70), request)
		require.Zero(t, status)
		command := <-commands
		assert.Equal(t, executedWrite{id, "set", map[string]any{"position": int64(70)}}, command)
		assertHAPValue(t, current, request, 15)
		assertHAPValue(t, motion, request, characteristic.PositionStateIncreasing)
		assertHAPValue(t, target, request, float64(70))
		entity.State = &EntityState{Value: json.RawMessage(`{"position":30,"motion":"opening"}`)}
		for _, binding := range built.bindings {
			binding.update(entity)
		}
		assertHAPValue(t, current, request, 30)
		assertHAPValue(t, target, request, float64(70))
		assert.Empty(t, events, "an intermediate Observation must not publish a different target")
		close(release)
		synctest.Wait()
		assertHAPValue(t, target, request, 30)
		require.Len(t, events, 1)
		assert.Equal(t, 30, <-events)
	})
}

func TestHAPPurifierWithOnlyActiveRoleHasReadableNonWritableMode(t *testing.T) {
	t.Parallel()
	const id = "fixture-power"
	entity := Entity{ID: id, DeviceID: "fixture-purifier", Type: "hearth.power/v1",
		Enabled: true, Availability: "available", State: &EntityState{Value: json.RawMessage("true")}}
	exposure := Exposure{
		DeviceID: entity.DeviceID, Kind: KindAirPurifier, Name: "Purifier", AID: 2,
		Entities: []RoleAssignment{{Role: "active", EntityID: id}}, Slots: map[string]uint16{"active/" + id: 1},
	}
	built, err := buildAccessory(exposure, Inventory{Entities: map[string]Entity{id: entity}}, "celsius",
		func(string) Entity { return entity }, func(uint64, writeIntent) int {
			t.Fatal("unmapped purifier mode must not issue a Command")
			return 0
		})
	require.NoError(t, err)
	mode := built.accessory.Ss[1].C(characteristic.TypeTargetAirPurifierState)
	require.NotNil(t, mode)
	read := httptest.NewRequest(http.MethodGet, "/characteristics", http.NoBody)
	assertHAPValue(t, mode, read, characteristic.TargetAirPurifierStateManual)
	assert.False(t, mode.IsWritable())
	entity.Availability = "unavailable"
	_, status := mode.ValueRequest(read)
	assert.Equal(t, hapCommunicationFailure, status)
}

type heldTimeoutExecutor struct {
	commands chan executedWrite
	release  chan struct{}
}

func (exec heldTimeoutExecutor) ExecuteCommand(ctx context.Context, id, operation string, parameters any) error {
	select {
	case exec.commands <- executedWrite{id, operation, parameters}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-exec.release:
		return &CommandError{HTTPStatus: http.StatusGatewayTimeout}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func assertHAPValue(t *testing.T, c *characteristic.C, request *http.Request, expected any) {
	t.Helper()
	value, status := c.ValueRequest(request)
	require.Zero(t, status)
	assert.Equal(t, expected, value)
}

func TestHAPLockCurrentAndTargetStayDistinct(t *testing.T) {
	t.Parallel()
	const id = "ent_01900000-0000-7000-8000-000000000001"
	entity := Entity{ID: id, DeviceID: "dev_01900000-0000-7000-8000-000000000001", Type: "hearth.lock/v1",
		Enabled: true, Availability: "available", State: &EntityState{},
		Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`)}
	exposure := Exposure{
		DeviceID: entity.DeviceID, Kind: KindLock, Name: "Lock", AID: 2,
		Entities: []RoleAssignment{{Role: "lock", EntityID: id}}, Slots: map[string]uint16{"lock/" + id: 1},
	}
	entity.State.Value = json.RawMessage(`"unlocked"`)
	built, err := buildAccessory(exposure, Inventory{Entities: map[string]Entity{id: entity}}, "celsius",
		func(string) Entity { return entity },
		func(uint64, writeIntent) int { return 0 })
	require.NoError(t, err)
	lock := built.accessory.Ss[1]
	request := httptest.NewRequest(http.MethodGet, "/characteristics", http.NoBody)
	current, target := lock.C(characteristic.TypeLockCurrentState), lock.C(characteristic.TypeLockTargetState)
	for _, row := range []struct {
		state           string
		current, target int
	}{
		{`"unlocked"`, 0, 0}, {`"locking"`, 0, 1}, {`"locked"`, 1, 1}, {`"jammed"`, 2, 1}, {`"unknown"`, 3, 1},
	} {
		entity.State.Value = json.RawMessage(row.state)
		assertHAPValue(t, current, request, row.current)
		assertHAPValue(t, target, request, row.target)
	}
	assert.True(t, target.RequiresTimedWrite())
}

func TestHAPThermostatSelectsOnlySupportedModesAndUsesCelsius(t *testing.T) {
	t.Parallel()
	const id = "fixture-thermostat"
	entity := Entity{ID: id, DeviceID: "fixture-device", Type: "hearth.thermostat/v1",
		Enabled: true, Availability: "available",
		State: &EntityState{Value: json.RawMessage(`{"mode":"heat","activity":"heating",
			"current_temperature":21500,"target_temperature":22000}`)},
		Support: json.RawMessage(`{"state":{"modes":["off","heat","heat_cool","auto"],"temperature_minimum":16000,
			"temperature_maximum":32000},"operations":{"set_mode":{},"set_target":{}}}`)}
	exposure := Exposure{
		DeviceID: entity.DeviceID, Kind: KindThermostat, Name: "Thermostat", AID: 2,
		Entities: []RoleAssignment{{Role: "thermostat", EntityID: id}}, Slots: map[string]uint16{"thermostat/" + id: 1},
	}
	var writes []writeIntent
	b, err := buildAccessory(exposure, Inventory{Entities: map[string]Entity{id: entity}}, "fahrenheit",
		func(string) Entity { return entity }, func(_ uint64, intent writeIntent) int {
			writes = append(writes, intent)
			return 0
		})
	require.NoError(t, err)
	thermostat := b.accessory.Ss[1]
	req := httptest.NewRequest(http.MethodPut, "/characteristics", http.NoBody)
	assertHAPValue(t, thermostat.C(characteristic.TypeCurrentTemperature), req, 21.5)
	assertHAPValue(t, thermostat.C(characteristic.TypeTemperatureDisplayUnits), req, 1)
	assert.Nil(t, thermostat.C(characteristic.TypeCurrentRelativeHumidity), "no humidity evidence")
	mode := thermostat.C(characteristic.TypeTargetHeatingCoolingState)
	assert.Equal(t, []int{0, 1, 3}, mode.ValidVals)
	_, status := mode.SetValueRequest(float64(2), req)
	assert.Equal(t, hapInvalidValue, status)
	_, status = mode.SetValueRequest(float64(3), req)
	assert.Zero(t, status)
	_, status = thermostat.C(characteristic.TypeTargetTemperature).SetValueRequest(21.5, req)
	assert.Zero(t, status)
	require.Len(t, writes, 2)
	assert.Equal(t, "set_mode", writes[0].Operation)
	assert.Equal(t, map[string]any{"mode": "heat_cool"}, writes[0].Parameters)
	assert.Equal(t, "set_target", writes[1].Operation)
	assert.Equal(t, map[string]any{"temperature": int64(21500)}, writes[1].Parameters)
}
