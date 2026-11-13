package esphome //nolint:testpackage // Exercises dispatch policy in the worker's shared light FIFO.

import (
	"testing"
	"testing/synctest"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
)

//nolint:gocognit // One ordered journey verifies both directions of power/effect FIFO admission.
func TestEffectsCheckPowerAtDispatchWithoutImplicitTurnOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		device := testDevice("02:00:00:00:00:01")
		device.Entities[2].(*apipb.ListEntitiesLightResponse).Effects = []string{"None", "Slow Pulse", "Fast Pulse"}
		dialer.devices["one"] = device
		instance, stop := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		power := entityIDFor(t, session, "02:00:00:00:00:01/0/light/Lamp#power")
		effect := entityIDFor(t, session, "02:00:00:00:00:01/0/light/Lamp#effect")
		rejected, done := command(t, instance, session, effect, "effect_while_off", `{"value":"Fast Pulse"}`)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if rejected.result() != "unavailable" {
			t.Fatalf("off effect = %s", rejected.result())
		}
		assertNoEffectFrame(t, peer)

		_, powerDone := command(t, instance, session, power, "power_on", `{"value":true}`)
		synctest.Wait()
		if request := nextLightFrame(t, peer); !request.GetHasState() || !request.GetState() {
			t.Fatalf("power on=%v", request)
		}
		accepted, effectDone := command(t, instance, session, effect, "effect_after_on", `{"value":"Fast Pulse"}`)
		synctest.Wait()
		if accepted.result() != "" {
			t.Fatal("queued effect responded before power evidence")
		}
		assertNoEffectFrame(t, peer)
		if err := peer.Push(&apipb.LightStateResponse{Key: 3, State: true, Brightness: 1, ColorBrightness: 1,
			ColorMode: apipb.ColorMode_COLOR_MODE_RGB, Red: 1, Effect: "None"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := <-powerDone; err != nil {
			t.Fatal(err)
		}
		if err := <-effectDone; err != nil {
			t.Fatal(err)
		}
		request := nextLightFrame(t, peer)
		if !request.GetHasEffect() || request.GetEffect() != "Fast Pulse" || request.GetHasState() ||
			request.GetHasBrightness() {
			t.Fatalf("effect=%v", request)
		}
		if err := peer.Push(&apipb.LightStateResponse{Key: 3, State: true, Brightness: 1, ColorBrightness: 1,
			ColorMode: apipb.ColorMode_COLOR_MODE_RGB, Red: 1, Effect: "Fast Pulse"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()

		_, powerDone = command(t, instance, session, power, "power_off", `{"value":false}`)
		synctest.Wait()
		if powerOffRequest := nextLightFrame(t, peer); !powerOffRequest.GetHasState() || powerOffRequest.GetState() {
			t.Fatalf("power off=%v", powerOffRequest)
		}
		rejected, effectDone = command(t, instance, session, effect, "effect_after_off", `{"value":"Slow Pulse"}`)
		synctest.Wait()
		if err := peer.Push(&apipb.LightStateResponse{Key: 3, State: false, Brightness: 1, ColorBrightness: 1,
			ColorMode: apipb.ColorMode_COLOR_MODE_RGB, Red: 1, Effect: "None"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := <-powerDone; err != nil {
			t.Fatal(err)
		}
		if err := <-effectDone; err != nil {
			t.Fatal(err)
		}
		if rejected.result() != "unavailable" {
			t.Fatalf("queued off effect=%s", rejected.result())
		}
		assertNoEffectFrame(t, peer)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
	})
}

func assertNoEffectFrame(t *testing.T, peer *nativeapitest.Session) {
	t.Helper()
	select {
	case message := <-peer.Received():
		t.Fatalf("unexpected frame: %v", message)
	default:
	}
}

func nextLightFrame(t *testing.T, peer *nativeapitest.Session) *apipb.LightCommandRequest {
	t.Helper()
	message := <-peer.Received()
	request, ok := message.Message.(*apipb.LightCommandRequest)
	if !ok {
		t.Fatalf("not a light command: %v", message)
	}
	return request
}
