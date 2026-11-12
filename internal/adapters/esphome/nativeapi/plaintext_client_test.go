package nativeapi_test

import (
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
)

func TestLegacyAPI10WithExplicitTransport(t *testing.T) {
	t.Parallel()
	for _, plaintext := range []bool{false, true} {
		t.Run(map[bool]string{false: "Noise", true: "plaintext"}[plaintext], func(t *testing.T) {
			t.Parallel()
			device := scriptedDevice(t)
			device.Plaintext, device.APIMajor, device.APIMinor = plaintext, 1, 10
			config := clientConfig(t)
			config.InsecurePlaintext = plaintext
			conn, peer, err := openPipe(t, device, config)
			if err != nil {
				t.Fatal(err)
			}
			entities, err := conn.ListEntities(t.Context())
			if err != nil || len(entities) != 4 {
				t.Fatalf("legacy entities = %+v, %v", entities, err)
			}
			if err = conn.SubscribeStates(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := nextEvent(t, conn); got.Number != 1572 {
				t.Fatalf("legacy State = %+v", got)
			}
			assertLegacySwitchCommand(t, conn, peer)
		})
	}
}

func assertLegacySwitchCommand(t *testing.T, conn nativeapi.Conn, peer *nativeapitest.Session) {
	t.Helper()
	if err := conn.Send(t.Context(), nativeapi.SwitchCommand{Key: 11, On: true}); err != nil {
		t.Fatal(err)
	}
	got := nextReceived(t, peer)
	request, ok := got.Message.(*apipb.SwitchCommandRequest)
	if !ok || request.GetKey() != 11 || !request.GetState() {
		t.Fatalf("command = %v", got.Message)
	}
}

func TestLegacyPasswordProtectedDeviceIsRejected(t *testing.T) {
	t.Parallel()
	for _, minor := range []uint32{10, 12, 13} {
		for _, plaintext := range []bool{false, true} {
			device := scriptedDevice(t)
			device.Plaintext, device.PasswordRequired, device.APIMajor, device.APIMinor = plaintext, true, 1, minor
			device.AutoAuthenticate = minor >= 12
			config := clientConfig(t)
			config.InsecurePlaintext = plaintext
			if _, _, err := openPipe(t, device, config); !errors.Is(err, nativeapi.ErrIncompatibleAPI) {
				t.Fatalf("API1.%d plaintext=%v password-protected device = %v", minor, plaintext, err)
			}
		}
	}
}

func TestLegacyAutoAuthenticatedFirmware(t *testing.T) {
	t.Parallel()
	for _, minor := range []uint32{12, 13} {
		for _, plaintext := range []bool{false, true} {
			device := scriptedDevice(t)
			device.Plaintext, device.APIMajor, device.APIMinor = plaintext, 1, minor
			device.AutoAuthenticate = true
			config := clientConfig(t)
			config.InsecurePlaintext = plaintext
			conn, _, err := openPipe(t, device, config)
			if err != nil {
				t.Fatalf("API1.%d plaintext=%v: %v", minor, plaintext, err)
			}
			entities, err := conn.ListEntities(t.Context())
			if err != nil || len(entities) != 4 {
				t.Fatalf("auto-authenticated entities = %+v, %v", entities, err)
			}
		}
	}
}
