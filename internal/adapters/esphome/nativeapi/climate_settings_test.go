package nativeapi //nolint:testpackage // Tests the private protocol boundary independently of Adapter planning.

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

func TestClimateDiscoveryUsesFlagsAndLegacyCapabilities(t *testing.T) {
	t.Parallel()
	_, data, err := encode(&apipb.ListEntitiesClimateResponse{
		Key:          7,
		Name:         "HVAC",
		DeviceId:     2,
		FeatureFlags: 59,
		SupportedModes: []apipb.ClimateMode{
			apipb.ClimateMode_CLIMATE_MODE_OFF,
			apipb.ClimateMode_CLIMATE_MODE_HEAT_COOL,
		},
		SupportedCustomFanModes:     []string{"Automatic", "Speed 1 (Low)"},
		VisualMinTemperature:        10,
		VisualMaxTemperature:        32.22222,
		VisualTargetTemperatureStep: 0.5,
		VisualMinHumidity:           10,
		VisualMaxHumidity:           80,
	})
	require.NoError(t, err)
	entity, err := decodeEntity(46, data)
	require.NoError(t, err)
	require.Equal(t, EntityClimate, entity.Type)
	require.Equal(t, uint32(2), entity.DeviceID)
	require.Equal(t, "HVAC", entity.Name)
	require.Equal(t, &ClimateInfo{
		Modes: []string{"off", "heat_cool"}, CustomFanModes: []string{"Automatic", "Speed 1 (Low)"},
		TemperatureUnit: "Cel", CurrentTemperature: true, TwoPointTarget: true,
		CurrentHumidity: true, TargetHumidity: true, Activity: true,
		MinimumTemperature: 10, MaximumTemperature: 32.22222, TargetTemperatureStep: 0.5,
		MinimumHumidity: 10, MaximumHumidity: 80,
	}, entity.Climate)

	legacy := decodeClimateInfo(&apipb.ListEntitiesClimateResponse{
		SupportsCurrentTemperature: true, SupportsTwoPointTargetTemperature: true,
		SupportsCurrentHumidity: true, SupportsTargetHumidity: true, SupportsAction: true,
		TemperatureUnit:   apipb.TemperatureUnit_TEMPERATURE_UNIT_FAHRENHEIT,
		SupportedFanModes: []apipb.ClimateFanMode{apipb.ClimateFanMode_CLIMATE_FAN_AUTO},
	})
	require.True(t, legacy.CurrentTemperature && legacy.TwoPointTarget &&
		legacy.CurrentHumidity && legacy.TargetHumidity && legacy.Activity)
	require.False(t, legacy.RequiresTwoPoint)
	require.Equal(t, "[degF]", legacy.TemperatureUnit)
	require.Equal(t, []string{"auto"}, legacy.StandardFanModes)
	required := decodeClimateInfo(&apipb.ListEntitiesClimateResponse{
		FeatureFlags: 4, TemperatureUnit: apipb.TemperatureUnit_TEMPERATURE_UNIT_KELVIN,
	})
	require.True(t, required.TwoPointTarget && required.RequiresTwoPoint)
	require.Equal(t, "K", required.TemperatureUnit)
	unknown := decodeClimateInfo(&apipb.ListEntitiesClimateResponse{TemperatureUnit: apipb.TemperatureUnit(99)})
	require.Empty(t, unknown.TemperatureUnit)
}

func TestClimateSnapshotPreservesZeroAndMissingReadings(t *testing.T) {
	t.Parallel()
	_, data, err := encode(&apipb.ClimateStateResponse{
		Key: 7, DeviceId: 2, Mode: apipb.ClimateMode_CLIMATE_MODE_HEAT,
		Action: apipb.ClimateAction_CLIMATE_ACTION_IDLE, CurrentTemperature: 0,
		TargetTemperature: float32(math.NaN()), TargetTemperatureLow: -1.5,
		TargetTemperatureHigh: float32(math.Inf(1)), CurrentHumidity: 0, TargetHumidity: 59,
		CustomFanMode: "Automatic", MissingState: true,
	})
	require.NoError(t, err)
	update, ok, err := decodeState(idClimateState, data)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, EntityClimate, update.Type)
	require.Equal(t, uint32(2), update.DeviceID)
	require.True(t, update.Missing)
	require.Equal(t, &ClimateState{
		Mode: "heat", Activity: new("idle"), CurrentTemperature: new(0.0),
		TargetTemperatureLow: new(-1.5), CurrentHumidity: new(0.0), TargetHumidity: new(59.0),
		CustomFanMode: new("Automatic"),
	}, update.Climate)

	_, data, err = encode(&apipb.ClimateStateResponse{
		Mode: apipb.ClimateMode(99), Action: apipb.ClimateAction(99), FanMode: apipb.ClimateFanMode(99),
	})
	require.NoError(t, err)
	unknown, _, err := decodeState(idClimateState, data)
	require.NoError(t, err)
	require.Empty(t, unknown.Climate.Mode)
	require.Nil(t, unknown.Climate.Activity)
	require.Nil(t, unknown.Climate.StandardFanMode)
}

func TestClimateZeroFanValueCannotEstablishOn(t *testing.T) {
	t.Parallel()
	// An unset std::optional fan and an explicit ON both omit the proto3 zero enum.
	for _, message := range []*apipb.ClimateStateResponse{
		{Key: 7}, {Key: 7, FanMode: apipb.ClimateFanMode_CLIMATE_FAN_ON},
	} {
		_, data, err := encode(message)
		require.NoError(t, err)
		update, ok, err := decodeState(idClimateState, data)
		require.NoError(t, err)
		require.True(t, ok)
		require.Nil(t, update.Climate.StandardFanMode)
	}
}

func TestClimateCommandsChangeOnlyOneFamily(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command ClimateCommand
		want    *apipb.ClimateCommandRequest
	}{
		{"off", ClimateCommand{Mode: new("off")}, &apipb.ClimateCommandRequest{HasMode: true}},
		{"single-zero", ClimateCommand{TargetTemperature: new(0.0)},
			&apipb.ClimateCommandRequest{HasTargetTemperature: true}},
		{"range", ClimateCommand{TargetTemperatureLow: new(-1.5), TargetTemperatureHigh: new(0.0)},
			&apipb.ClimateCommandRequest{
				HasTargetTemperatureLow: true, TargetTemperatureLow: -1.5, HasTargetTemperatureHigh: true,
			}},
		{"standard-fan", ClimateCommand{StandardFanMode: new("auto")},
			&apipb.ClimateCommandRequest{HasFanMode: true, FanMode: apipb.ClimateFanMode_CLIMATE_FAN_AUTO}},
		{"custom-fan", ClimateCommand{CustomFanMode: new("Automatic")},
			&apipb.ClimateCommandRequest{HasCustomFanMode: true, CustomFanMode: "Automatic"}},
		{"humidity-zero", ClimateCommand{TargetHumidity: new(0.0)},
			&apipb.ClimateCommandRequest{HasTargetHumidity: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.command.Key, test.command.DeviceID = 7, 2
			test.want.Key, test.want.DeviceId = 7, 2
			message, err := commandMessage(test.command)
			require.NoError(t, err)
			require.True(t, proto.Equal(test.want, message), "got %v, want %v", message, test.want)
		})
	}
}

func TestClimateCommandRejectsAmbiguousAndInvalidInput(t *testing.T) {
	t.Parallel()
	for _, command := range []ClimateCommand{
		{},
		{Mode: new("heat"), TargetHumidity: new(50.0)},
		{Mode: new("not-a-mode")},
		{StandardFanMode: new("unknown")},
		{StandardFanMode: new("auto"), CustomFanMode: new("Automatic")},
		{CustomFanMode: new("Low")},
		{TargetTemperatureLow: new(1.0)},
		{TargetTemperatureLow: new(2.0), TargetTemperatureHigh: new(1.0)},
		{TargetTemperature: new(math.NaN())},
		{TargetHumidity: new(math.Inf(1))},
	} {
		_, err := commandMessage(command)
		require.Error(t, err)
	}
}

func TestNumberAndSelectProtocolMapping(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		message proto.Message
		want    EntityInfo
	}{
		{&apipb.ListEntitiesNumberResponse{
			Key: 3, DeviceId: 2, Name: "Offset", MinValue: -9.5, MaxValue: 9.5, Step: 0.5,
			UnitOfMeasurement: "%", EntityCategory: apipb.EntityCategory_ENTITY_CATEGORY_CONFIG,
		}, EntityInfo{
			Type: EntityNumber, UpstreamType: "number", Key: 3, DeviceID: 2, Name: "Offset",
			Category: CategoryConfig, Unit: "%", Number: &NumberInfo{Minimum: -9.5, Maximum: 9.5, Step: 0.5},
		}},
		{&apipb.ListEntitiesSelectResponse{Key: 4, Name: "Cycle", Options: []string{"Continuous", "Timed, 10 min"}},
			EntityInfo{Type: EntitySelect, UpstreamType: "select", Key: 4, Name: "Cycle",
				Options: []string{"Continuous", "Timed, 10 min"}}},
	} {
		id, data, err := encode(test.message)
		require.NoError(t, err)
		got, err := decodeEntity(id, data)
		require.NoError(t, err)
		require.Equal(t, test.want, got)
	}
	for _, test := range []struct {
		message proto.Message
		want    StateUpdate
	}{
		{&apipb.NumberStateResponse{Key: 3, DeviceId: 2, State: 0.1},
			StateUpdate{Type: EntityNumber, Key: 3, DeviceID: 2, Number: 0.1}},
		{&apipb.NumberStateResponse{Key: 3, MissingState: true},
			StateUpdate{Type: EntityNumber, Key: 3, Missing: true}},
		{&apipb.SelectStateResponse{Key: 4, DeviceId: 2, State: "Continuous"},
			StateUpdate{Type: EntitySelect, Key: 4, DeviceID: 2, Text: "Continuous"}},
		{&apipb.SelectStateResponse{Key: 4, MissingState: true},
			StateUpdate{Type: EntitySelect, Key: 4, Missing: true}},
	} {
		id, data, err := encode(test.message)
		require.NoError(t, err)
		got, ok, err := decodeState(id, data)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, test.want, got)
	}
	number, err := commandMessage(NumberCommand{Key: 3, DeviceID: 2, Value: 0.1})
	require.NoError(t, err)
	require.True(t, proto.Equal(&apipb.NumberCommandRequest{Key: 3, DeviceId: 2, State: 0.1}, number))
	selection, err := commandMessage(SelectCommand{Key: 4, DeviceID: 2, Value: "Continuous"})
	require.NoError(t, err)
	require.True(t, proto.Equal(&apipb.SelectCommandRequest{Key: 4, DeviceId: 2, State: "Continuous"}, selection))
	_, err = commandMessage(NumberCommand{Value: math.NaN()})
	require.Error(t, err)
}
