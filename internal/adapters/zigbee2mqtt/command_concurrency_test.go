package zigbee2mqtt //nolint:testpackage // Command tests exercise the private runtime state machine.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This deterministic concurrency test protects per-IEEE FIFO ownership until evidence disposition completes, including
// after the first command handler has returned.
func TestCommandsForSameIEEEStaySerializedUntilDisposition(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	firstLinked := make(chan struct{})
	releaseFirstLinked := make(chan struct{})
	var linkedCount atomic.Int32
	session.publishHook = func(ctx context.Context, _ adapter.Observation, linked bool) error {
		if linked && linkedCount.Add(1) == 1 {
			close(firstLinked)
			select {
			case <-releaseFirstLinked:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	var setCount atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, payload []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		setCount.Add(1)
		return publishState(ctx, z2m, device, string(payload), time.Now().UTC())
	}
	results := make(chan error, 2)
	go func() {
		results <- z2m.HandleCommand(
			context.Background(),
			testCommand(device.entities[0].entityID, `{"value":true}`),
			newFakeResponder(recorder, session),
		)
	}()
	select {
	case err := <-results:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first handler did not return after acceptance")
	}
	<-firstLinked
	secondResult := make(chan error, 1)
	z2m.runtimeEvents <- commandSubmitted{
		ctx:       context.Background(),
		command:   testCommand(device.entities[1].entityID, `{"value":50}`),
		responder: newFakeResponder(recorder, session),
		result:    secondResult,
	}
	if err := publishState(
		context.Background(),
		z2m,
		device,
		`{"brightness":50}`,
		time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if setCount.Load() != 1 {
		t.Fatalf("same-IEEE set publications overlapped: %d", setCount.Load())
	}
	select {
	case err := <-secondResult:
		t.Fatalf("second same-IEEE Command completed before first disposition: %v", err)
	default:
	}
	close(releaseFirstLinked)
	if err := <-secondResult; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return setCount.Load() == 2 })
}

// This test protects queue granularity: independent IEEE Devices may wait on MQTT PUBACK concurrently.
func TestCommandsForDifferentIEEEDevicesOverlap(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, coordinator, connection, first := commandReadyAdapter(t, recorder, session)
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
	activation := make(chan routeActivationResult, 1)
	z2m.runtimeEvents <- routesActivated{
		generation: 1,
		connection: connection,
		disconnect: func(error) {},
		snapshot: routeSnapshot{
			routes:  routes,
			devices: map[string]runtimeDevice{first.friendly: first, second.friendly: second},
		},
		result: activation,
	}
	if result := <-activation; result.err != nil {
		t.Fatal(result.err)
	}
	_ = coordinator

	bothStarted := make(chan struct{})
	release := make(chan struct{})
	var started atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if !strings.HasSuffix(topic, "/set") {
			return nil
		}
		if started.Add(1) == 2 {
			close(bothStarted)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	results := make(chan error, 2)
	for _, entityID := range []string{first.entities[0].entityID, second.entities[0].entityID} {
		go func() {
			results <- z2m.HandleCommand(
				context.Background(),
				testCommand(entityID, `{"value":true}`),
				newFakeResponder(recorder, session),
			)
		}()
	}
	select {
	case <-bothStarted:
	case <-time.After(time.Second):
		t.Fatal("different-IEEE Commands did not overlap")
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

// This test protects invalidation acknowledgement: routes become unavailable immediately, unaccepted work is rejected,
// and a claimed pre-link report receives exactly one ordinary fallback.
func TestRouteInvalidationRejectsAndFallsBack(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	setStarted := make(chan struct{})
	releaseSet := make(chan struct{})
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			if err := publishState(ctx, z2m, device, `{"state":"ON"}`, time.Now().UTC()); err != nil {
				return err
			}
			close(setStarted)
			select {
			case <-releaseSet:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	responder := newFakeResponder(recorder, session)
	result := make(chan error, 1)
	go func() {
		result <- z2m.HandleCommand(
			context.Background(),
			testCommand(device.entities[0].entityID, `{"value":true}`),
			responder,
		)
	}()
	<-setStarted
	invalidated := make(chan error, 1)
	z2m.runtimeEvents <- routesInvalidated{generation: 1, cause: context.Canceled, result: invalidated}
	if err := <-invalidated; err != nil {
		t.Fatal(err)
	}
	close(releaseSet)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.observations) == 1
	})
	if responder.unavailable != 1 || responder.accepted != 0 || len(session.linked) != 0 {
		t.Fatalf("responder=%#v ordinary=%#v linked=%#v", responder, session.observations, session.linked)
	}
	missing := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		missing,
	); err != nil {
		t.Fatal(err)
	}
	if missing.unavailable != 1 {
		t.Fatalf("missing route responder = %#v", missing)
	}
}
