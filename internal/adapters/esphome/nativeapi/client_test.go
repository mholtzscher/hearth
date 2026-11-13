package nativeapi_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
)

const deviceMAC = "0200000000a1"

func testPSK(t *testing.T, seed byte) nativeapi.PSK {
	t.Helper()
	var psk nativeapi.PSK
	for index := range psk {
		psk[index] = seed + byte(index)
	}
	return psk
}

func scriptedDevice(t *testing.T) nativeapitest.Device {
	t.Helper()
	return nativeapitest.Device{
		PSK:  testPSK(t, 1),
		Name: "office-air",
		MAC:  deviceMAC,
		Info: &apipb.DeviceInfoResponse{
			MacAddress: "02:00:00:00:00:A1", Name: "office-air", FriendlyName: "Office Air",
			EsphomeVersion: "2026.4.0", Model: "esp32-c3", Manufacturer: "Espressif",
		},
		Entities: []proto.Message{
			&apipb.ListEntitiesSensorResponse{
				Key:               10,
				Name:              "CO2",
				DeviceClass:       "carbon_dioxide",
				UnitOfMeasurement: "ppm",
			},
			&apipb.ListEntitiesSwitchResponse{Key: 11, Name: "Relay"},
			&apipb.ListEntitiesLightResponse{
				Key: 12, Name: "RGB Light", SupportedColorModes: []apipb.ColorMode{apipb.ColorMode_COLOR_MODE_RGB},
			},
			&apipb.ListEntitiesButtonResponse{Key: 13, Name: "ESP Reboot"},
		},
		States: []proto.Message{
			&apipb.SensorStateResponse{Key: 10, State: 1572},
			&apipb.SwitchStateResponse{Key: 11, State: true},
			&apipb.LightStateResponse{Key: 12, State: true, Brightness: 0.5},
		},
	}
}

// openPipe serves device on one end of a pipe and opens the client on the other.
func openPipe(
	t *testing.T,
	device nativeapitest.Device,
	config nativeapi.DialConfig,
) (nativeapi.Conn, *nativeapitest.Session, error) {
	t.Helper()
	client, server := net.Pipe()
	session := device.Serve(server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := nativeapi.Open(ctx, client, config)
	if err != nil {
		_ = client.Close()
		<-session.Done()
		return nil, session, err
	}
	t.Cleanup(func() {
		_ = conn.Close()
		<-session.Done()
	})
	return conn, session, nil
}

func clientConfig(t *testing.T) nativeapi.DialConfig {
	t.Helper()
	return nativeapi.DialConfig{PSK: testPSK(t, 1), ExpectMAC: "02:00:00:00:00:A1", ClientInfo: "test"}
}

func nextReceived(t *testing.T, session *nativeapitest.Session) nativeapitest.Message {
	t.Helper()
	select {
	case message, ok := <-session.Received():
		if !ok {
			t.Fatalf("session ended: %v", session.Err())
		}
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("no message received")
	}
	return nativeapitest.Message{}
}

func nextEvent(t *testing.T, conn nativeapi.Conn) nativeapi.StateUpdate {
	t.Helper()
	select {
	case update, ok := <-conn.Events():
		if !ok {
			t.Fatalf("events closed: %v", conn.Err())
		}
		if update.ReceivedAt.IsZero() {
			t.Fatal("state has no frame receipt timestamp")
		}
		update.ReceivedAt = time.Time{}
		return update
	case <-time.After(5 * time.Second):
		t.Fatal("no state received")
	}
	return nativeapi.StateUpdate{}
}

//nolint:gocognit // This cohesive scenario verifies the complete ordered behavior.
func TestConnDiscoversEntitiesStreamsStatesAndSendsCommands(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if server := conn.Server(); server != (nativeapi.ServerInfo{Name: "office-air", APIMajor: 1, APIMinor: 19}) {
		t.Fatalf("server = %+v", server)
	}
	ctx := context.Background()
	info, err := conn.DeviceInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.MAC != "02:00:00:00:00:A1" || info.FriendlyName != "Office Air" || info.ESPHomeVersion != "2026.4.0" {
		t.Fatalf("info = %+v", info)
	}
	entities, err := conn.ListEntities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []nativeapi.EntityType{
		nativeapi.EntitySensor, nativeapi.EntitySwitch, nativeapi.EntityLight, nativeapi.EntityOther,
	}
	if len(entities) != len(wantTypes) {
		t.Fatalf("entities = %+v", entities)
	}
	for index, entity := range entities {
		if entity.Type != wantTypes[index] {
			t.Errorf("entity %d type = %s, want %s", index, entity.Type, wantTypes[index])
		}
	}

	if subscribeStatesErr := conn.SubscribeStates(ctx); subscribeStatesErr != nil {
		t.Fatal(subscribeStatesErr)
	}
	wantStates := []nativeapi.StateUpdate{
		{Type: nativeapi.EntitySensor, Key: 10, Number: 1572},
		{Type: nativeapi.EntitySwitch, Key: 11, Bool: true},
		{Type: nativeapi.EntityLight, Key: 12, Bool: true, Brightness: 0.5},
	}
	for _, want := range wantStates {
		if got := nextEvent(t, conn); got != want {
			t.Errorf("state = %+v, want %+v", got, want)
		}
	}
	if pushErr := session.Push(&apipb.SensorStateResponse{Key: 10, State: 1580}); pushErr != nil {
		t.Fatal(pushErr)
	}
	if got := nextEvent(t, conn); got.Number != 1580 {
		t.Fatalf("pushed state = %+v", got)
	}

	brightness := 0.5
	commands := []struct {
		command nativeapi.Command
		want    proto.Message
	}{
		{nativeapi.SwitchCommand{Key: 11, On: true}, &apipb.SwitchCommandRequest{Key: 11, State: true}},
		{
			nativeapi.LightCommand{Key: 12, On: new(true), Brightness: &brightness},
			&apipb.LightCommandRequest{Key: 12, HasState: true, State: true, HasBrightness: true, Brightness: 0.5},
		},
		{nativeapi.LightCommand{Key: 12, On: new(false)}, &apipb.LightCommandRequest{Key: 12, HasState: true}},
	}
	for _, test := range commands {
		if sendErr := conn.Send(ctx, test.command); sendErr != nil {
			t.Fatal(sendErr)
		}
		got := nextReceived(t, session)
		wantBytes, _ := proto.MarshalOptions{Deterministic: true}.Marshal(test.want)
		gotBytes, _ := proto.MarshalOptions{Deterministic: true}.Marshal(got.Message)
		if string(gotBytes) != string(wantBytes) {
			t.Errorf("command = %v, want %v", got.Message, test.want)
		}
	}
}

func TestOpenFailsClosedBeforeAnyDeviceRequest(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		mutate func(*nativeapitest.Device, *nativeapi.DialConfig)
		want   error
	}{
		"wrong key": {
			mutate: func(_ *nativeapitest.Device, config *nativeapi.DialConfig) { config.PSK = testPSK(t, 9) },
			want:   nativeapi.ErrEncryptionKeyRejected,
		},
		"plaintext device": {
			mutate: func(device *nativeapitest.Device, _ *nativeapi.DialConfig) { device.Plaintext = true },
			want:   nativeapi.ErrPlaintextDevice,
		},
		"different MAC": {
			mutate: func(device *nativeapitest.Device, _ *nativeapi.DialConfig) { device.MAC = "0200000000b2" },
			want:   nativeapi.ErrIdentityMismatch,
		},
		"major version 2": {
			mutate: func(device *nativeapitest.Device, _ *nativeapi.DialConfig) { device.APIMajor, device.APIMinor = 2, 0 },
			want:   nativeapi.ErrIncompatibleAPI,
		},
		"minor version 9": {
			mutate: func(device *nativeapitest.Device, _ *nativeapi.DialConfig) { device.APIMajor, device.APIMinor = 1, 9 },
			want:   nativeapi.ErrIncompatibleAPI,
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			device, config := scriptedDevice(t), clientConfig(t)
			test.mutate(&device, &config)
			_, session, err := openPipe(t, device, config)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			for message := range session.Received() {
				t.Errorf("device received %v", message.Message)
			}
		})
	}
}

func TestConnAnswersDevicePings(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if pushErr := session.Push(&apipb.PingRequest{}); pushErr != nil {
		t.Fatal(pushErr)
	}
	if got := nextReceived(t, session); got.ID != 8 {
		t.Fatalf("answer = %d %v", got.ID, got.Message)
	}
	if connErr := conn.Err(); connErr != nil {
		t.Fatalf("conn ended: %v", connErr)
	}
}

func TestConnPingsWhenIdleAndEndsAfterSilence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		device := scriptedDevice(t)
		device.IgnorePings = true
		conn, session, err := openPipe(t, device, clientConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(19 * time.Second)
		synctest.Wait()
		select {
		case message := <-session.Received():
			t.Fatalf("early message %v", message.Message)
		default:
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := <-session.Received(); got.ID != 7 {
			t.Fatalf("idle message = %d, want ping", got.ID)
		}
		time.Sleep(38 * time.Second)
		synctest.Wait()
		if connErr := conn.Err(); connErr != nil {
			t.Fatalf("ended before the silence limit: %v", connErr)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if connErr := conn.Err(); !errors.Is(connErr, nativeapi.ErrKeepaliveTimeout) {
			t.Fatalf("error = %v", connErr)
		}
		if _, open := <-conn.Events(); open {
			t.Fatal("events still open")
		}
	})
}

func TestConnStaysOpenWhenDeviceAnswersPings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		conn, _, err := openPipe(t, scriptedDevice(t), clientConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if connErr := conn.Err(); connErr != nil {
			t.Fatalf("conn ended: %v", connErr)
		}
	})
}

func TestConnEndsWhenStateBufferFills(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	device := scriptedDevice(t)
	if subscribeStatesErr := conn.SubscribeStates(context.Background()); subscribeStatesErr != nil {
		t.Fatal(subscribeStatesErr)
	}
	for range 256 - len(device.States) {
		if pushErr := session.Push(&apipb.SensorStateResponse{Key: 10, State: 1}); pushErr != nil {
			t.Fatal(pushErr)
		}
	}
	_ = session.Push(&apipb.SensorStateResponse{Key: 10, State: 2})
	<-session.Done()
	deadline := time.After(5 * time.Second)
	for conn.Err() == nil {
		select {
		case <-deadline:
			t.Fatal("conn still open")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if connErr := conn.Err(); !errors.Is(connErr, nativeapi.ErrSlowConsumer) {
		t.Fatalf("error = %v", connErr)
	}
}

func TestConnIgnoresUnknownMessagesAndFields(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if subscribeStatesErr := conn.SubscribeStates(context.Background()); subscribeStatesErr != nil {
		t.Fatal(subscribeStatesErr)
	}
	for range scriptedDevice(t).States {
		nextEvent(t, conn)
	}
	if sendRawErr := session.SendRaw(9999, []byte{0xff, 0x00, 0x13}); sendRawErr != nil {
		t.Fatal(sendRawErr)
	}
	data, _ := proto.Marshal(&apipb.SensorStateResponse{Key: 10, State: 7})
	data = protowire.AppendTag(data, 99, protowire.BytesType)
	data = protowire.AppendBytes(data, []byte("future"))
	if sendRawErr := session.SendRaw(25, data); sendRawErr != nil {
		t.Fatal(sendRawErr)
	}
	if got := nextEvent(t, conn); got != (nativeapi.StateUpdate{Type: nativeapi.EntitySensor, Key: 10, Number: 7}) {
		t.Fatalf("state = %+v", got)
	}
	if connErr := conn.Err(); connErr != nil {
		t.Fatalf("conn ended: %v", connErr)
	}
}

func TestCloseDisconnectsPolitely(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := conn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	<-session.Done()
	if sessionErr := session.Err(); sessionErr == nil || sessionErr.Error() != "client disconnected" {
		t.Fatalf("session error = %v", sessionErr)
	}
	if !errors.Is(conn.Err(), nativeapi.ErrClosed) {
		t.Fatalf("conn error = %v", conn.Err())
	}
}

func TestDeviceDisconnectEndsConn(t *testing.T) {
	t.Parallel()
	conn, session, err := openPipe(t, scriptedDevice(t), clientConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if pushErr := session.Push(&apipb.DisconnectRequest{}); pushErr != nil {
		t.Fatal(pushErr)
	}
	if got := nextReceived(t, session); got.ID != 6 {
		t.Fatalf("answer = %d", got.ID)
	}
	if _, open := <-conn.Events(); open {
		t.Fatal("events still open")
	}
	if conn.Err() == nil {
		t.Fatal("conn still open")
	}
}
