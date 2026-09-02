package zigbee2mqtt //nolint:testpackage // Command tests exercise package-private matchers and route snapshots.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type fakeResponder struct {
	mutex       sync.Mutex
	recorder    *runtimeRecorder
	accepted    int
	rejected    int
	unavailable int
}

func (responder *fakeResponder) Accept() error {
	responder.mutex.Lock()
	responder.accepted++
	responder.mutex.Unlock()
	responder.recorder.add("accept")
	return nil
}

func (responder *fakeResponder) Reject(string) error {
	responder.mutex.Lock()
	responder.rejected++
	responder.mutex.Unlock()
	responder.recorder.add("reject")
	return nil
}

func (responder *fakeResponder) RejectUnavailable(string) error {
	responder.mutex.Lock()
	responder.unavailable++
	responder.mutex.Unlock()
	responder.recorder.add("unavailable")
	return nil
}

// This test protects typed facade dispatch, early post-dispatch matching, Accept-before-linked-observation, exact-once
// claiming, sibling ordinary projection, and the mandatory /set then /get publication order.
func TestCommandClaimsEarlyMatchExactlyOnceAfterAccept(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	var receivedAt time.Time
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if topic == "zigbee2mqtt/fixture-light/set" {
			receivedAt = time.Now().UTC()
			return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic:      "zigbee2mqtt/fixture-light",
				Payload:    []byte(`{"state":"ON","brightness":63.75}`),
				ReceivedAt: receivedAt,
			})
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
	if responder.accepted != 1 || responder.rejected != 0 || responder.unavailable != 0 {
		t.Fatalf("responder = %#v", responder)
	}
	if len(connection.published) != 2 || connection.published[0].topic != "zigbee2mqtt/fixture-light/set" ||
		connection.published[0].payload != `{"state":"ON"}` || connection.published[1].topic != "zigbee2mqtt/fixture-light/get" ||
		connection.published[1].payload != `{"state":""}` {
		t.Fatalf("publications = %#v", connection.published)
	}
	if len(session.observations) != 2 {
		t.Fatalf("observations = %#v", session.observations)
	}
	var linked, ordinary int
	for _, observation := range session.observations {
		if observation.RefreshForCommand == nil {
			ordinary++
			if observation.EntityID != device.entities[1].entityID {
				t.Fatalf("ordinary Observation claimed wrong property: %#v", observation)
			}
		} else {
			linked++
			if observation.EntityID != device.entities[0].entityID || *observation.RefreshForCommand != "cmd-test" {
				t.Fatalf("linked Observation = %#v", observation)
			}
		}
	}
	if linked != 1 || ordinary != 1 {
		t.Fatalf("linked=%d ordinary=%d", linked, ordinary)
	}
	assertOrdered(
		t,
		recorder.snapshot(),
		"mqtt:zigbee2mqtt/fixture-light/set",
		"accept",
		"mqtt:zigbee2mqtt/fixture-light/get",
		"linked-observation",
	)
}

// This test protects freshness and exact matching. It fails if retained replay or a fresh wrong target satisfies the
// Command instead of the non-retained matching report produced by active refresh.
func TestCommandRequiresFreshNonRetainedExactMatch(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	retainedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var freshAt time.Time
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		switch topic {
		case "zigbee2mqtt/fixture-light/set":
			for _, message := range []mqttMessage{
				{
					Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
					Retained: true, ReceivedAt: retainedAt,
				},
				{
					Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
					ReceivedAt: retainedAt.Add(time.Second),
				},
				{
					Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"OFF"}`),
					ReceivedAt: time.Now().UTC(),
				},
			} {
				if err := z2m.publishDeviceState(ctx, 1, device, message); err != nil {
					return err
				}
			}
			return nil
		case "zigbee2mqtt/fixture-light/get":
			freshAt = time.Now().UTC()
			return z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`), ReceivedAt: freshAt,
			})
		}
		return nil
	}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		&fakeResponder{recorder: recorder},
	); err != nil {
		t.Fatal(err)
	}
	var linked adapter.Observation
	for _, observation := range session.observations {
		if observation.RefreshForCommand != nil {
			linked = observation
		}
	}
	if linked.AdapterReceivedAt != freshAt.Format(time.RFC3339Nano) {
		t.Fatalf(
			"linked receive time = %q, want fresh %q; observations=%#v",
			linked.AdapterReceivedAt,
			freshAt,
			session.observations,
		)
	}
	if len(session.observations) != 4 {
		t.Fatalf(
			"observations = %#v, want retained, pre-dispatch, wrong ordinary, and fresh linked",
			session.observations,
		)
	}
}

// This test protects typed rejection on an unacknowledged /set. It fails if broker failure is reported as a generic
// upstream rejection, leaves routes enabled, or permits a stale matcher to survive.
func TestCommandSetFailureRejectsUnavailableAndDisablesRoutes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	connectionContext, cancelConnection := context.WithCancelCause(context.Background())
	defer cancelConnection(nil)
	z2m.mutex.Lock()
	z2m.connectionCancel = cancelConnection
	z2m.mutex.Unlock()
	connection.onPublish = func(context.Context, *fakeConnection, string, []byte) error {
		return errors.New("set PUBACK failed")
	}
	responder := &fakeResponder{recorder: recorder}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	); err != nil {
		t.Fatal(err)
	}
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if responder.unavailable != 1 || responder.accepted != 0 || z2m.healthy || len(z2m.routes) != 0 ||
		len(z2m.matchers) != 0 {
		t.Fatalf(
			"set failure responder=%#v healthy=%t routes=%d matchers=%d",
			responder,
			z2m.healthy,
			len(z2m.routes),
			len(z2m.matchers),
		)
	}
	cause := context.Cause(connectionContext)
	if cause == nil || !strings.Contains(cause.Error(), "set PUBACK failed") {
		t.Fatalf("connection cancellation cause = %v", cause)
	}
}

// This test protects held early State and command-local cancellation. It fails if an unacknowledged set drops valid
// ordinary evidence or if one expired Command tears down an otherwise healthy broker route.
//
//nolint:gocognit // The shared setup compares two distinct failure classifications.
func TestCommandSetFailurePreservesHeldStateAndContextFailureDoesNotDisconnect(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name            string
		publishErr      error
		wantHealthy     bool
		wantObservation bool
	}{
		{name: "broker failure", publishErr: errors.New("PUBACK failed"), wantObservation: true},
		{name: "command deadline", publishErr: context.DeadlineExceeded, wantHealthy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, connection, device := commandReadyAdapter(t, recorder, session)
			connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
				if topic == "zigbee2mqtt/fixture-light/set" && test.wantObservation {
					if err := z2m.publishDeviceState(ctx, 1, device, mqttMessage{
						Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
						ReceivedAt: time.Now().UTC(),
					}); err != nil {
						return err
					}
				}
				return test.publishErr
			}
			responder := &fakeResponder{recorder: recorder}
			if err := z2m.HandleCommand(
				context.Background(),
				testCommand(device.entities[0].entityID, `{"value":true}`),
				responder,
			); err != nil {
				t.Fatal(err)
			}
			z2m.mutex.Lock()
			healthy := z2m.healthy
			z2m.mutex.Unlock()
			if healthy != test.wantHealthy || responder.unavailable != 1 {
				t.Fatalf("healthy=%t responder=%#v", healthy, responder)
			}
			if got := len(session.observations) == 1; got != test.wantObservation {
				t.Fatalf("ordinary held Observation present=%t, want %t", got, test.wantObservation)
			}
			if test.wantObservation && session.observations[0].RefreshForCommand != nil {
				t.Fatalf("held State was incorrectly linked: %#v", session.observations[0])
			}
		})
	}
}

// This test protects accepted-Command behavior when active refresh publication fails. It fails if /get failure removes
// the matcher and prevents a later natural non-retained State report from satisfying the Command.
func TestCommandGetFailureStillAllowsNaturalMatch(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if topic != "zigbee2mqtt/fixture-light/get" {
			return nil
		}
		go func() {
			_ = z2m.publishDeviceState(ctx, 1, device, mqttMessage{
				Topic: "zigbee2mqtt/fixture-light", Payload: []byte(`{"state":"ON"}`),
				ReceivedAt: time.Now().UTC(),
			})
		}()
		return errors.New("refresh PUBACK failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := z2m.HandleCommand(
		ctx,
		testCommand(device.entities[0].entityID, `{"value":true}`),
		&fakeResponder{recorder: recorder},
	); err != nil {
		t.Fatal(err)
	}
	if len(session.observations) != 1 || session.observations[0].RefreshForCommand == nil {
		t.Fatalf("natural match was not linked after /get failure: %#v", session.observations)
	}
}

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

func commandReadyAdapter(
	t *testing.T,
	recorder *runtimeRecorder,
	session *fakeSession,
) (*Adapter, *fakeConnection, runtimeDevice) {
	t.Helper()
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	connection := newFakeConnection(recorder)
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	binding := adapter.Binding{BindingKey: device.Registration.BindingKey, DeviceID: "dev-test"}
	for _, entity := range device.Entities {
		binding.Entities = append(
			binding.Entities,
			adapter.EntityBinding{
				Key:      entity.Descriptor.Key,
				EntityID: "entity-" + entity.Descriptor.Key,
				Enabled:  true,
			},
		)
	}
	runtime, err := runtimeDeviceFromBinding(device, binding)
	if err != nil {
		t.Fatal(err)
	}
	z2m.mutex.Lock()
	z2m.generation = 1
	z2m.connection = connection
	z2m.mutex.Unlock()
	routes := make(map[string]commandRoute)
	for _, entity := range runtime.entities {
		routes[entity.entityID] = commandRoute{
			entityID: entity.entityID, ieeeAddress: runtime.ieeeAddress, friendlyName: runtime.friendly,
			entity: entity.discovered, connectionGeneration: 1,
		}
	}
	z2m.installSnapshot(1, routeSnapshot{routes: routes, devices: map[string]runtimeDevice{runtime.friendly: runtime}})
	return z2m, connection, runtime
}

func testCommand(entityID, parameters string) adapter.Command {
	return adapter.Command{
		ID: "cmd-test", CorrelationID: "cor-test", EntityID: entityID, OperationName: "set",
		Parameters: json.RawMessage(parameters), Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
}
