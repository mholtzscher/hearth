package zigbee2mqtt //nolint:testpackage // Runtime tests use the package-private MQTT seam.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects fresh availability after an unhealthy transition and fails if pre-offline evidence is replayed.
func TestRunRecoveryDoesNotReplayStaleAvailability(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
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
		connection.emit("zigbee2mqtt/test-light/availability", []byte(`{"state":"online"}`), true, now)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 1 && len(session.availability) == 2
	})

	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"offline"}`), false, now.Add(time.Second))
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 2
	})
	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), false, now.Add(2*time.Second))
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 3
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if len(session.availability) != 2 {
		t.Fatalf("recovery replayed stale availability: %#v", session.availability)
	}
}

// This test protects availability identity and fails if a reused friendly name transfers evidence between IEEE Devices.
func TestReconciledAvailabilityUsesIEEEIdentity(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	device := runtimeDevice{
		bindingKey:  "z2m-00124b0024abcdee",
		ieeeAddress: "0x00124b0024abcdee",
		friendly:    "reused-name",
		entities: []runtimeEntity{{entityID: "new-power", plan: entityPlan{
			Descriptor: adapter.EntityDescriptor{Key: "power"},
		}}},
	}
	z2m.rememberMapping(adapter.OwnedMapping{
		BindingKey: device.bindingKey, EntityKey: "power", EntityID: "new-power",
	})
	inventory := inventoryDiscovery{Devices: []discoveredDevice{{
		IEEEAddress:  device.ieeeAddress,
		Registration: adapter.Registration{BindingKey: device.bindingKey},
	}}}
	err := z2m.reportReconciledAvailability(
		context.Background(),
		inventory,
		routeSnapshot{devices: map[string]runtimeDevice{device.friendly: device}},
		map[string]availabilityEvidence{
			"0x00124b0024abcdef": {available: true, receivedAt: time.Now().UTC()},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.availability) != 0 {
		t.Fatalf("foreign IEEE availability was reported: %#v", session.availability)
	}
}

// This test protects explicit availability evidence and fails if unknown strings or malformed payloads imply availability.
func TestDecodeAvailability(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    bool
		valid   bool
	}{
		{payload: `{"state":"online","extra":true}`, want: true, valid: true},
		{payload: `{"state":"offline"}`, want: false, valid: true},
		{payload: `{"state":"unknown"}`},
		{payload: `{}`},
		{payload: `null`},
		{payload: `not-json`},
	} {
		got, err := decodeAvailability([]byte(test.payload))
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"decodeAvailability(%s) = %t, %v; want %t, valid=%t",
				test.payload,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}
