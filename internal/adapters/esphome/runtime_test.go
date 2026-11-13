package esphome //nolint:testpackage // Runtime tests inject the connection and SDK seams.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestRuntimeReportsHealthBeforeAvailabilityAndAvailabilityBeforeState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{owned: []adapter.OwnedMapping{{
			BindingKey: "esphome-020000000009", EntityKey: "old", EntityID: "ent_old",
		}}}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		_, stop := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		calls := session.snapshot()
		if len(calls) < 4 || calls[0].kind != "health" || calls[1].kind != "availability" ||
			calls[2].kind != "register" {
			t.Fatalf("startup calls = %+v", calls)
		}
		if calls[1].availability[0].ReasonCode != reasonDeviceNotConfigured ||
			calls[0].health.Status != adapter.HealthHealthy {
			t.Fatalf("startup = %+v", calls[:2])
		}
		available := map[string]bool{}
		for _, entry := range calls {
			for _, report := range entry.availability {
				available[report.EntityID] = report.Status == adapter.AvailabilityAvailable
			}
			if entry.kind == "observation" && !available[entry.observation.EntityID] {
				t.Fatalf("State published before available: %+v", entry)
			}
		}
		if got := len(callsOfKind(calls, "observation")); got != 6 {
			t.Fatalf("observations = %d, want 6 including RGB color", got)
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
	})
}

//nolint:gocognit // One recovery journey verifies isolation, continuing sibling reports, and cache reset.
func TestRuntimeDisconnectIsPerDeviceAndReconnectClearsCache(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		two := testDevice("02:00:00:00:00:02")
		two.Entities = two.Entities[:1]
		two.Entities[0] = &apipb.ListEntitiesSensorResponse{
			Key:               1,
			Name:              "Other Temperature",
			DeviceClass:       "temperature",
			UnitOfMeasurement: "°C",
		}
		two.States = two.States[:1]
		dialer.devices["two"] = two
		instance, _ := runAdapter(t, session, dialer,
			harnessDevice(t, "02:00:00:00:00:01", "one"), harnessDevice(t, "02:00:00:00:00:02", "two"))
		synctest.Wait()
		relay := entityIDFor(t, session, "02:00:00:00:00:01/0/switch/Relay#power")
		other := entityIDFor(t, session, "02:00:00:00:00:02/0/sensor/Other Temperature")
		dialer.setFailure("one", errors.New("offline"))
		dialer.latest(t, "one").Close()
		synctest.Wait()
		latest := lastAvailability(session.snapshot())
		if latest[relay].ReasonCode != reasonDeviceUnreachable ||
			latest[other].Status != adapter.AvailabilityAvailable {
			t.Fatalf("availability = %+v", latest)
		}
		if err := dialer.latest(t, "two").Push(&apipb.SensorStateResponse{Key: 1, State: 22}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		updated := false
		for _, entry := range callsOfKind(session.snapshot(), "observation") {
			if entry.observation.EntityID == other && string(entry.observation.Value) == "22000" {
				updated = true
			}
		}
		if !updated {
			t.Fatal("healthy sibling stopped publishing during device outage")
		}
		responder, done := command(t, instance, session, relay, "cmd_offline", `{"value":false}`)
		synctest.Wait()
		if err := <-done; err != nil || responder.result() != "unavailable" {
			t.Fatalf("offline command = %s, %v", responder.result(), err)
		}
		for _, entry := range callsOfKind(session.snapshot(), "health") {
			if entry.health.Status != adapter.HealthHealthy {
				t.Fatalf("device outage changed Adapter health: %+v", entry)
			}
		}
		// Reconnect without initial states: a cache from the old connection must not satisfy this command.
		dialer.mu.Lock()
		device := dialer.devices["one"]
		device.States = nil
		dialer.devices["one"] = device
		dialer.mu.Unlock()
		dialer.setFailure("one", nil)
		time.Sleep(time.Second)
		synctest.Wait()
		responder, done = command(t, instance, session, relay, "cmd_reconnected", `{"value":false}`)
		synctest.Wait()
		if err := <-done; err != nil || responder.result() != "accepted" {
			t.Fatalf("reconnected command = %s, %v", responder.result(), err)
		}
		if got := len(callsOfKind(session.snapshot(), "linked")); got != 0 {
			t.Fatalf("old cache produced %d linked observations", got)
		}
		if lastAvailability(session.snapshot())[relay].Status != adapter.AvailabilityAvailable {
			t.Fatal("reconnection did not restore availability")
		}
	})
}

func TestRuntimeUnsupportedConnectionsRegisterNothing(t *testing.T) {
	t.Parallel()
	for name, reason := range map[string]string{
		"deep_sleep": reasonDeepSleepUnsupported, "wrong_mac": reasonDeviceIdentityMismatch, "wrong_key": reasonEncryptionKeyRejected,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				session := &fakeSession{
					owned: []adapter.OwnedMapping{
						{BindingKey: "esphome-020000000001", EntityKey: "existing", EntityID: "ent_existing"},
					},
				}
				dialer := newPipeDialer()
				device := testDevice("02:00:00:00:00:01")
				switch name {
				case "deep_sleep":
					device.Info.HasDeepSleep = true
				case "wrong_mac":
					device.Info.MacAddress = "02:00:00:00:00:02"
				case "wrong_key":
					device.PSK[0]++
				}
				dialer.devices["one"] = device
				_, _ = runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
				synctest.Wait()
				if len(callsOfKind(session.snapshot(), "register")) != 0 {
					t.Fatal("unsupported device registered Entities")
				}
				if got := lastAvailability(session.snapshot())["ent_existing"].ReasonCode; got != reason {
					t.Fatalf("reason = %s, want %s", got, reason)
				}
			})
		})
	}
}

//nolint:gocognit,gocyclo,cyclop // This cohesive scenario verifies the complete ordered behavior.
func TestCommandsLinkCachedAndFreshStateAndSerializeLightControls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		relay := entityIDFor(t, session, "02:00:00:00:00:01/0/switch/Relay#power")
		device := dialer.latest(t, "one")
		responder, done := command(t, instance, session, relay, "cmd_cached", `{"value":false}`)
		synctest.Wait()
		if err := <-done; err != nil || responder.result() != "accepted" {
			t.Fatalf("cached command = %s, %v", responder.result(), err)
		}
		linked := callsOfKind(session.snapshot(), "linked")
		if len(linked) != 1 || linked[0].linkedTo != "cmd_cached" || string(linked[0].observation.Value) != "false" {
			t.Fatalf("cached evidence = %+v", linked)
		}
		responder, done = command(t, instance, session, relay, "cmd_fresh", `{"value":true}`)
		synctest.Wait()
		if err := <-done; err != nil || responder.result() != "accepted" {
			t.Fatalf("fresh command = %s, %v", responder.result(), err)
		}
		if err := device.Push(&apipb.SwitchStateResponse{Key: 2, State: false}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := device.Push(&apipb.SwitchStateResponse{Key: 2, State: true}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		linked = callsOfKind(session.snapshot(), "linked")
		if len(linked) != 3 || linked[1].linkedTo != "cmd_fresh" || string(linked[1].observation.Value) != "false" ||
			linked[2].linkedTo != "cmd_fresh" || string(linked[2].observation.Value) != "true" {
			t.Fatalf("fresh evidence = %+v", linked)
		}
		brightness := entityIDFor(t, session, "02:00:00:00:00:01/0/light/Lamp#brightness")
		power := entityIDFor(t, session, "02:00:00:00:00:01/0/light/Lamp#power")
		_, first := command(t, instance, session, brightness, "cmd_50", `{"value":50}`)
		synctest.Wait()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		_, second := command(t, instance, session, power, "cmd_off", `{"value":false}`)
		synctest.Wait()
		select {
		case <-second:
			t.Fatal("light power bypassed pending brightness")
		default:
		}
		if err := device.Push(&apipb.LightStateResponse{Key: 3, State: true, Brightness: 0.5}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		if err := device.Push(&apipb.LightStateResponse{Key: 3, State: false, Brightness: 0.5}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		linked = callsOfKind(session.snapshot(), "linked")
		last := linked[len(linked)-2:]
		if last[0].linkedTo != "cmd_50" || string(last[0].observation.Value) != "50" ||
			last[1].linkedTo != "cmd_off" || string(last[1].observation.Value) != "false" {
			t.Fatalf("light evidence = %+v", last)
		}
		var writes []*apipb.LightCommandRequest
		for {
			select {
			case message := <-device.Received():
				if light, ok := message.Message.(*apipb.LightCommandRequest); ok {
					writes = append(writes, light)
				}
			default:
				if len(writes) != 2 || !writes[0].GetHasState() || !writes[0].GetState() ||
					!writes[0].GetHasBrightness() ||
					writes[0].GetBrightness() != 0.5 ||
					writes[1].GetState() {
					t.Fatalf("light writes = %+v", writes)
				}
				return
			}
		}
	})
}

func TestCommandExpiryReleasesFIFOAndShutdownAnswersQueuedHandlers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = testDevice("02:00:00:00:00:01")
		instance, stop := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		relay := entityIDFor(t, session, "02:00:00:00:00:01/0/switch/Relay#power")
		_, first := command(t, instance, session, relay, "cmd_first", `{"value":true}`)
		synctest.Wait()
		<-first
		_, second := command(t, instance, session, relay, "cmd_second", `{"value":true}`)
		synctest.Wait()
		select {
		case <-second:
			t.Fatal("FIFO advanced before first expired")
		default:
		}
		// Submit the second one a moment later with a later deadline in a direct handler call.
		time.Sleep(11 * time.Second)
		synctest.Wait()
		<-second
		responder, active := command(t, instance, session, relay, "cmd_active", `{"value":true}`)
		synctest.Wait()
		<-active
		if responder.result() != "accepted" {
			t.Fatal("new Command not dispatched after expiry")
		}
		queuedResponder, queued := command(t, instance, session, relay, "cmd_queued", `{"value":false}`)
		synctest.Wait()
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if err := <-queued; err != nil || queuedResponder.result() != "unavailable" {
			t.Fatalf("queued shutdown = %s, %v", queuedResponder.result(), err)
		}
	})
}

func TestBackoffGrowsBeforeJitterAndResetsAfterStableConnection(t *testing.T) {
	t.Parallel()
	w := &worker{adapter: &Adapter{timing: harnessTiming()}}
	delay := time.Duration(0)
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute} {
		delay = w.nextDelay(delay, 0, reasonDeviceUnreachable)
		if delay != want {
			t.Fatalf("backoff = %v, want %v", delay, want)
		}
	}
	if got := w.nextDelay(delay, time.Minute, reasonDeviceUnreachable); got != time.Second {
		t.Fatalf("reset = %v", got)
	}
	if got := w.nextDelay(0, 0, reasonEncryptionKeyRejected); got != time.Minute {
		t.Fatalf("key backoff = %v", got)
	}
}

func TestUnavailableReasonClassification(t *testing.T) {
	t.Parallel()
	if connectionReason(nativeapi.ErrPlaintextDevice) != reasonPlaintextRejected ||
		connectionReason(context.DeadlineExceeded) != reasonDeviceUnreachable {
		t.Fatal("wrong error classification")
	}
}
