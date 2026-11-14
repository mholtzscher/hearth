package esphome //nolint:testpackage // Exercises thermostat dispatch through the owning worker.

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// thermostatRuntimeDevice is synthetic; it does not represent a captured device.
func thermostatRuntimeDevice(paired bool) nativeapitest.Device {
	device := testDevice("02:00:00:00:00:01")
	device.Entities = []proto.Message{&apipb.ListEntitiesClimateResponse{
		Key: 7, Name: "HVAC",
		SupportsCurrentTemperature: true, SupportsTwoPointTargetTemperature: paired,
		SupportsCurrentHumidity: true, SupportsTargetHumidity: true,
		SupportedModes: []apipb.ClimateMode{
			apipb.ClimateMode_CLIMATE_MODE_OFF, apipb.ClimateMode_CLIMATE_MODE_HEAT,
		},
		SupportedCustomFanModes: []string{"Automatic", "Speed 1 (Low)"},
		VisualMinTemperature:    10, VisualMaxTemperature: 30,
		VisualMinHumidity: 10, VisualMaxHumidity: 80,
	}}
	device.States = []proto.Message{thermostatRuntimeState()}
	return device
}

func thermostatRuntimeState() *apipb.ClimateStateResponse {
	return &apipb.ClimateStateResponse{
		Key: 7, Mode: apipb.ClimateMode_CLIMATE_MODE_OFF,
		CurrentTemperature: 20, TargetTemperature: 19, TargetTemperatureLow: 18,
		TargetTemperatureHigh: 24, CurrentHumidity: 45, TargetHumidity: 40, CustomFanMode: "Automatic",
	}
}

func thermostatRuntimeCommand(
	instance *Adapter, session *fakeSession, entityID, id, operation, parameters string, deadline time.Duration,
) (*fakeResponder, <-chan error) {
	responder := &fakeResponder{session: session, command: id}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()
		done <- instance.HandleCommand(ctx, adapter.Command{
			ID: id, EntityID: entityID, OperationName: operation, Parameters: []byte(parameters),
		}, responder)
	}()
	synctest.Wait()
	return responder, done
}

func thermostatRuntimeFrame(t *testing.T, peer *nativeapitest.Session) *apipb.ClimateCommandRequest {
	t.Helper()
	select {
	case frame := <-peer.Received():
		request, ok := frame.Message.(*apipb.ClimateCommandRequest)
		require.True(t, ok, "unexpected frame: %v", frame)
		require.Equal(t, uint32(7), request.GetKey())
		return request
	default:
		t.Fatal("expected climate command frame")
		return nil
	}
}

func thermostatRuntimePush(t *testing.T, peer *nativeapitest.Session, state *apipb.ClimateStateResponse) {
	t.Helper()
	require.NoError(t, peer.Push(state))
	synctest.Wait()
}

func thermostatRuntimeLinked(t *testing.T, session *fakeSession, index int, id, entityID, value string) call {
	t.Helper()
	linked := callsOfKind(session.snapshot(), "linked")
	require.Len(t, linked, index+1)
	require.Equal(t, id, linked[index].linkedTo)
	require.Equal(t, entityID, linked[index].observation.EntityID)
	require.NotEmpty(t, linked[index].observation.AdapterReceivedAt)
	require.JSONEq(t, value, string(linked[index].observation.Value))
	return linked[index]
}

func TestThermostatRuntimeSharedFIFOAndExactEvidence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = thermostatRuntimeDevice(true)
		instance, stop := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		entity := entityIDFor(t, session, "02:00:00:00:00:01/0/climate/HVAC")

		rangeResponse, rangeDone := thermostatRuntimeCommand(instance, session, entity, "range", "set_range",
			`{"low":19000,"high":23000}`, time.Minute)
		require.NoError(t, <-rangeDone)
		require.Equal(t, "accepted", rangeResponse.result())
		rangeFrame := thermostatRuntimeFrame(t, peer)
		require.True(t, rangeFrame.GetHasTargetTemperatureLow() && rangeFrame.GetHasTargetTemperatureHigh())

		modeResponse, modeDone := thermostatRuntimeCommand(instance, session, entity, "mode", "set_mode",
			`{"mode":"heat"}`, time.Minute)
		fanResponse, fanDone := thermostatRuntimeCommand(instance, session, entity, "fan", "set_fan_mode",
			`{"fan_mode":"Speed 1 (Low)"}`, time.Minute)
		humidityResponse, humidityDone := thermostatRuntimeCommand(
			instance,
			session,
			entity,
			"humidity",
			"set_humidity",
			`{"humidity":55}`,
			time.Minute,
		)
		require.Empty(t, modeResponse.result())
		require.Empty(t, fanResponse.result())
		require.Empty(t, humidityResponse.result())
		assertNoEffectFrame(t, peer)

		state := thermostatRuntimeState()
		state.Mode = apipb.ClimateMode_CLIMATE_MODE_HEAT
		state.TargetTemperatureLow = 19 // Mode matches the queued command, but the active range is incomplete.
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			0,
			"range",
			entity,
			`{"mode":"heat","current_temperature":20000,"target_temperature_low":19000,"target_temperature_high":24000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		require.Empty(t, modeResponse.result())
		assertNoEffectFrame(t, peer)

		state.Mode = apipb.ClimateMode_CLIMATE_MODE_OFF
		state.TargetTemperatureHigh = 23
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			1,
			"range",
			entity,
			`{"mode":"off","current_temperature":20000,"target_temperature_low":19000,"target_temperature_high":23000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		require.NoError(t, <-modeDone)
		require.Equal(t, "accepted", modeResponse.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasMode())
		assertNoEffectFrame(t, peer)

		state.Mode = apipb.ClimateMode_CLIMATE_MODE_HEAT
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			2,
			"mode",
			entity,
			`{"mode":"heat","current_temperature":20000,"target_temperature_low":19000,"target_temperature_high":23000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		require.NoError(t, <-fanDone)
		require.Equal(t, "accepted", fanResponse.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasCustomFanMode())
		assertNoEffectFrame(t, peer)

		state.CustomFanMode = "Speed 1 (Low)"
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			3,
			"fan",
			entity,
			`{"mode":"heat","current_temperature":20000,"target_temperature_low":19000,"target_temperature_high":23000,"fan_mode":"Speed 1 (Low)","current_humidity":45,"target_humidity":40}`,
		)
		require.NoError(t, <-humidityDone)
		require.Equal(t, "accepted", humidityResponse.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasTargetHumidity())
		assertNoEffectFrame(t, peer)

		state.CurrentHumidity = 55 // A measurement is not evidence of the target setting.
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			4,
			"humidity",
			entity,
			`{"mode":"heat","current_temperature":20000,"target_temperature_low":19000,"target_temperature_high":23000,"fan_mode":"Speed 1 (Low)","current_humidity":55,"target_humidity":40}`,
		)
		state.TargetHumidity = 55
		thermostatRuntimePush(t, peer, state)
		matched := thermostatRuntimeLinked(
			t,
			session,
			5,
			"humidity",
			entity,
			`{"mode":"heat","current_temperature":20000,"target_temperature_low":19000,"target_temperature_high":23000,"fan_mode":"Speed 1 (Low)","current_humidity":55,"target_humidity":55}`,
		)

		time.Sleep(time.Second)
		cachedResponse, cachedDone := thermostatRuntimeCommand(instance, session, entity, "unchanged", "set_humidity",
			`{"humidity":55}`, time.Minute)
		require.NoError(t, <-cachedDone)
		require.Equal(t, "accepted", cachedResponse.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasTargetHumidity())
		cached := thermostatRuntimeLinked(t, session, 6, "unchanged", entity, string(matched.observation.Value))
		require.Equal(t, matched.observation.AdapterReceivedAt, cached.observation.AdapterReceivedAt)
		require.NoError(t, stop())
	})
}

func TestThermostatRuntimeSingleTargetExpiryAdvancesFIFO(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = thermostatRuntimeDevice(false)
		instance, stop := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		entity := entityIDFor(t, session, "02:00:00:00:00:01/0/climate/HVAC")
		target, targetDone := thermostatRuntimeCommand(instance, session, entity, "target", "set_target",
			`{"temperature":22000}`, time.Second)
		require.NoError(t, <-targetDone)
		require.Equal(t, "accepted", target.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasTargetTemperature())
		mode, modeDone := thermostatRuntimeCommand(instance, session, entity, "mode", "set_mode",
			`{"mode":"heat"}`, time.Minute)
		state := thermostatRuntimeState()
		state.CurrentTemperature = 22
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			0,
			"target",
			entity,
			`{"mode":"off","current_temperature":22000,"target_temperature":19000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		require.Empty(t, mode.result())
		assertNoEffectFrame(t, peer)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		require.NoError(t, <-modeDone)
		require.Equal(t, "accepted", mode.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasMode())
		state.TargetTemperature = 22 // Late target report belongs to the current mode attempt, not the expired one.
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			1,
			"mode",
			entity,
			`{"mode":"off","current_temperature":22000,"target_temperature":22000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		state.Mode = apipb.ClimateMode_CLIMATE_MODE_HEAT
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			2,
			"mode",
			entity,
			`{"mode":"heat","current_temperature":22000,"target_temperature":22000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		next, nextDone := thermostatRuntimeCommand(instance, session, entity, "next_target", "set_target",
			`{"temperature":23000}`, time.Minute)
		require.NoError(t, <-nextDone)
		require.Equal(t, "accepted", next.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasTargetTemperature())
		state.TargetTemperature = 23
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			3,
			"next_target",
			entity,
			`{"mode":"heat","current_temperature":22000,"target_temperature":23000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		assertNoEffectFrame(t, peer)
		require.NoError(t, stop())
	})
}

func TestThermostatRuntimeReconnectDropsQueueAndPreviousCache(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = thermostatRuntimeDevice(true)
		instance, stop := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		entity := entityIDFor(t, session, "02:00:00:00:00:01/0/climate/HVAC")
		active, activeDone := thermostatRuntimeCommand(instance, session, entity, "old", "set_mode",
			`{"mode":"heat"}`, time.Minute)
		require.NoError(t, <-activeDone)
		require.Equal(t, "accepted", active.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasMode())
		queued, queuedDone := thermostatRuntimeCommand(instance, session, entity, "queued", "set_humidity",
			`{"humidity":55}`, time.Minute)
		require.Empty(t, queued.result())
		dialer.mu.Lock()
		device := dialer.devices["one"]
		device.States = nil
		dialer.devices["one"] = device
		dialer.mu.Unlock()
		peer.Close()
		synctest.Wait()
		require.NoError(t, <-queuedDone)
		require.Equal(t, "unavailable", queued.result())
		time.Sleep(time.Second)
		synctest.Wait()
		peer = dialer.latest(t, "one")
		assertNoEffectFrame(t, peer)

		// The old generation reported off, but cannot satisfy this new write.
		fresh, freshDone := thermostatRuntimeCommand(instance, session, entity, "fresh", "set_mode",
			`{"mode":"off"}`, time.Minute)
		require.NoError(t, <-freshDone)
		require.Equal(t, "accepted", fresh.result())
		require.True(t, thermostatRuntimeFrame(t, peer).GetHasMode())
		require.Empty(t, callsOfKind(session.snapshot(), "linked"))
		state := thermostatRuntimeState()
		state.Mode = apipb.ClimateMode_CLIMATE_MODE_HEAT
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			0,
			"fresh",
			entity,
			`{"mode":"heat","current_temperature":20000,"target_temperature_low":18000,"target_temperature_high":24000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		state.Mode = apipb.ClimateMode_CLIMATE_MODE_OFF
		thermostatRuntimePush(t, peer, state)
		thermostatRuntimeLinked(
			t,
			session,
			1,
			"fresh",
			entity,
			`{"mode":"off","current_temperature":20000,"target_temperature_low":18000,"target_temperature_high":24000,"fan_mode":"Automatic","current_humidity":45,"target_humidity":40}`,
		)
		assertNoEffectFrame(t, peer)
		require.NoError(t, stop())
	})
}
