package zigbee2mqtt //nolint:testpackage // Runtime tests use the package-private MQTT seam.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects connection generations and bridge recovery. It fails if loss leaves routes healthy, retained
// synchronization is skipped on reconnect, or recovered availability precedes the second healthy acknowledgement.
func TestRunDisconnectsUnhealthyAndResynchronizesNewGeneration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connections := []*fakeConnection{newFakeConnection(recorder), newFakeConnection(recorder)}
	for index, connection := range connections {
		generationTime := now.Add(time.Duration(index) * time.Minute)
		connection.onSubscribe = func(connection *fakeConnection) {
			connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, generationTime)
			connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, generationTime)
			connection.emit("zigbee2mqtt/bridge/devices", inventory, true, generationTime)
			connection.emit("zigbee2mqtt/test-light/availability", []byte(`{"state":"online"}`), true, generationTime)
		}
	}
	dialer := &fakeDialer{connections: connections}
	z2m := newRuntimeAdapter(t, session, dialer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 1
	})
	connections[0].lost <- errors.New("broker stopped")
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) >= 3 && session.health[len(session.health)-1].Status == adapter.HealthHealthy
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if dialer.dials != 2 || z2m.generation != 2 {
		t.Fatalf("dials=%d generation=%d", dialer.dials, z2m.generation)
	}
	if session.health[1].Status != adapter.HealthUnhealthy ||
		session.health[1].ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("health reports = %#v", session.health)
	}
	events := recorder.snapshot()
	firstHealthy := indexOf(events, "health:healthy:", 0)
	unhealthy := indexOf(events, "health:unhealthy:"+externalSystemUnavailableReason, firstHealthy+1)
	secondHealthy := indexOf(events, "health:healthy:", unhealthy+1)
	secondAvailability := indexOf(events, "availability", secondHealthy+1)
	if firstHealthy < 0 || unhealthy < 0 || secondHealthy < 0 || secondAvailability < 0 {
		t.Fatalf("recovery ordering events = %v", events)
	}
}

// This test protects loss during reconciliation and fails if a dead MQTT generation reports healthy or available.
func TestRunConnectionLossCancelsReconciliationBeforeHealthy(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	registerStarted := make(chan struct{})
	session.registerHook = func(ctx context.Context, _ adapter.Registration) {
		close(registerStarted)
		<-ctx.Done()
	}
	connection := newFakeConnection(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	<-registerStarted
	connection.lost <- errors.New("broker stopped during registration")
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) >= 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	for _, report := range session.health {
		if report.Status == adapter.HealthHealthy {
			t.Fatalf("dead generation reported healthy: %#v", session.health)
		}
	}
	if len(session.availability) != 0 {
		t.Fatalf("dead generation reported availability: %#v", session.availability)
	}
}
