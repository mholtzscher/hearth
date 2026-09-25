package zwavejs //nolint:testpackage // Regression tests exercise the private coordinator and planning seams.

// runtime_regression_test.go protects cross-component Event handling, startup
// buffering, exact Value identity, and snapshot projection boundaries.

import (
	"context"
	"encoding/json"
	"errors"
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

// This test protects Value Event decoding and fails if keyed or malformed
// identities alias the unkeyed current Value used by a route.
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

// This test protects the value-added plan boundary, and fails if an unrelated
// sensor Value addition recycles the connection and interrupts Commands for an
// unchanged supported route.
func TestRuntimeUnrelatedValueAddedKeepsCommandsOnTheActiveConnection(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	first := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	second := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	dialer := &fakeDialer{connections: []*fakeConnection{first, second}}
	zwave := newRuntimeAdapter(t, session, dialer)
	zwave.retryDelay = func(time.Duration) time.Duration { return 0 }
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	added := valueUpdatedEvent(testValueID(49, 0, valuePropertyCurrentValue), "21")
	added.Event.Event = eventValueAdded
	first.emit(added)
	time.Sleep(testQuietPeriod)
	if got := dialer.dialCount(); got != 1 {
		t.Fatalf("connection generations after unrelated Value addition = %d, want 1", got)
	}

	entityID := routeEntityID(testNodeID, "power")
	responder := newFakeResponder(recorder, session)
	if err := runCommand(t, zwave, commandFixture(
		entityID, `{"value":true}`, time.Now().Add(time.Minute),
	), responder); err != nil {
		t.Fatalf("Command after unrelated Value addition: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("Command responses = %d/%d/%d, want 1/0/1", accepted, rejected, total)
	}
	if got := len(first.recordedSetCalls()); got != 1 {
		t.Fatalf("writes on original connection = %d, want 1", got)
	}
}

// This test protects exact switch Value identity, and fails if a keyed,
// numeric, malformed, negative-endpoint, or unrelated Value can trigger a
// supported-plan reconnect.
func TestValueAddedChangesSupportedPlanOnlyForValidSwitchSlots(t *testing.T) {
	t.Parallel()
	keyed := testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue)
	keyed.PropertyKey = json.RawMessage(`2`)
	numericProperty := testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue)
	numericProperty.Property.Numeric = true
	malformedProperty := testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue)
	malformedProperty.Property.Invalid = true
	for _, testCase := range []struct {
		name string
		id   valueID
		want bool
	}{
		{name: "binary current", id: testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue), want: true},
		{name: "binary target", id: testValueID(commandClassBinarySwitch, 1, valuePropertyTargetValue), want: true},
		{name: "multilevel current", id: testValueID(commandClassMultilevelSwitch, 2, valuePropertyCurrentValue), want: true},
		{name: "multilevel target", id: testValueID(commandClassMultilevelSwitch, 3, valuePropertyTargetValue), want: true},
		{name: "sensor", id: testValueID(49, 0, valuePropertyCurrentValue)},
		{name: "keyed", id: keyed},
		{name: "numeric property", id: numericProperty},
		{name: "malformed property", id: malformedProperty},
		{name: "negative endpoint", id: testValueID(commandClassBinarySwitch, -1, valuePropertyCurrentValue)},
		{name: "other property", id: testValueID(commandClassBinarySwitch, 0, "duration")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := valueAddedChangesSupportedPlan(testCase.id); got != testCase.want {
				t.Fatalf("valueAddedChangesSupportedPlan(%+v) = %t, want %t", testCase.id, got, testCase.want)
			}
		})
	}
}

// This test protects incomplete switch plans, and fails if adding either the
// current or target Value stops reconnecting for a fresh inventory snapshot.
func TestRuntimeSupportedSwitchValueAddedReconnectsBeforeThePairIsComplete(t *testing.T) {
	t.Parallel()
	for _, property := range []string{valuePropertyCurrentValue, valuePropertyTargetValue} {
		t.Run(property, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newRuntimeSession(recorder)
			node := switchNodeFixture(testNodeID, "Kitchen Switch")
			first := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
			second := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
			dialer := &fakeDialer{connections: []*fakeConnection{first, second}}
			zwave := newRuntimeAdapter(t, session, dialer)
			zwave.retryDelay = func(time.Duration) time.Duration { return 0 }
			startRuntime(t, zwave)
			waitForRoutesActivated(t, session)

			added := valueUpdatedEvent(testValueID(commandClassBinarySwitch, 3, property), "false")
			added.Event.Event = eventValueAdded
			first.emit(added)
			waitFor(t, property+" addition reconnect", func() bool {
				return dialer.dialCount() >= 2 && recorder.has("health:healthy")
			})
			waitForRoutesActivated(t, session)
		})
	}
}

// This test protects the value-added plan boundary, and fails if a value added
// frame that creates a new plan is published before its registration and its
// State is lost, or if a frame whose route already exists is published twice.
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
