package zwavejs //nolint:testpackage // Regression tests exercise the private coordinator and planning seams.

// runtime_regression_test.go protects cross-component protocol and coordinator
// boundaries that previously admitted stale routes, State, or availability.
// Each test is named for the behavior it protects:
//
//	1 overflow             TestRuntimeOverflowBeforeActivationTerminatesTheIncomingGeneration
//	                       TestRuntimeTerminatedIncomingGenerationCannotActivate
//	2 superseded register  TestRuntimeSupersededRefreshRegistrationIsRememberedUnavailable
//	3 refresh timeout      TestRuntimeUnansweredNodeRefreshTimesOutAndDropsTheGeneration
//	                       TestRuntimeSlowRegistrationOutlivesTheNodeRefreshTimeout
//	4 keyed event          TestDecodeValueEventArgsRejectsKeyedValues
//	5 value added replay   TestRuntimeValueAddedForANewPlanPublishesAfterRefresh
//	                       TestValueAddedReplayQueueIsBoundedAndCleared
//	6 negative endpoint    TestPlanNetworkIsolatesNegativeEndpointValues
//	7 keyed snapshot       TestResolveCurrentValueRequiresExactUnkeyedIdentity
//	8 replay retention     TestRuntimeValueAddedCurrentBeforeTargetPublishesAfterPlanCompletes
//	                       TestRuntimeValueAddedReplaySurvivesARejectedIntermediateRefresh
//	                       TestValueAddedCarriesStateOnlyPlannedCurrentValues
//	9 status supersede     TestRuntimeStatusEventSchedulesReplacementRefreshAfterSupersededRegistration
//	10 stale replay        TestRuntimeValueUpdatedSupersedesQueuedValueAddedBeforeRegistration
//	11 node-scoped routes  TestRuntimeValueReportResolvesOnlyItsOwnNodesRoute
//	12 snapshot filter     TestRuntimeStartupSkipsStateForUnroutableAsleepSnapshot

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects the pre-activation overflow boundary, and fails if Events
// that overflow before an incoming generation is installed drop only the active
// generation, leaving the incoming connection to activate a stale snapshot.
func TestRuntimeOverflowBeforeActivationTerminatesTheIncomingGeneration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)

	// Hold the incoming reconciliation in Register, so its Events arrive before
	// its snapshot is activated.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.registerHook = func(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return adapter.Binding{}, ctx.Err()
		case <-release:
			return canonicalBinding(registration), nil
		}
	}
	reconciledRuntime(t, session, connection)

	awaitSignal(t, entered, "the incoming reconciliation to register")
	waitFor(t, "the incoming Event buffer to overflow", func() bool {
		for range 8 {
			select {
			case connection.events <- receivedEvent{
				Event:      nodeEvent(eventAlive),
				ReceivedAt: time.Now().UTC(),
			}:
			default:
			}
		}
		return session.logs.has("adapter.event_buffer_overflowed")
	})

	// With the defect the incoming connection keeps running and later installs
	// its stale snapshot once Register is released.
	close(release)
	waitFor(t, "the terminated incoming generation to be reported unhealthy", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})
	time.Sleep(testQuietPeriod)
	if session.logs.has("adapter.reconcile_completed") {
		t.Fatal("a generation whose Events overflowed activated its stale snapshot")
	}
	if got := len(session.recordedObservations()); got != 0 {
		t.Fatalf("Observations = %d, want 0 from a terminated generation", got)
	}
}

// This test protects the terminated-generation guard, and fails if a
// reconciliation for a generation whose Events already overflowed can still
// install its stale snapshot.
func TestRuntimeTerminatedIncomingGenerationCannotActivate(t *testing.T) {
	t.Parallel()
	zwave := newRuntimeAdapter(t, newRuntimeSession(&runtimeRecorder{}), &fakeDialer{})
	coordinator := newRuntimeCoordinator(t.Context(), zwave)

	connection := newFakeConnection(
		&runtimeRecorder{},
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(testNodeID, "Kitchen Switch")),
	)
	var terminatedCause error
	disconnect := func(cause error) { terminatedCause = cause }
	for index := range bufferedEventLimit + 1 {
		if err := coordinator.handle(upstreamEvent{
			generation: 1,
			connection: connection,
			disconnect: disconnect,
			event:      receivedEvent{Event: nodeEvent(eventAlive), ReceivedAt: time.Now().UTC()},
		}); err != nil {
			t.Fatalf("overflow Event %d = %v, want it handled", index, err)
		}
	}
	if terminatedCause == nil {
		t.Fatal("the incoming generation was not terminated")
	}
	if _, ok := errors.AsType[*eventBufferOverflowError](terminatedCause); !ok {
		t.Fatalf("termination cause = %v, want an Event buffer overflow", terminatedCause)
	}
	select {
	case <-connection.closed:
	case <-time.After(harnessTimeout):
		t.Fatal("the incoming connection was not closed")
	}

	result := make(chan error, 1)
	if err := coordinator.handle(reconciliationSubmitted{
		generation: 1,
		connection: connection,
		disconnect: disconnect,
		homeID:     testHomeID,
		observedAt: time.Now().UTC(),
		result:     result,
	}); err != nil {
		t.Fatalf("reconciliation handle = %v", err)
	}
	select {
	case err := <-result:
		if _, ok := errors.AsType[*eventBufferOverflowError](err); !ok {
			t.Fatalf("reconciliation result = %v, want an Event buffer overflow", err)
		}
	case <-time.After(harnessTimeout):
		t.Fatal("the terminated reconciliation was not refused")
	}
	if coordinator.scope != nil || len(coordinator.snapshot.ByEntityID) != 0 {
		t.Fatal("a terminated generation installed routes")
	}
}

// This test protects the refresh registration boundary, and fails if a
// Registration that committed after its refresh was superseded leaves unknown
// mappings behind: the returned bindings must be remembered, reported in their
// current unavailable state, and must never install the stale routes.
func TestRuntimeSupersededRefreshRegistrationIsRememberedUnavailable(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(testNodeID, "Kitchen Switch")),
	)

	var registers atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.registerHook = func(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
		if registers.Add(1) == 1 {
			return canonicalBinding(registration), nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return adapter.Binding{}, ctx.Err()
		case <-release:
			return canonicalBinding(registration), nil
		}
	}
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	// The refreshed node gains endpoint 1, so its refreshed registration creates
	// a mapping that the active routes never held.
	refreshed := switchNodeFixture(testNodeID, "Kitchen Switch")
	refreshed.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	refreshed.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))
	connection.setNodeState(refreshed)
	connection.emit(nodeEvent(eventMetadataUpdated))
	awaitSignal(t, entered, "the refreshed registration")

	// A sleep supersedes the in-flight refresh while Register is blocked.
	connection.emit(nodeEvent(eventSleep))
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "sleep availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.ReasonCode == nodeAsleepReason
	})
	close(release)

	// The committed mapping is remembered and reported in the node's current
	// unavailable state, which the superseding sleep Event made authoritative.
	ep1ID := routeEntityID(testNodeID, "power-ep1")
	waitFor(t, "the superseded registration's mapping to be reported unavailable", func() bool {
		report, ok := session.lastAvailability(ep1ID)
		return ok && report.Status == adapter.AvailabilityUnavailable
	})
	report, _ := session.lastAvailability(ep1ID)
	if report.ReasonCode != nodeAsleepReason {
		t.Fatalf("superseded mapping reason = %q, want %q", report.ReasonCode, nodeAsleepReason)
	}

	// The stale registration never installed a route, so its Entity cannot be
	// commanded.
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(ep1ID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want 0/1/1", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("writes = %d, want 0 (a superseded registration must not install routes)", got)
	}
}

// This test protects the bounded node refresh, and fails if an unanswered
// node.get_state can hold its node's stale routes for the whole generation
// instead of ending and reconciling that generation.
func TestRuntimeUnansweredNodeRefreshTimesOutAndDropsTheGeneration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(testNodeID, "Kitchen Switch")),
	)
	connection.getStateHook = func(ctx context.Context, _ int) (nodeState, error) {
		<-ctx.Done()
		return nodeState{}, ctx.Err()
	}
	dialer := &fakeDialer{connections: []*fakeConnection{connection}}
	zwave := newRuntimeAdapter(t, session, dialer)
	zwave.refreshTimeout = 20 * time.Millisecond
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	connection.emit(nodeEvent(eventMetadataUpdated))
	waitFor(t, "the refresh timeout to end the generation", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})

	// The timed-out generation has no routes left, so a Command is rejected
	// instead of dispatching through stale routes.
	powerID := routeEntityID(testNodeID, "power")
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want 0/1/1", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("writes = %d, want 0", got)
	}
}

// This test protects the elapsed-timeout boundary, and fails if a slow SDK
// Register is bounded by the node refresh timeout and aborts a registration that
// would have succeeded.
func TestRuntimeSlowRegistrationOutlivesTheNodeRefreshTimeout(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(testNodeID, "Kitchen Switch")),
	)

	// The refreshed node gains endpoint 1, so only a completed registration can
	// install its route.
	refreshed := switchNodeFixture(testNodeID, "Kitchen Switch")
	refreshed.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	refreshed.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))
	connection.setNodeState(refreshed)

	var registers atomic.Int64
	session.registerHook = func(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
		if registers.Add(1) == 1 {
			return canonicalBinding(registration), nil
		}
		timer := time.NewTimer(3 * 25 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			// The elapsed refresh timeout must not reach Register.
			return adapter.Binding{}, ctx.Err()
		case <-timer.C:
			return canonicalBinding(registration), nil
		}
	}
	dialer := &fakeDialer{connections: []*fakeConnection{connection}}
	zwave := newRuntimeAdapter(t, session, dialer)
	zwave.refreshTimeout = 25 * time.Millisecond
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	connection.emit(nodeEvent(eventMetadataUpdated))
	ep1ID := routeEntityID(testNodeID, "power-ep1")
	waitFor(t, "the slow registration to install the refreshed route", func() bool {
		report, ok := session.lastAvailability(ep1ID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
	if recorder.has("health:unhealthy:" + externalSystemUnavailableReason) {
		t.Fatal("a slow Register was bounded by the node refresh timeout")
	}

	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(ep1ID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want 1/0/1", accepted, rejected, total)
	}
}

// This test protects the value added replay bound, and fails if a burst of
// frames that have no route yet can grow the queue without the Event bound, or
// if ending a generation leaves queued frames behind for the next one.
func TestValueAddedReplayQueueIsBoundedAndCleared(t *testing.T) {
	t.Parallel()
	zwave := newRuntimeAdapter(t, newRuntimeSession(&runtimeRecorder{}), &fakeDialer{})
	coordinator := newRuntimeCoordinator(t.Context(), zwave)
	received := receivedEvent{
		Event:      valueUpdatedEvent(testValueID(commandClassBinarySwitch, 1, valuePropertyCurrentValue), "true"),
		ReceivedAt: time.Now().UTC(),
	}
	for index := range bufferedEventLimit {
		if !coordinator.queueValueAddedReplay(testNodeID, received) {
			t.Fatalf("value added replay slot %d was rejected before the Event bound", index)
		}
	}
	if coordinator.queueValueAddedReplay(testNodeID, received) {
		t.Fatal("the value added replay queue exceeded its Event bound")
	}

	coordinator.dropGeneration(errors.New("the generation ended"))
	if got := len(coordinator.pendingValueAdded); got != 0 {
		t.Fatalf("pending value added nodes after a dropped generation = %d, want 0", got)
	}
}

// This test protects the Event Value ID rule, and fails if a keyed value Event
// is accepted and aliases the unkeyed Value that shares its Command Class,
// endpoint, and property.
func TestDecodeValueEventArgsRejectsKeyedValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{
			name:    "unkeyed",
			payload: `{"commandClass":38,"endpoint":0,"property":"currentValue","newValue":40}`,
			want:    true,
		},
		{
			name:    "explicit null key",
			payload: `{"commandClass":38,"endpoint":0,"property":"currentValue","propertyKey":null,"newValue":40}`,
			want:    true,
		},
		{
			name:    "numeric key",
			payload: `{"commandClass":38,"endpoint":0,"property":"currentValue","propertyKey":2,"newValue":40}`,
			want:    false,
		},
		{
			name:    "string key",
			payload: `{"commandClass":38,"endpoint":0,"property":"currentValue","propertyKey":"night","newValue":40}`,
			want:    false,
		},
		{
			name:    "numeric property",
			payload: `{"commandClass":112,"endpoint":0,"property":4,"newValue":30}`,
			want:    false,
		},
		{
			name:    "missing property",
			payload: `{"commandClass":38,"endpoint":0,"newValue":40}`,
			want:    false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, ok := decodeValueEventArgs(json.RawMessage(testCase.payload))
			if ok != testCase.want {
				t.Fatalf("decodeValueEventArgs(%s) ok = %t, want %t", testCase.payload, ok, testCase.want)
			}
		})
	}
}

// This test protects the value-added plan boundary, and fails if a value added
// frame that creates a new plan is published before its registration and its
// State is lost, or if a frame whose route already exists is published twice.
func TestRuntimeValueAddedForANewPlanPublishesAfterRefresh(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(testNodeID, "Kitchen Switch")),
	)

	var registers atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.registerHook = func(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
		if registers.Add(1) == 1 {
			return canonicalBinding(registration), nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return adapter.Binding{}, ctx.Err()
		case <-release:
			return canonicalBinding(registration), nil
		}
	}
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	waitFor(t, "the snapshot Observation", func() bool { return recorder.count("observation:") == 1 })

	// The refreshed node gains endpoint 1, so the value added frame creates a
	// plan that has no route until the refresh installs it.
	refreshed := switchNodeFixture(testNodeID, "Kitchen Switch")
	refreshed.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	refreshed.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))
	connection.setNodeState(refreshed)

	added := valueUpdatedEvent(testValueID(testCommandClassBinarySwitch, 1, valuePropertyCurrentValue), "false")
	added.Event.Event = eventValueAdded
	receivedAt := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	connection.events <- receivedEvent{Event: added, ReceivedAt: receivedAt}

	awaitSignal(t, entered, "the refreshed registration")
	if got := recorder.count("observation:"); got != 1 {
		t.Fatalf("Observations before the refreshed routes = %d, want 1", got)
	}
	close(release)

	ep1ID := routeEntityID(testNodeID, "power-ep1")
	waitFor(t, "the replayed value added Observation", func() bool {
		_, ok := observationForEntity(session, ep1ID)
		return ok
	})
	observation, _ := observationForEntity(session, ep1ID)
	if got, want := string(observation.Value), "false"; got != want {
		t.Fatalf("replayed State = %q, want %q", got, want)
	}
	parsed, err := time.Parse(time.RFC3339Nano, observation.AdapterReceivedAt)
	if err != nil {
		t.Fatalf("AdapterReceivedAt = %q: %v", observation.AdapterReceivedAt, err)
	}
	if !parsed.Equal(receivedAt) {
		t.Fatalf("replayed receive time = %s, want %s", parsed, receivedAt)
	}
	if got := recorder.count("observation:"); got != 2 {
		t.Fatalf("Observations = %d, want 2 (no duplicate publication)", got)
	}
}

// This test protects the pre-route State ordering boundary, and fails if a newer
// value update for a queued value added frame is discarded because no route
// exists yet, so the replay publishes the stale added value instead of the
// newest report at the update's receive time.
func TestRuntimeValueUpdatedSupersedesQueuedValueAddedBeforeRegistration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(testNodeID, "Kitchen Switch")),
	)

	var registers atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.registerHook = func(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
		if registers.Add(1) == 1 {
			return canonicalBinding(registration), nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return adapter.Binding{}, ctx.Err()
		case <-release:
			return canonicalBinding(registration), nil
		}
	}
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	waitFor(t, "the snapshot Observation", func() bool { return recorder.count("observation:") == 1 })

	// The refreshed node gains endpoint 1, so the value added frame creates a
	// plan that has no route until the refresh installs it.
	refreshed := switchNodeFixture(testNodeID, "Kitchen Switch")
	refreshed.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	refreshed.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))
	connection.setNodeState(refreshed)

	// The value added frame reports false, then a newer value update reports true
	// for the same exact unkeyed Value before any route exists.
	addedAt := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	added := valueUpdatedEvent(testValueID(testCommandClassBinarySwitch, 1, valuePropertyCurrentValue), "false")
	added.Event.Event = eventValueAdded
	connection.events <- receivedEvent{Event: added, ReceivedAt: addedAt}

	awaitSignal(t, entered, "the refreshed registration")
	updatedAt := addedAt.Add(time.Second)
	updated := valueUpdatedEvent(testValueID(testCommandClassBinarySwitch, 1, valuePropertyCurrentValue), "true")
	connection.events <- receivedEvent{Event: updated, ReceivedAt: updatedAt}

	// A malformed value update behind the newer report has no Session boundary,
	// so its ignored diagnostic is the ordered barrier that proves the newer
	// report was consumed before the registration is released.
	ignoredBefore := session.logs.count("adapter.event_ignored")
	sentinel := keyedValueUpdatedEvent(
		testValueID(testCommandClassBinarySwitch, 1, valuePropertyCurrentValue), 2, "false",
	)
	connection.events <- receivedEvent{Event: sentinel, ReceivedAt: updatedAt.Add(time.Second)}
	waitFor(t, "the superseding update to be consumed", func() bool {
		return session.logs.count("adapter.event_ignored") > ignoredBefore
	})
	if got := recorder.count("observation:"); got != 1 {
		t.Fatalf("Observations before registration = %d, want 1 (the superseded report never publishes early)", got)
	}
	close(release)

	// Exactly one post-registration Observation carries the newest report at the
	// update's receive time; the stale added value is never published.
	ep1ID := routeEntityID(testNodeID, "power-ep1")
	waitFor(t, "the replayed superseding Observation", func() bool {
		_, ok := observationForEntity(session, ep1ID)
		return ok
	})
	observation, _ := observationForEntity(session, ep1ID)
	if got, want := string(observation.Value), "true"; got != want {
		t.Fatalf("replayed State = %q, want %q", got, want)
	}
	parsed, err := time.Parse(time.RFC3339Nano, observation.AdapterReceivedAt)
	if err != nil {
		t.Fatalf("AdapterReceivedAt = %q: %v", observation.AdapterReceivedAt, err)
	}
	if !parsed.Equal(updatedAt) {
		t.Fatalf("replayed receive time = %s, want the update time %s", parsed, updatedAt)
	}
	if got := recorder.count("observation:"); got != 2 {
		t.Fatalf("Observations = %d, want 2 (exactly one post-registration report)", got)
	}
}

// This test protects snapshot State resolution, and fails if a keyed snapshot
// Value that shares a Command Class, endpoint, and property with the planned
// unkeyed current Value is resolved in its place, or if an explicit null key or
// an unkeyed sibling stops resolving.
func TestResolveCurrentValueRequiresExactUnkeyedIdentity(t *testing.T) {
	t.Parallel()
	planned := testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue)
	keyed := propertyKeyValueFixture(
		commandClassBinarySwitch, 0, valuePropertyCurrentValue, 2,
		boolMetadata(true, false), "false",
	)
	unkeyed := snapshotValueFixture(
		commandClassBinarySwitch, 0, valuePropertyCurrentValue,
		boolMetadata(true, false), "true",
	)

	// A keyed Value that precedes the planned unkeyed one must never be resolved
	// in its place.
	resolved, ok := resolveCurrentValue([]valueState{keyed, unkeyed}, planned)
	if !ok || string(resolved) != "true" {
		t.Fatalf("resolveCurrentValue(keyed, unkeyed) = %q, %t, want true", resolved, ok)
	}
	// A keyed Value alone is a different Value than the planned unkeyed one.
	if keyedOnly, found := resolveCurrentValue([]valueState{keyed}, planned); found {
		t.Fatalf("resolveCurrentValue(keyed) = %q, want no resolution", keyedOnly)
	}
	// An explicit JSON null key counts as absent, exactly as planning treats it.
	nullKeyed := unkeyed
	nullKeyed.PropertyKey = json.RawMessage("null")
	if nullResolved, found := resolveCurrentValue(
		[]valueState{nullKeyed},
		planned,
	); !found || string(nullResolved) != "true" {
		t.Fatalf("resolveCurrentValue(null key) = %q, %t, want true", nullResolved, found)
	}
	// A numeric property is a different Value than the planned string property.
	numeric := numericPropertyValueFixture(commandClassBinarySwitch, 0, "true")
	if numericOnly, found := resolveCurrentValue([]valueState{numeric}, planned); found {
		t.Fatalf("resolveCurrentValue(numeric) = %q, want no resolution", numericOnly)
	}
}

// This test characterizes the runtime invariant that one node's Value report
// projects only onto that node's State. It is a structural check, not a fault
// detector for the node-scoped route key: the runtime already filtered routes by
// NodeID at the call site before the key carried a node, so this test passes with
// or without node-scoped lookup. TestNewRouteSnapshotScopesValuesToTheirNode is
// the fault detector for the node-scoped key.
func TestRuntimeValueReportResolvesOnlyItsOwnNodesRoute(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	const garageNodeID = testNodeID + 1
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(
			testHomeID,
			switchNodeFixture(testNodeID, "Kitchen Switch"),
			switchNodeFixture(garageNodeID, "Garage Switch"),
		),
	)
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	waitFor(t, "both snapshot Observations", func() bool {
		return recorder.count("observation:") == 2
	})
	kitchenPowerID := routeEntityID(testNodeID, "power")
	garagePowerID := routeEntityID(garageNodeID, "power")

	// An update for the kitchen node's Binary current Value must project only
	// onto the kitchen node, even though the garage node reports the same Value
	// ID on the same endpoint.
	connection.events <- receivedEvent{
		Event: valueUpdatedEventForNode(
			testNodeID,
			testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue),
			"false",
		),
		ReceivedAt: time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC),
	}
	waitFor(t, "the kitchen node's updated State", func() bool {
		observation, ok := observationForEntity(session, kitchenPowerID)
		return ok && string(observation.Value) == "false"
	})
	garage, ok := observationForEntity(session, garagePowerID)
	if !ok || string(garage.Value) != "true" {
		t.Fatalf("garage State = %q, %t, want its own unchanged true State", garage.Value, ok)
	}
	if got := len(session.recordedObservations()); got != 3 {
		t.Fatalf("Observations = %d, want 3 (one update for the reporting node only)", got)
	}
}

// This test protects the startup snapshot's routable-route filter, and fails if
// an inconsistent snapshot that is asleep yet advertises listening publishes
// State from a node v1 refuses to route, or reports that node available.
func TestRuntimeStartupSkipsStateForUnroutableAsleepSnapshot(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Sleeping Switch")
	// Inconsistent upstream state: the node is asleep but still reports itself as
	// listening. v1 trusts the asleep status, so the node keeps its registration
	// and mappings but installs no route.
	node.Status = nodeStatusAsleep
	node.IsListening = true
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "the asleep availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityUnavailable &&
			report.ReasonCode == nodeAsleepReason
	})
	if got := len(session.recordedObservations()); got != 0 {
		t.Fatalf("Observations = %d, want 0 from an unroutable asleep snapshot", got)
	}
}

// This test protects the current-before-target value added sequence, and fails
// if an intermediate refresh that cannot project a queued current Value deletes
// it, so the State the frame carried is lost once the target Value completes the
// plan.
func TestRuntimeValueAddedCurrentBeforeTargetPublishesAfterPlanCompletes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)

	// The node gains endpoint 1 one Value at a time: the intermediate refresh
	// reports only its current Value, and the completing refresh reports its
	// target Value too.
	intermediate := node
	intermediate.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	intermediate.Values = append(slices.Clone(binaryPairFixture(0)), snapshotValueFixture(
		commandClassBinarySwitch, 1, valuePropertyCurrentValue, boolMetadata(true, false), "false",
	))
	completing := node
	completing.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	completing.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))

	var reads atomic.Int64
	connection.getStateHook = func(context.Context, int) (nodeState, error) {
		if reads.Add(1) == 1 {
			return intermediate, nil
		}
		return completing, nil
	}

	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	waitFor(t, "the snapshot Observation", func() bool { return recorder.count("observation:") == 1 })
	availabilityBefore := len(session.recordedAvailability())

	// The current Value is added first. No active route can project it, so the
	// frame is queued and a refresh is requested.
	receivedAt := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	added := valueUpdatedEvent(testValueID(commandClassBinarySwitch, 1, valuePropertyCurrentValue), "false")
	added.Event.Event = eventValueAdded
	connection.events <- receivedEvent{Event: added, ReceivedAt: receivedAt}

	// The intermediate refresh still cannot plan endpoint 1, so it must retain the
	// queued frame instead of deleting it.
	waitFor(t, "the intermediate refresh availability", func() bool {
		return len(session.recordedAvailability()) > availabilityBefore
	})
	ep1ID := routeEntityID(testNodeID, "power-ep1")
	if _, ok := observationForEntity(session, ep1ID); ok {
		t.Fatal("the intermediate refresh projected a Value it cannot plan")
	}

	// The target Value is added later, completing the plan and routing endpoint 1.
	targetAdded := valueUpdatedEvent(testValueID(commandClassBinarySwitch, 1, valuePropertyTargetValue), "true")
	targetAdded.Event.Event = eventValueAdded
	connection.events <- receivedEvent{Event: targetAdded, ReceivedAt: receivedAt.Add(time.Second)}

	// The retained frame replays with its original receive time, so the State the
	// current Value added carried is never lost.
	waitFor(t, "the replayed current Observation", func() bool {
		_, ok := observationForEntity(session, ep1ID)
		return ok
	})
	observation, _ := observationForEntity(session, ep1ID)
	if got, want := string(observation.Value), "false"; got != want {
		t.Fatalf("replayed State = %q, want %q", got, want)
	}
	parsed, err := time.Parse(time.RFC3339Nano, observation.AdapterReceivedAt)
	if err != nil {
		t.Fatalf("AdapterReceivedAt = %q: %v", observation.AdapterReceivedAt, err)
	}
	if !parsed.Equal(receivedAt) {
		t.Fatalf("replayed receive time = %s, want %s", parsed, receivedAt)
	}
}

// This test protects the status-superseded registration boundary, and fails if
// an alive or dead Event clears the pending replacement, leaving a Registration
// that committed after the Event remembered but never refreshed or routed, or if
// the superseded read's stale state overwrites the status the Event set.
func TestRuntimeStatusEventSchedulesReplacementRefreshAfterSupersededRegistration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	// The node starts dead, so the status Event's alive transition is observable
	// in availability and proves the Event was processed before release.
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	node.Status = nodeStatusDead
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)

	// The refreshed node gains endpoint 1, so only a completed replacement
	// registration can install its route. The first read deliberately describes
	// the node as still dead and eligible, so applying its state would overwrite
	// the alive status the status Event sets.
	refreshed := switchNodeFixture(testNodeID, "Kitchen Switch")
	refreshed.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	refreshed.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))
	staleRead := refreshed
	staleRead.Status = nodeStatusDead

	var reads atomic.Int64
	connection.getStateHook = func(context.Context, int) (nodeState, error) {
		if reads.Add(1) == 1 {
			return staleRead, nil
		}
		return refreshed, nil
	}

	var registers atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	session.registerHook = func(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
		if registers.Add(1) == 1 {
			return canonicalBinding(registration), nil
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return adapter.Binding{}, ctx.Err()
		case <-release:
			return canonicalBinding(registration), nil
		}
	}
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "the initial dead availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityUnavailable &&
			report.ReasonCode == nodeDeadReason
	})

	// A metadata Event starts a refresh whose read is stale and whose Register
	// blocks, so a status Event can supersede it while it is in flight.
	connection.emit(nodeEvent(eventMetadataUpdated))
	awaitSignal(t, entered, "the superseded registration")

	// The alive Event supersedes the in-flight refresh and must keep a replacement
	// pending, because the registration it already started still needs a fresh read
	// before its committed mappings can be routed.
	connection.emit(nodeEvent(eventAlive))
	waitFor(t, "alive availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
	close(release)

	// The replacement refresh re-reads and re-registers the node, so endpoint 1
	// becomes routable and the stale read never installs its state.
	ep1ID := routeEntityID(testNodeID, "power-ep1")
	waitFor(t, "the replacement refresh to route the new endpoint", func() bool {
		report, ok := session.lastAvailability(ep1ID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
	if got := len(connection.recordedGetCalls()); got != 2 {
		t.Fatalf("node refreshes = %d, want 2 (a replacement refresh after the status Event)", got)
	}
	// Status authority: the alive status the Event set is what availability
	// reports, never the superseded read's stale dead state.
	report, ok := session.lastAvailability(powerID)
	if !ok || report.Status != adapter.AvailabilityAvailable {
		t.Fatalf("power availability = %#v, want available from the alive status", report)
	}
}

// This test protects the value added queue filter, and fails if a target Value,
// a numeric property, a keyed Value, or an unrelated Command Class is queued for
// replay and can crowd out or outlive the current Values that are State.
func TestValueAddedCarriesStateOnlyPlannedCurrentValues(t *testing.T) {
	t.Parallel()
	keyedCurrent := testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue)
	keyedCurrent.PropertyKey = json.RawMessage("2")
	for _, testCase := range []struct {
		name string
		id   valueID
		want bool
	}{
		{name: "binary_current", id: testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue), want: true},
		{name: "multilevel_current_ep2", id: testValueID(commandClassMultilevelSwitch, 2, valuePropertyCurrentValue), want: true},
		{name: "binary_target", id: testValueID(commandClassBinarySwitch, 0, valuePropertyTargetValue)},
		{name: "multilevel_target", id: testValueID(commandClassMultilevelSwitch, 0, valuePropertyTargetValue)},
		{name: "unrelated_command_class", id: testValueID(112, 0, valuePropertyCurrentValue)},
		{name: "numeric_property", id: numericPropertyValueFixture(commandClassBinarySwitch, 0, "true").valueID},
		{name: "keyed_current", id: keyedCurrent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := valueAddedCarriesState(testCase.id); got != testCase.want {
				t.Fatalf("valueAddedCarriesState(%+v) = %t, want %t", testCase.id, got, testCase.want)
			}
		})
	}
}

// This test protects the rejected intermediate refresh boundary, and fails if a
// refresh that drops a node's routes also deletes the queued current Value that
// waits for the plan its target Value later completes.
func TestRuntimeValueAddedReplaySurvivesARejectedIntermediateRefresh(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)

	// The intermediate read rejects the whole node, and the completing read
	// restores endpoint 0 and adds endpoint 1.
	rejected := node
	rejected.Values = nil
	completing := node
	completing.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Left"}}
	completing.Values = slices.Concat(binaryPairFixture(0), binaryPairFixture(1))

	var reads atomic.Int64
	connection.getStateHook = func(context.Context, int) (nodeState, error) {
		if reads.Add(1) == 1 {
			return rejected, nil
		}
		return completing, nil
	}

	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "initial power availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})

	// The new endpoint's current Value is added first and waits for its route.
	receivedAt := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	added := valueUpdatedEvent(testValueID(commandClassBinarySwitch, 1, valuePropertyCurrentValue), "false")
	added.Event.Event = eventValueAdded
	connection.events <- receivedEvent{Event: added, ReceivedAt: receivedAt}

	// The intermediate refresh rejects the node and drops its routes, so power
	// becomes capability-missing while the queued frame must survive.
	waitFor(t, "the rejected intermediate refresh", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.ReasonCode == capabilityMissingReason
	})

	// The target Value completes the plan, and the retained current Value replays.
	targetAdded := valueUpdatedEvent(testValueID(commandClassBinarySwitch, 1, valuePropertyTargetValue), "true")
	targetAdded.Event.Event = eventValueAdded
	connection.events <- receivedEvent{Event: targetAdded, ReceivedAt: receivedAt.Add(time.Second)}

	ep1ID := routeEntityID(testNodeID, "power-ep1")
	waitFor(t, "the replayed current Observation", func() bool {
		_, ok := observationForEntity(session, ep1ID)
		return ok
	})
	observation, _ := observationForEntity(session, ep1ID)
	if got, want := string(observation.Value), "false"; got != want {
		t.Fatalf("replayed State = %q, want %q", got, want)
	}
	parsed, err := time.Parse(time.RFC3339Nano, observation.AdapterReceivedAt)
	if err != nil {
		t.Fatalf("AdapterReceivedAt = %q: %v", observation.AdapterReceivedAt, err)
	}
	if !parsed.Equal(receivedAt) {
		t.Fatalf("replayed receive time = %s, want %s", parsed, receivedAt)
	}
}
