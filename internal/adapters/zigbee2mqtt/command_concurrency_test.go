package zigbee2mqtt //nolint:testpackage // Command tests exercise package-private matchers and route snapshots.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This deterministic concurrency test protects the per-IEEE context lock. It fails if power and brightness Commands for
// one physical Device publish concurrently or if lock waiting bypasses the existing Command context.
func TestCommandsForSameIEEEAreSerialized(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var setCount atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if topic != "zigbee2mqtt/fixture-light/set" {
			return nil
		}
		count := setCount.Add(1)
		if count == 1 {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
			Topic: "zigbee2mqtt/fixture-light", Payload: append([]byte(nil), payload...), ReceivedAt: time.Now().UTC(),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		results <- z2m.HandleCommand(ctx, testCommand(device.entities[0].entityID, `{"value":true}`), &fakeResponder{recorder: recorder})
	}()
	<-firstStarted
	go func() {
		results <- z2m.HandleCommand(ctx, testCommand(device.entities[1].entityID, `{"value":50}`), &fakeResponder{recorder: recorder})
	}()
	select {
	case <-time.After(30 * time.Millisecond):
		if setCount.Load() != 1 {
			t.Fatalf("same-IEEE set publications overlapped: %d", setCount.Load())
		}
	case err := <-results:
		t.Fatalf("second same-IEEE Command completed while first was blocked: %v", err)
	}
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if setCount.Load() != 2 {
		t.Fatalf("set publication count = %d", setCount.Load())
	}
}

// This test protects lock granularity. It fails if a global lock prevents Commands for independent IEEE Devices from
// reaching MQTT concurrently.
func TestCommandsForDifferentIEEEDevicesOverlap(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, first := commandReadyAdapter(t, recorder, session)
	second := first
	second.ieeeAddress = "0x0000000000000002"
	second.friendly = "other-light"
	second.entities = append([]runtimeEntity(nil), first.entities...)
	for index := range second.entities {
		second.entities[index].entityID += "-other"
	}
	routes := make(map[string]commandRoute)
	for _, device := range []runtimeDevice{first, second} {
		for _, entity := range device.entities {
			routes[entity.entityID] = commandRoute{
				entityID: entity.entityID, ieeeAddress: device.ieeeAddress, friendlyName: device.friendly,
				entity: entity.discovered, connectionGeneration: 1,
			}
		}
	}
	z2m.installSnapshot(1, routeSnapshot{
		routes:  routes,
		devices: map[string]runtimeDevice{first.friendly: first, second.friendly: second},
	})
	bothStarted := make(chan struct{})
	release := make(chan struct{})
	var started atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		if started.Add(1) == 2 {
			close(bothStarted)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		device := first
		if strings.Contains(topic, "/other-light/") {
			device = second
		}
		return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
			Topic: strings.TrimSuffix(
				topic,
				"/set",
			),
			Payload:    append([]byte(nil), payload...),
			ReceivedAt: time.Now().UTC(),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for _, entityID := range []string{first.entities[0].entityID, second.entities[0].entityID} {
		go func() {
			results <- z2m.HandleCommand(ctx, testCommand(entityID, `{"value":true}`), &fakeResponder{recorder: recorder})
		}()
	}
	select {
	case <-bothStarted:
	case <-ctx.Done():
		t.Fatal("different-IEEE Commands did not overlap")
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

// This test protects cancellation of an already-claimed report and fails if disconnect races publish it as linked or drops it.
func TestUnhealthyTransitionPublishesClaimedStateAsOrdinary(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		switch topic {
		case "zigbee2mqtt/fixture-light/set":
			return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
				ReceivedAt: time.Now().UTC(),
			})
		case "zigbee2mqtt/fixture-light/get":
			z2m.disableRoutes()
		}
		return nil
	}
	responder := &fakeResponder{recorder: recorder}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.accepted != 1 || len(session.observations) != 1 ||
		session.observations[0].RefreshForCommand != nil {
		t.Fatalf("responder=%#v observations=%#v", responder, session.observations)
	}
}

// This test protects typed entity_unavailable rejection and matcher cancellation when health removes route snapshots.
func TestUnhealthyTransitionCancelsCommandAndRejectsMissingRoute(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	accepted := make(chan struct{})
	connection.onPublish = func(_ context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if topic == "zigbee2mqtt/fixture-light/get" {
			close(accepted)
		}
		return nil
	}
	result := make(chan error, 1)
	go func() {
		result <- z2m.HandleCommand(context.Background(), testCommand(device.entities[0].entityID, `{"value":true}`), &fakeResponder{recorder: recorder})
	}()
	<-accepted
	z2m.disableRoutes()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	responder := &fakeResponder{recorder: recorder}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	if responder.unavailable != 1 || responder.accepted != 0 {
		t.Fatalf("missing route responder = %#v", responder)
	}
}
