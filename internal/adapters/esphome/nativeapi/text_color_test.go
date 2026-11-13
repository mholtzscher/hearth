package nativeapi //nolint:testpackage // Exercises the wire boundary and presence flags.

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

func TestDecodeTextAndColorState(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		message proto.Message
		want    StateUpdate
	}{
		{&apipb.TextSensorStateResponse{Key: 9, State: "Improved", DeviceId: 2},
			StateUpdate{Type: EntityTextSensor, Key: 9, DeviceID: 2, Text: "Improved"}},
		{&apipb.TextSensorStateResponse{Key: 9, MissingState: true},
			StateUpdate{Type: EntityTextSensor, Key: 9, Missing: true}},
		{&apipb.TextSensorStateResponse{Key: 9}, StateUpdate{Type: EntityTextSensor, Key: 9}},
		{&apipb.LightStateResponse{Key: 3, State: false, Brightness: 0.2,
			ColorMode: apipb.ColorMode_COLOR_MODE_RGB, ColorBrightness: 0.5, Red: 1, Green: 0.5, Blue: 0, Effect: "Slow Pulse"},
			StateUpdate{Type: EntityLight, Key: 3, Brightness: 0.2, ColorMode: 35,
				RGB: RGB{Red: 1, Green: 0.5}, ColorBrightness: 0.5, Effect: "Slow Pulse"}},
	} {
		id, data, err := encode(test.message)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := decodeState(id, data)
		if err != nil || !ok || got != test.want {
			t.Errorf("%T: %+v, ok=%t, err=%v; want %+v", test.message, got, ok, err, test.want)
		}
	}
}

func TestColorAndEffectCommandsPreservePowerAndBrightness(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		command LightCommand
		want    *apipb.LightCommandRequest
	}{
		{LightCommand{Key: 3, DeviceID: 2, RGB: &RGB{Red: 1, Green: 0.5}, ColorBrightness: new(0.5)},
			&apipb.LightCommandRequest{Key: 3, DeviceId: 2, HasRgb: true, Red: 1, Green: 0.5,
				HasColorMode: true, ColorMode: apipb.ColorMode_COLOR_MODE_RGB, HasColorBrightness: true, ColorBrightness: 0.5}},
		{LightCommand{Key: 3, Effect: new("Slow Pulse")},
			&apipb.LightCommandRequest{Key: 3, HasEffect: true, Effect: "Slow Pulse"}},
		{LightCommand{Key: 3, Effect: new("None")},
			&apipb.LightCommandRequest{Key: 3, HasEffect: true, Effect: "None"}},
	} {
		got, err := commandMessage(test.command)
		if err != nil || !proto.Equal(got, test.want) {
			t.Errorf("command = %v, %v; want %v", got, err, test.want)
		}
	}
}

func TestDecodeRejectsNegativeLightColorMode(t *testing.T) {
	t.Parallel()
	id, data, err := encode(&apipb.LightStateResponse{Key: 3, ColorMode: apipb.ColorMode(-1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, decodeErr := decodeState(id, data); decodeErr == nil || ok {
		t.Fatalf("negative color mode decoded: ok=%t, err=%v", ok, decodeErr)
	}
}
