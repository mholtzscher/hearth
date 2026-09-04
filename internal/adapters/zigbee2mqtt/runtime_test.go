package zigbee2mqtt //nolint:testpackage // Tests exercise the private runtime state machine.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects absolute queue deadlines. It fails if an expired queued command reaches MQTT or blocks later cleanup.
func TestQueuedCommandDeadlineSkipsMQTT(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var sets atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			if sets.Add(1) == 1 {
				close(firstStarted)
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		return nil
	}
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- z2m.HandleCommand(
			context.Background(),
			testCommand(device.entities[0].entityID, `{"value":true}`),
			newFakeResponder(recorder, session),
		)
	}()
	<-firstStarted
	queued := testCommand(device.entities[1].entityID, `{"value":50}`)
	queued.Deadline = time.Now().Add(20 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	queuedResult := make(chan error, 1)
	go func() {
		queuedResult <- z2m.HandleCommand(context.Background(), queued, newFakeResponder(recorder, session))
	}()
	select {
	case err := <-queuedResult:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("queued error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued deadline was not observed")
	}
	if sets.Load() != 1 {
		t.Fatalf("set count = %d, expired queued command reached MQTT", sets.Load())
	}
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
}

// This test protects failed acceptance: a held report falls back ordinarily once and no evidence publication starts.
func TestAcceptanceFailureFallsBackHeldState(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			return publishState(ctx, z2m, device, `{"state":"ON"}`, time.Now().UTC())
		}
		return nil
	}
	acceptErr := errors.New("acceptance publication failed")
	responder := newFakeResponder(recorder, session)
	responder.acceptErr = acceptErr
	err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		responder,
	)
	if !errors.Is(err, acceptErr) {
		t.Fatalf("handler error = %v", err)
	}
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.observations) == 1
	})
	if len(session.linked) != 0 {
		t.Fatalf("failed acceptance linked State: %#v", session.linked)
	}
}

// This test protects ambiguous linked persistence: after linked publication starts, a failure must not create an ordinary
// duplicate, dispatch queued work, or leave the Adapter runtime alive.
func TestLinkedPublicationFailureNeverFallsBack(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	terminalErr := errors.New("session fenced")
	linkedStarted := make(chan struct{})
	releaseLinked := make(chan struct{})
	session.publishHook = func(ctx context.Context, _ adapter.Observation, linked bool) error {
		if linked {
			close(linkedStarted)
			select {
			case <-releaseLinked:
				return terminalErr
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	var sets atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			sets.Add(1)
			return publishState(ctx, z2m, device, `{"state":"ON"}`, time.Now().UTC())
		}
		return nil
	}
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		newFakeResponder(recorder, session),
	); err != nil {
		t.Fatal(err)
	}
	<-linkedStarted
	queuedResult := make(chan error, 1)
	z2m.runtimeEvents <- commandSubmitted{
		ctx:       context.Background(),
		command:   testCommand(device.entities[1].entityID, `{"value":50}`),
		responder: newFakeResponder(recorder, session),
		result:    queuedResult,
	}
	close(releaseLinked)
	select {
	case <-z2m.runtimeDone:
	case <-time.After(time.Second):
		t.Fatal("terminal linked publication did not stop runtime")
	}
	if len(session.observations) != 0 {
		t.Fatalf("linked failure produced ordinary duplicate: %#v", session.observations)
	}
	if sets.Load() != 1 {
		t.Fatalf("terminal linked failure dispatched %d set publications, want one", sets.Load())
	}
	select {
	case err := <-queuedResult:
		if !errors.Is(err, terminalErr) {
			t.Fatalf("queued handler error = %v, want terminal publication error", err)
		}
	default:
		t.Fatal("queued handler was not released by terminal runtime failure")
	}
}

// This test protects stale attempt completions after route recovery. It fails if the old /set PUBACK accepts or removes
// a newer command routed through the replacement snapshot.
func TestStaleSetCompletionCannotChangeRecoveredRoute(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, _, connection, device := commandReadyAdapter(t, recorder, session)
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	var sets atomic.Int32
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") && sets.Add(1) == 1 {
			close(oldStarted)
			select {
			case <-releaseOld:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	oldResponder := newFakeResponder(recorder, session)
	oldResult := make(chan error, 1)
	go func() {
		oldResult <- z2m.HandleCommand(
			context.Background(),
			testCommand(device.entities[0].entityID, `{"value":true}`),
			oldResponder,
		)
	}()
	<-oldStarted
	invalidated := make(chan error, 1)
	z2m.runtimeEvents <- routesInvalidated{generation: 1, result: invalidated}
	<-invalidated
	if err := <-oldResult; err != nil {
		t.Fatal(err)
	}
	routes := make(map[string]commandRoute)
	for _, entity := range device.entities {
		routes[entity.entityID] = commandRoute{
			entityID: entity.entityID, ieeeAddress: device.ieeeAddress, friendlyName: device.friendly,
			entity: entity,
		}
	}
	activated := make(chan routeActivationResult, 1)
	z2m.runtimeEvents <- routesActivated{
		generation: 2,
		connection: connection,
		disconnect: func(error) {},
		snapshot:   routeSnapshot{routes: routes, devices: map[string]runtimeDevice{device.friendly: device}},
		result:     activated,
	}
	if result := <-activated; result.err != nil {
		t.Fatal(result.err)
	}
	close(releaseOld)
	newResponder := newFakeResponder(recorder, session)
	if err := z2m.HandleCommand(
		context.Background(),
		testCommand(device.entities[0].entityID, `{"value":true}`),
		newResponder,
	); err != nil {
		t.Fatal(err)
	}
	if oldResponder.accepted != 0 || newResponder.accepted != 1 || sets.Load() != 2 {
		t.Fatalf("old=%#v new=%#v sets=%d", oldResponder, newResponder, sets.Load())
	}
}

// This test protects the absolute response deadline when a successful /set completion is processed late.
func TestLateSetCompletionDoesNotAccept(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	coordinator := newRuntimeCoordinator(context.Background(), z2m)
	t.Cleanup(coordinator.cancel)
	responder := newFakeResponder(recorder, session)
	result := make(chan error, 1)
	mqttContext, cancelMQTT := context.WithCancel(context.Background())
	attempt := &commandAttempt{
		id: 1, command: testCommand("entity-power", `{"value":true}`), responder: responder,
		handlerResult: result,
		route:         commandRoute{entityID: "entity-power", ieeeAddress: "0x1"},
		deadline:      time.Now().Add(-time.Second), phase: commandPublishingSet,
		mqttContext: mqttContext, cancelMQTT: cancelMQTT,
	}
	coordinator.attempts[attempt.id] = attempt
	coordinator.deviceQueues[attempt.route.ieeeAddress] = &deviceCommandQueue{active: attempt}
	if err := coordinator.finishSet(setPublishFinished{attemptID: attempt.id}); err != nil {
		t.Fatal(err)
	}
	if responder.accepted != 0 {
		t.Fatalf("late /set completion accepted %d times", responder.accepted)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handler error = %v, want deadline exceeded", err)
	}
}

// This test protects attempt phase fencing. A late fallback completion must not terminate an attempt whose /set is
// still waiting for PUBACK.
func TestStaleFallbackCompletionCannotFinishPublishingSet(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m, coordinator, connection, device := commandReadyAdapter(t, recorder, session)
	setStarted := make(chan struct{})
	releaseSet := make(chan struct{})
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
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
	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- z2m.HandleCommand(
			context.Background(),
			testCommand(device.entities[0].entityID, `{"value":true}`),
			responder,
		)
	}()
	<-setStarted
	attempt := coordinator.matchers[device.entities[0].entityID]
	if attempt == nil {
		t.Fatal("matcher was not installed before /set")
	}
	z2m.runtimeEvents <- fallbackPublishFinished{attemptID: attempt.id}
	close(releaseSet)
	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stale fallback completion consumed active attempt")
	}
	if responder.accepted != 1 {
		t.Fatalf("accept count = %d", responder.accepted)
	}
}

// This test protects shutdown cleanup: handler and MQTT effect both observe cancellation and the coordinator joins the
// effect before announcing runtime completion.
func TestCoordinatorShutdownUnblocksHandlerAndJoinsEffects(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	ctx, cancel := context.WithCancel(context.Background())
	coordinator := newRuntimeCoordinator(ctx, z2m)
	go func() { _ = coordinator.run() }()
	connection := newFakeConnection(recorder)
	effectStarted := make(chan struct{})
	effectDone := make(chan struct{})
	connection.onPublish = func(ctx context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/set") {
			close(effectStarted)
			<-ctx.Done()
			close(effectDone)
			return ctx.Err()
		}
		return nil
	}
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	binding := adapter.Binding{BindingKey: device.Registration.BindingKey, DeviceID: "dev-test"}
	for _, entity := range device.Entities {
		binding.Entities = append(binding.Entities, adapter.EntityBinding{
			Key: entity.Descriptor.Key, EntityID: "entity-" + entity.Descriptor.Key, Enabled: true,
		})
	}
	runtime, err := runtimeDeviceFromBinding(device, binding)
	if err != nil {
		t.Fatal(err)
	}
	route := commandRoute{
		entityID:     runtime.entities[0].entityID,
		ieeeAddress:  runtime.ieeeAddress,
		friendlyName: runtime.friendly,
		entity:       runtime.entities[0],
	}
	activated := make(chan routeActivationResult, 1)
	z2m.runtimeEvents <- routesActivated{
		generation: 1,
		connection: connection,
		disconnect: func(error) {},
		snapshot:   routeSnapshot{routes: map[string]commandRoute{route.entityID: route}},
		result:     activated,
	}
	<-activated
	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- z2m.HandleCommand(
			context.Background(),
			testCommand(route.entityID, `{"value":true}`),
			newFakeResponder(recorder, session),
		)
	}()
	<-effectStarted
	cancel()
	select {
	case <-z2m.runtimeDone:
	case <-time.After(time.Second):
		t.Fatal("coordinator did not stop")
	}
	select {
	case <-effectDone:
	default:
		t.Fatal("runtime completed before MQTT effect exited")
	}
	select {
	case err = <-handlerDone:
		if err == nil {
			t.Fatal("handler unexpectedly succeeded during shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("handler remained blocked after shutdown")
	}
}

// This test protects matcher gate ordering and fails if stale or late evidence
// invokes the outcome closure. The matcher would return true for both
// candidates, so a call count above zero proves the closure ran before the
// post-dispatch and deadline gates.
func TestMatcherSkippedForStaleAndLateEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		receivedAt func(dispatchedAt time.Time, deadline time.Time) time.Time
	}{
		{name: "stale", receivedAt: func(dispatchedAt time.Time, _ time.Time) time.Time {
			return dispatchedAt
		}},
		{name: "late", receivedAt: func(_ time.Time, deadline time.Time) time.Time {
			return deadline.Add(time.Second)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m := newRuntimeAdapter(t, session, &fakeDialer{})
			coordinator := newRuntimeCoordinator(context.Background(), z2m)
			t.Cleanup(coordinator.cancel)
			now := time.Now().UTC()
			deadline := now.Add(time.Minute)
			dispatchedAt := now
			calls := 0
			attempt := &commandAttempt{
				id: 1, generation: 1, routeRevision: 1,
				command: testCommand("entity-power", `{"value":true}`),
				matches: func(stateReport) bool {
					calls++
					return true
				},
				deadline: deadline, dispatchedAt: dispatchedAt, phase: commandAwaitingEvidence,
			}
			coordinator.matchers[attempt.command.EntityID] = attempt
			coordinator.attempts[attempt.id] = attempt
			event := stateCandidate{
				ctx: context.Background(), generation: 1, routeRevision: 1,
				entityID:   attempt.command.EntityID,
				report:     stateReport{},
				receivedAt: test.receivedAt(dispatchedAt, deadline),
				result:     make(chan stateDisposition, 1),
			}
			if err := coordinator.handleState(event); err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatalf("%s evidence invoked matcher %d times, want zero", test.name, calls)
			}
			if attempt.claimed != nil {
				t.Fatalf("%s evidence claimed the attempt", test.name)
			}
		})
	}
}
