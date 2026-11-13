package esphome //nolint:testpackage // Runs real Native API frames through the owning worker.

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// coverDevice is synthetic, shaped like ratgdo's primary entities, not a captured device.
func coverDevice() nativeapitest.Device {
	device := testDevice("02:00:00:00:00:01")
	device.Entities = append(device.Entities,
		&apipb.ListEntitiesCoverResponse{Key: 7, Name: "Door", SupportsPosition: true, SupportsStop: true},
		&apipb.ListEntitiesLockResponse{Key: 8, Name: "Lock remotes"})
	device.States = append(device.States,
		&apipb.CoverStateResponse{Key: 7},
		&apipb.LockStateResponse{Key: 8, State: apipb.LockState_LOCK_STATE_UNLOCKED})
	return device
}

func coverCommand(
	t *testing.T,
	instance *Adapter,
	session *fakeSession,
	entityID, id, operation, parameters string,
	deadline time.Duration,
) *fakeResponder {
	t.Helper()
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
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("cover request waited for an earlier target instead of dispatching")
	}
	return responder
}

func nextCoverFrame(t *testing.T, peer *nativeapitest.Session) *apipb.CoverCommandRequest {
	t.Helper()
	select {
	case frame := <-peer.Received():
		request, ok := frame.Message.(*apipb.CoverCommandRequest)
		if !ok {
			t.Fatalf("unexpected command: %v", frame)
		}
		return request
	default:
		t.Fatal("cover frame was not sent")
		return nil
	}
}

func pushCover(t *testing.T, peer *nativeapitest.Session, position float32) {
	t.Helper()
	if err := peer.Push(&apipb.CoverStateResponse{Key: 7, Position: position}); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

//nolint:gocognit // One ordered journey verifies travel interruption, no false evidence, and idle Stop.
func TestCoverStopInterruptsTargetsWithoutFabricatingState(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = coverDevice()
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		door := entityIDFor(t, session, "02:00:00:00:00:01/0/cover/Door")
		for _, test := range []struct {
			id, parameters string
			position       float32
		}{
			{"open", `{"position":100}`, 1}, {"half", `{"position":50}`, 0.5},
		} {
			responder := coverCommand(t, instance, session, door, test.id, "set", test.parameters, time.Minute)
			frame := nextCoverFrame(t, peer)
			if responder.result() != "accepted" || !frame.GetHasPosition() || frame.GetPosition() != test.position {
				t.Fatalf("target was not immediately dispatched: %s, %v", responder.result(), frame)
			}
		}
		before := len(session.snapshot())
		responder := coverCommand(t, instance, session, door, "stop", "stop", `{}`, 10*time.Second)
		frame := nextCoverFrame(t, peer)
		if responder.result() != "accepted" || !frame.GetStop() || frame.GetHasPosition() {
			t.Fatalf("stop=%s, %v", responder.result(), frame)
		}
		if len(session.snapshot()) != before {
			t.Fatal("Stop fabricated an observation before a device report")
		}
		pushCover(t, peer, 1)
		if linked := callsOfKind(session.snapshot(), "linked"); len(linked) != 0 {
			t.Fatalf("post-stop report satisfied old travel: %+v", linked)
		}
		responder = coverCommand(t, instance, session, door, "quarter", "set", `{"position":25}`, time.Minute)
		if frame = nextCoverFrame(t, peer); responder.result() != "accepted" || frame.GetPosition() != 0.25 {
			t.Fatalf("new target=%s, %v", responder.result(), frame)
		}
		pushCover(t, peer, 0.25)
		linked := callsOfKind(session.snapshot(), "linked")
		if len(linked) != 1 || linked[0].linkedTo != "quarter" {
			t.Fatalf("new target did not receive evidence: %+v", linked)
		}
		// Stopping an idle device needs no new push to be accepted.
		responder = coverCommand(t, instance, session, door, "idle_stop", "stop", `{}`, 10*time.Second)
		if frame = nextCoverFrame(t, peer); responder.result() != "accepted" || !frame.GetStop() {
			t.Fatalf("idle stop=%s, %v", responder.result(), frame)
		}
		if len(callsOfKind(session.snapshot(), "linked")) != 1 {
			t.Fatal("idle Stop published linked evidence")
		}
	})
}

func TestCoverTargetsMatchOneReportOldestFirstAndExpireIndependently(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = coverDevice()
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		door := entityIDFor(t, session, "02:00:00:00:00:01/0/cover/Door")
		for index, position := range []int{50, 51, 100} {
			deadline := time.Minute
			if position == 100 {
				deadline = time.Second
			}
			coverCommand(t, instance, session, door, strconv.Itoa(index), "set",
				fmt.Sprintf(`{"position":%d}`, position), deadline)
			nextCoverFrame(t, peer)
		}
		pushCover(t, peer, 0.5)
		linked := callsOfKind(session.snapshot(), "linked")
		if len(linked) != 1 || linked[0].linkedTo != "0" {
			t.Fatalf("report did not match only the oldest target: %+v", linked)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		pushCover(t, peer, 1)
		if len(callsOfKind(session.snapshot(), "linked")) != 1 {
			t.Fatal("expired target received evidence")
		}
		pushCover(t, peer, 0.51)
		linked = callsOfKind(session.snapshot(), "linked")
		if len(linked) != 2 || linked[1].linkedTo != "1" {
			t.Fatalf("expiry lost the other pending target: %+v", linked)
		}
		assertNoEffectFrame(t, peer) // No retry or late queued target.
	})
}

func TestCoverStopBypassesFullTargetLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = coverDevice()
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		door := entityIDFor(t, session, "02:00:00:00:00:01/0/cover/Door")
		for index := range commandQueueLimit {
			responder := coverCommand(
				t,
				instance,
				session,
				door,
				strconv.Itoa(index),
				"set",
				`{"position":100}`,
				time.Minute,
			)
			if responder.result() != "accepted" {
				t.Fatal("target rejected below limit")
			}
			nextCoverFrame(t, peer)
		}
		responder := coverCommand(t, instance, session, door, "full", "set", `{"position":50}`, time.Minute)
		if responder.result() != "unavailable" {
			t.Fatalf("limit rejection=%s", responder.result())
		}
		assertNoEffectFrame(t, peer)
		responder = coverCommand(t, instance, session, door, "stop", "stop", `{}`, 10*time.Second)
		if frame := nextCoverFrame(t, peer); responder.result() != "accepted" || !frame.GetStop() {
			t.Fatalf("full-limit Stop=%s, %v", responder.result(), frame)
		}
		pushCover(t, peer, 1)
		if len(callsOfKind(session.snapshot(), "linked")) != 0 {
			t.Fatal("Stop left an old matcher active")
		}
		assertNoEffectFrame(t, peer)
	})
}

func TestCoverReconnectDropsTargetsAndCacheWithoutReplay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		session := &fakeSession{}
		dialer := newPipeDialer()
		dialer.devices["one"] = coverDevice()
		instance, _ := runAdapter(t, session, dialer, harnessDevice(t, "02:00:00:00:00:01", "one"))
		synctest.Wait()
		peer := dialer.latest(t, "one")
		door := entityIDFor(t, session, "02:00:00:00:00:01/0/cover/Door")
		coverCommand(t, instance, session, door, "old", "set", `{"position":100}`, time.Minute)
		nextCoverFrame(t, peer)
		dialer.mu.Lock()
		device := dialer.devices["one"]
		device.States = nil
		dialer.devices["one"] = device
		dialer.mu.Unlock()
		peer.Close()
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		peer = dialer.latest(t, "one")
		assertNoEffectFrame(t, peer)
		pushCover(t, peer, 1)
		if len(callsOfKind(session.snapshot(), "linked")) != 0 {
			t.Fatal("reconnected State satisfied old command")
		}
		// A no-op target may use only this generation's live cache.
		coverCommand(t, instance, session, door, "cached", "set", `{"position":100}`, time.Minute)
		nextCoverFrame(t, peer)
		linked := callsOfKind(session.snapshot(), "linked")
		if len(linked) != 1 || linked[0].linkedTo != "cached" {
			t.Fatalf("live-generation cache not used: %+v", linked)
		}
	})
}
