package zigbee2mqtt //nolint:testpackage // Pending-queue bounds and diagnostics are package-private.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects the pending-queue bound and the no-silent-eviction policy,
// and it fails if the bound stops refusing queue growth, if a refused append
// mutates order or content, or if coalescing an existing ordinary topic at the
// bound starts being rejected. Plausible defect: a naive implementation drops
// or replaces one queued entry to admit the new message, which would silently
// lose a chosen Event occurrence while the connection stays alive.
func TestPendingMessageLimitRefusesGrowthWithoutEviction(t *testing.T) {
	t.Parallel()
	state := &connectionSync{}
	for index := range pendingMessageLimit {
		topic := fmt.Sprintf("zigbee2mqtt/ordinary-%04d", index)
		if err := state.queuePending(
			mqttMessage{Topic: topic, Payload: []byte(`{"battery":100}`)},
			false,
		); err != nil {
			t.Fatalf("filling entry %d: %v", index, err)
		}
	}
	if len(state.pending) != pendingMessageLimit {
		t.Fatalf("pending = %d, want the %d-entry bound", len(state.pending), pendingMessageLimit)
	}

	// Replacing an existing ordinary topic stays allowed at the bound: it
	// swaps in the latest payload and moves the entry to the end without
	// changing the count, so the latest State per topic still replays last.
	replaced := mqttMessage{Topic: "zigbee2mqtt/ordinary-0000", Payload: []byte(`{"battery":7}`)}
	if err := state.queuePending(replaced, false); err != nil {
		t.Fatalf("replacing an existing ordinary topic at the bound: %v", err)
	}
	if len(state.pending) != pendingMessageLimit {
		t.Fatalf("pending after replacement = %d, want %d", len(state.pending), pendingMessageLimit)
	}
	last := state.pending[len(state.pending)-1]
	if last.occurrence || last.message.Topic != replaced.Topic ||
		string(last.message.Payload) != string(replaced.Payload) {
		t.Fatalf("replacement entry = %#v, want the latest payload moved to the end", last)
	}

	before := append([]pendingMessage(nil), state.pending...)
	for _, refused := range []struct {
		name       string
		message    mqttMessage
		occurrence bool
	}{
		{
			name:    "new ordinary topic",
			message: mqttMessage{Topic: "zigbee2mqtt/ordinary-new", Payload: []byte(`{"battery":1}`)},
		},
		{
			name:       "event occurrence",
			message:    mqttMessage{Topic: "zigbee2mqtt/ordinary-0001", Payload: []byte(`{"action":"single"}`)},
			occurrence: true,
		},
	} {
		err := state.queuePending(refused.message, refused.occurrence)
		if !errors.Is(err, errPendingMessageLimit) {
			t.Fatalf("%s error = %v, want errPendingMessageLimit", refused.name, err)
		}
		if len(state.pending) != pendingMessageLimit || !reflect.DeepEqual(state.pending, before) {
			t.Fatalf("%s mutated the bounded queue: %#v", refused.name, state.pending)
		}
	}
}

// This test protects the overflow policy end to end, and it fails if overflow
// keeps the connection alive by evicting a chosen message, if the retry loop
// treats overflow as a fatal session error instead of reconnecting, or if the
// overflow diagnostic leaks a topic, payload, or device identity. Plausible
// defect: extending the queue forever, or returning a sessionOperationError so
// the adapter process exits instead of resynchronizing.
func TestRunConnectionOverflowEndsGenerationAndReconnects(t *testing.T) {
	t.Parallel()
	const friendlySentinel = "sentinel-burst-4f2a"
	handler := newCaptureHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first := newFakeConnection(recorder)
	first.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		// No bridge/devices is emitted, so routes never activate and every
		// distinct ordinary topic below queues as its own entry. The burst runs
		// in a separate goroutine because Subscribe is still on the run-loop
		// stack; it needs no sleeps, only the relay channel's own backpressure.
		go func() {
			for index := 0; index <= pendingMessageLimit; index++ {
				connection.emit(
					fmt.Sprintf("zigbee2mqtt/%s-%04d", friendlySentinel, index),
					[]byte(`{"battery":100}`),
					false,
					now.Add(time.Duration(index)*time.Millisecond),
				)
			}
		}()
	}
	second := newFakeConnection(recorder)
	dialer := &fakeDialer{connections: []*fakeConnection{first, second}}
	z2m := newCaptureAdapter(t, session, handler, dialer)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	// A second dial proves the overflow ended generation one and the existing
	// reconnect path proceeded; the fake dialer is the synchronization point.
	waitFor(t, func() bool {
		dialer.mutex.Lock()
		defer dialer.mutex.Unlock()
		return dialer.dials >= 2
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if !first.closed {
		t.Fatal("overflow generation left its MQTT connection open")
	}
	if len(session.health) != 1 ||
		session.health[0].Status != adapter.HealthUnhealthy ||
		session.health[0].ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("health reports = %#v, want one unhealthy retry report", session.health)
	}
	if got := handler.count(slog.LevelWarn, "adapter.pending_message_limit_reached"); got != 1 {
		t.Fatalf("overflow warnings = %d, want exactly one", got)
	}
	record, found := handler.first(slog.LevelWarn, "adapter.pending_message_limit_reached")
	if !found {
		t.Fatal("missing adapter.pending_message_limit_reached record")
	}
	wantKeys := map[string]bool{
		"component": true, "event": true, "error_code": true, "pending_limit": true,
	}
	for key := range record.attrs {
		if !wantKeys[key] {
			t.Fatalf("overflow record carries unexpected field %q: %#v", key, record.attrs)
		}
	}
	if record.attrs["error_code"] != pendingMessageLimitErrorCode {
		t.Fatalf("error_code = %v, want %q", record.attrs["error_code"], pendingMessageLimitErrorCode)
	}
	if record.attrs["pending_limit"] != int64(pendingMessageLimit) {
		t.Fatalf("pending_limit = %v, want %d", record.attrs["pending_limit"], pendingMessageLimit)
	}
	if handler.containsText(friendlySentinel) {
		t.Fatal("logs leak the burst topic identity")
	}
	// The retry loop classifies overflow with its own stable code rather than
	// the generic connection failure.
	retry, found := handler.first(slog.LevelDebug, "dependency.retrying")
	if !found || retry.attrs["error_code"] != pendingMessageLimitErrorCode {
		t.Fatalf("retry record = %#v, want error_code %q", retry.attrs, pendingMessageLimitErrorCode)
	}
}
