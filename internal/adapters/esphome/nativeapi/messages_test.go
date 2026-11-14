package nativeapi //nolint:testpackage // These tests exercise unexported codec and transport limits.

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
)

func TestMessageIDsMatchSchema(t *testing.T) {
	t.Parallel()
	cases := map[uint16]proto.Message{
		idHelloRequest:             &apipb.HelloRequest{},
		idHelloResponse:            &apipb.HelloResponse{},
		idDisconnectRequest:        &apipb.DisconnectRequest{},
		idDisconnectResponse:       &apipb.DisconnectResponse{},
		idPingRequest:              &apipb.PingRequest{},
		idPingResponse:             &apipb.PingResponse{},
		idDeviceInfoRequest:        &apipb.DeviceInfoRequest{},
		idDeviceInfoResponse:       &apipb.DeviceInfoResponse{},
		idListEntitiesRequest:      &apipb.ListEntitiesRequest{},
		idListEntitiesDoneResponse: &apipb.ListEntitiesDoneResponse{},
		idSubscribeStatesRequest:   &apipb.SubscribeStatesRequest{},
		idBinarySensorState:        &apipb.BinarySensorStateResponse{},
		idLightState:               &apipb.LightStateResponse{},
		idSensorState:              &apipb.SensorStateResponse{},
		idSwitchState:              &apipb.SwitchStateResponse{},
		idTextSensorState:          &apipb.TextSensorStateResponse{},
		idLightCommandRequest:      &apipb.LightCommandRequest{},
		idSwitchCommandRequest:     &apipb.SwitchCommandRequest{},
		idClimateState:             &apipb.ClimateStateResponse{},
		idClimateCommandRequest:    &apipb.ClimateCommandRequest{},
		idNumberState:              &apipb.NumberStateResponse{},
		idNumberCommandRequest:     &apipb.NumberCommandRequest{},
		idSelectState:              &apipb.SelectStateResponse{},
		idSelectCommandRequest:     &apipb.SelectCommandRequest{},
	}
	for want, message := range cases {
		got, _, err := encode(message)
		if err != nil || got != want {
			t.Errorf("%T id = %d, %v; want %d", message, got, err, want)
		}
	}
}

func TestDecodeEntityKeepsIdentityForUnmappedPlatforms(t *testing.T) {
	t.Parallel()
	id, data, err := encode(&apipb.ListEntitiesButtonResponse{
		Key: 7, Name: "ESP Reboot", DeviceClass: "restart", EntityCategory: apipb.EntityCategory_ENTITY_CATEGORY_CONFIG,
	})
	if err != nil {
		t.Fatal(err)
	}
	entity, err := decodeEntity(id, data)
	if err != nil {
		t.Fatal(err)
	}
	want := EntityInfo{
		Type: EntityOther, UpstreamType: "button", Key: 7, Name: "ESP Reboot",
		Category: CategoryConfig, DeviceClass: "restart",
	}
	if fmt.Sprint(entity) != fmt.Sprint(want) {
		t.Fatalf("entity = %+v, want %+v", entity, want)
	}
}

func TestDecodeEntityReadsMappedPlatformFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		message proto.Message
		want    EntityInfo
	}{
		{
			message: &apipb.ListEntitiesSensorResponse{
				Key: 1, Name: "CO2", DeviceClass: "carbon_dioxide", UnitOfMeasurement: "ppm", AccuracyDecimals: 0,
				DisabledByDefault: true, DeviceId: 3,
			},
			want: EntityInfo{
				Type: EntitySensor, UpstreamType: "sensor", Key: 1, DeviceID: 3, Name: "CO2",
				DisabledByDefault: true, DeviceClass: "carbon_dioxide", Unit: "ppm",
			},
		},
		{
			message: &apipb.ListEntitiesSwitchResponse{Key: 2, Name: "Relay", AssumedState: true},
			want:    EntityInfo{Type: EntitySwitch, UpstreamType: "switch", Key: 2, Name: "Relay", AssumedState: true},
		},
		{
			message: &apipb.ListEntitiesTextSensorResponse{Key: 5, Name: "VOC Quality"},
			want:    EntityInfo{Type: EntityTextSensor, UpstreamType: "text_sensor", Key: 5, Name: "VOC Quality"},
		},
		{
			message: &apipb.ListEntitiesLightResponse{
				Key: 3, Name: "RGB Light",
				SupportedColorModes: []apipb.ColorMode{apipb.ColorMode_COLOR_MODE_RGB},
				Effects:             []string{"None", "Slow Pulse", "Fast Pulse"},
			},
			want: EntityInfo{
				Type: EntityLight, UpstreamType: "light", Key: 3, Name: "RGB Light", ColorModes: []uint32{35},
				Effects: []string{"None", "Slow Pulse", "Fast Pulse"},
			},
		},
		{
			message: &apipb.ListEntitiesBinarySensorResponse{
				Key: 4, Name: "Online", DeviceClass: "connectivity",
				EntityCategory: apipb.EntityCategory_ENTITY_CATEGORY_DIAGNOSTIC,
			},
			want: EntityInfo{
				Type: EntityBinarySensor, UpstreamType: "binary_sensor", Key: 4, Name: "Online",
				Category: CategoryDiagnostic, DeviceClass: "connectivity",
			},
		},
	}
	for _, test := range cases {
		id, data, err := encode(test.message)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeEntity(id, data)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Errorf("entity = %+v, want %+v", got, test.want)
		}
	}
}

func TestDecodeStateIgnoresUnknownFields(t *testing.T) {
	t.Parallel()
	_, data, err := encode(&apipb.SensorStateResponse{Key: 9, State: 21.5})
	if err != nil {
		t.Fatal(err)
	}
	data = protowire.AppendTag(data, 99, protowire.VarintType)
	data = protowire.AppendVarint(data, 12345)
	update, ok, err := decodeState(idSensorState, data)
	if err != nil || !ok {
		t.Fatalf("decode = %v, %v", ok, err)
	}
	if update != (StateUpdate{Type: EntitySensor, Key: 9, Number: 21.5}) {
		t.Fatalf("update = %+v", update)
	}
}

func TestShortestFloatKeepsWireDecimal(t *testing.T) {
	t.Parallel()
	for wire, want := range map[float32]float64{0.2: 0.2, 21.538067: 21.538067, -23.9: -23.9, 1572: 1572} {
		if got := shortestFloat(wire); got != want {
			t.Errorf("shortestFloat(%v) = %v, want %v", wire, got, want)
		}
	}
}

func TestParsePSK(t *testing.T) {
	t.Parallel()
	key := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	psk, err := ParsePSK(" " + key + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if psk[31] != 31 {
		t.Fatalf("psk[31] = %d", psk[31])
	}
	for _, text := range []string{"", "not base64", "AAEC"} {
		if _, parsePSKErr := ParsePSK(text); parsePSKErr == nil {
			t.Errorf("ParsePSK(%q) succeeded", text)
		}
	}
	if fmt.Sprint(psk) != "[redacted]" || fmt.Sprintf("%#v", psk) != "nativeapi.PSK([redacted])" {
		t.Fatalf("psk formatting leaked: %v %#v", psk, psk)
	}
}

func TestParseServerHello(t *testing.T) {
	t.Parallel()
	hello, err := parseServerHello([]byte("\x01office-air\x000200000000A1\x00"))
	if err != nil || hello != (serverHello{name: "office-air", mac: "0200000000a1"}) {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	hello, err = parseServerHello([]byte("\x01"))
	if err != nil || hello != (serverHello{}) {
		t.Fatalf("bare hello = %+v, %v", hello, err)
	}
	if _, parseServerHelloErr := parseServerHello([]byte("\x02")); !errors.Is(parseServerHelloErr, ErrIncompatibleAPI) {
		t.Fatalf("protocol 2 error = %v", parseServerHelloErr)
	}
}

func TestWriteMessageRejectsOversizedData(t *testing.T) {
	t.Parallel()
	var sink bytes.Buffer
	transport := &noiseTransport{writer: &sink}
	if err := transport.writeMessage(1, make([]byte, maximumMessageData+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("error = %v", err)
	}
	if sink.Len() != 0 {
		t.Fatalf("wrote %d bytes", sink.Len())
	}
}

func TestCompactMAC(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"02:00:00:00:00:A1", "0200000000a1", "02-00-00-00-00-a1"} {
		if got := compactMAC(text); got != "0200000000a1" {
			t.Errorf("compactMAC(%q) = %q", text, got)
		}
	}
}
