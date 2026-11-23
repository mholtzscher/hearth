package scripted_test

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

func thermostatDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "hallway-thermostat", Name: "Hallway thermostat", Kind: "thermostat",
		Entities: []scripted.EntitySpec{{
			Key: "thermostat", Name: "Thermostat", Type: "hearth.thermostat/v1",
			Support: map[string]any{
				"state": map[string]any{
					"modes": []string{"off", "heat", "cool"}, "fan_modes": []string{"auto", "on"},
					"temperature_minimum": 10000, "temperature_maximum": 30000,
					"humidity_minimum": 20, "humidity_maximum": 80,
				},
				"operations": map[string]any{
					"set_target": map[string]any{}, "set_range": map[string]any{}, "set_mode": map[string]any{},
					"set_fan_mode": map[string]any{}, "set_humidity": map[string]any{},
				},
			},
			Initial: map[string]any{
				"mode": "heat", "activity": "idle", "current_temperature": 18500, "target_temperature": 19000,
				"current_humidity": 42,
			},
		}},
	}
}

func TestThermostatCommandAppliesState(t *testing.T) {
	t.Parallel()
	cases := []struct{ operation, parameters, want string }{
		{"set_target", `{"temperature":21000}`, `{"target_temperature":21000}`},
		{"set_range", `{"low":18000,"high":24000}`, `{"target_temperature_low":18000,"target_temperature_high":24000}`},
		{"set_mode", `{"mode":"cool"}`, `{"mode":"cool"}`},
		{"set_fan_mode", `{"fan_mode":"auto"}`, `{"fan_mode":"auto"}`},
		{"set_humidity", `{"humidity":55.5}`, `{"target_humidity":55.5}`},
	}
	for _, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			t.Parallel()
			session := &fakeSession{}
			runtime, err := scripted.New(session, []scripted.DeviceSpec{thermostatDevice()})
			if err != nil {
				t.Fatal(err)
			}
			attachOne(t, runtime, "hallway-thermostat", "thermostat", "ent_thermostat")
			responder := session.dispatch(
				context.Background(),
				runtime,
				"ent_thermostat",
				tc.operation,
				json.RawMessage(tc.parameters),
			)
			if !responder.accepted || len(session.linked) != 1 {
				t.Fatalf("response = %+v; linked = %+v", responder, session.linked)
			}
			want := map[string]any{"mode": "heat", "activity": "idle", "current_temperature": float64(18500),
				"target_temperature": float64(19000), "current_humidity": float64(42)}
			var changes map[string]any
			if decodeErr := json.Unmarshal([]byte(tc.want), &changes); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			maps.Copy(want, changes)
			var got map[string]any
			if decodeErr := json.Unmarshal(
				session.linked[0].Value,
				&got,
			); decodeErr != nil ||
				!reflect.DeepEqual(got, want) {
				t.Fatalf("linked state = %s, want %+v: %v", session.linked[0].Value, want, decodeErr)
			}
		})
	}
}

func TestThermostatCommandRejectsInvalidParametersAndUnsupportedOperations(t *testing.T) {
	t.Parallel()
	cases := []struct{ operation, parameters string }{
		{"set_target", `{"temperature":21000.5}`}, {"set_target", `{"temperature":31000}`},
		{"set_target", `{"temperature":21000,"extra":1}`}, {"set_target", `{}`},
		{"set_range", `{"low":25000,"high":18000}`}, {"set_range", `{"low":9000,"high":18000}`},
		{"set_mode", `{"mode":"dry"}`}, {"set_fan_mode", `{"fan_mode":"unsupported"}`},
		{"set_humidity", `{"humidity":81}`}, {"unknown", `{}`},
	}
	device := thermostatDevice()
	delete(device.Entities[0].Support.(map[string]any)["operations"].(map[string]any), "set_fan_mode")
	for _, tc := range cases {
		t.Run(tc.operation+tc.parameters, func(t *testing.T) {
			t.Parallel()
			session := &fakeSession{}
			runtime, err := scripted.New(session, []scripted.DeviceSpec{device})
			if err != nil {
				t.Fatal(err)
			}
			attachOne(t, runtime, "hallway-thermostat", "thermostat", "ent_thermostat")
			before := string(runtime.Snapshot()[0].Current)
			responder := session.dispatch(
				context.Background(),
				runtime,
				"ent_thermostat",
				tc.operation,
				json.RawMessage(tc.parameters),
			)
			if responder.accepted || responder.rejected == "" || len(session.linked) != 0 ||
				string(runtime.Snapshot()[0].Current) != before {
				t.Fatalf("invalid command mutated state or was accepted: %+v", responder)
			}
		})
	}
}

func TestThermostatCommandCanPublishUnchangedState(t *testing.T) {
	t.Parallel()
	device := thermostatDevice()
	device.Entities[0].Commands = map[string]scripted.CommandBehavior{
		"set_target": {ApplyParameters: new(false)},
	}
	session := &fakeSession{}
	runtime, err := scripted.New(session, []scripted.DeviceSpec{device})
	if err != nil {
		t.Fatal(err)
	}
	attachOne(t, runtime, "hallway-thermostat", "thermostat", "ent_thermostat")
	before := string(runtime.Snapshot()[0].Current)
	responder := session.dispatch(
		context.Background(),
		runtime,
		"ent_thermostat",
		"set_target",
		json.RawMessage(`{"temperature":21000}`),
	)
	if !responder.accepted || len(session.linked) != 1 || string(session.linked[0].Value) != before {
		t.Fatalf("unchanged command response = %+v, observations = %+v", responder, session.linked)
	}
}
