package zwavejs //nolint:testpackage // Runtime tests exercise the private coordinator and connection seam.

// runtime_test.go covers D3: startup and reconnect reconciliation ordering,
// health and availability, topology Events, immutable route generations, and
// reconnect. Every test drives the public Adapter seam over a scripted
// connection and asserts against an independent recording of the SDK boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// reconciledRuntime starts one Adapter over the scripted connections and
// returns it with its dialer. The caller waits for the transition it asserts.
func reconciledRuntime(
	t *testing.T,
	session *runtimeSession,
	connections ...*fakeConnection,
) (*Adapter, *fakeDialer) {
	t.Helper()
	dialer := &fakeDialer{connections: connections}
	zwave := newRuntimeAdapter(t, session, dialer)
	startRuntime(t, zwave)
	return zwave, dialer
}

func TestRuntimeReportsHealthyBeforeAvailabilityAndObservations(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	node.Endpoints = []endpointState{rootEndpointFixture(), {Index: 1, EndpointLabel: "Second"}}
	node.Values = append(levelPairFixture(0), levelPairFixture(1)...)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)

	waitFor(t, "four snapshot Observations", func() bool {
		return recorder.count("observation:") == 4
	})

	binding := nodeBindingKey(fixtureHomeIDText, 23)
	powerID := routeEntityID(testNodeID, "power")
	brightnessID := routeEntityID(testNodeID, "brightness")
	powerEndpointID := routeEntityID(testNodeID, "power-ep1")
	brightnessEndpointID := routeEntityID(testNodeID, "brightness-ep1")

	registrations := session.recordedRegistrations()
	if len(registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(registrations))
	}
	wantKeys := []string{"power", "brightness", "power-ep1", "brightness-ep1"}
	gotKeys := make([]string, 0, len(registrations[0].Entities))
	for _, entity := range registrations[0].Entities {
		gotKeys = append(gotKeys, entity.Key)
	}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("Entity keys = %v, want %v", gotKeys, wantKeys)
	}
	for index := range wantKeys {
		if gotKeys[index] != wantKeys[index] {
			t.Fatalf("Entity keys = %v, want %v", gotKeys, wantKeys)
		}
	}

	assertOrdered(t, recorder.snapshot(),
		"register:"+binding,
		"health:healthy",
		"availability",
		"observation:"+powerID,
		"observation:"+brightnessID,
		"observation:"+powerEndpointID,
		"observation:"+brightnessEndpointID,
	)
	assertNotBefore(t, recorder, "availability", "health:healthy")
	assertNotBefore(t, recorder, "observation:", "availability")

	observations := session.recordedObservations()
	if got, want := observationValues(observations), []string{"true", "15", "true", "15"}; !equalStrings(got, want) {
		t.Fatalf("Observation values = %v, want %v", got, want)
	}

	reports := session.recordedAvailability()
	wantIDs := []string{powerID, brightnessID, powerEndpointID, brightnessEndpointID}
	if len(reports) != len(wantIDs) {
		t.Fatalf("availability reports = %d, want %d", len(reports), len(wantIDs))
	}
	for index, report := range reports {
		if report.EntityID != wantIDs[index] {
			t.Fatalf("availability order = %v, want %v", report.EntityID, wantIDs[index])
		}
		if report.Status != adapter.AvailabilityAvailable || report.ReasonCode != "" {
			t.Fatalf("availability %s = %s/%s, want available", report.EntityID, report.Status, report.ReasonCode)
		}
	}
}

func TestRuntimeReportsHealthyForAnEmptyNetwork(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID),
	)
	reconciledRuntime(t, session, connection)

	waitFor(t, "healthy empty reconciliation", func() bool {
		return recorder.has("health:healthy")
	})
	if got := session.recordedRegistrations(); len(got) != 0 {
		t.Fatalf("registrations = %d, want 0", len(got))
	}
	if got := session.recordedAvailability(); len(got) != 0 {
		t.Fatalf("availability reports = %d, want 0", len(got))
	}
	if got := session.recordedObservations(); len(got) != 0 {
		t.Fatalf("Observations = %d, want 0", len(got))
	}
}

func TestRuntimeReportsNetworkIdentityMismatchUnhealthy(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	session.setMappings(adapter.OwnedMapping{
		BindingKey: nodeBindingKey("aabbccdd", 7),
		DeviceID:   "dev-other",
		EntityKey:  "power",
		EntityID:   "ent-other-power",
	})
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, switchNodeFixture(23, "Kitchen Switch")),
	)
	reconciledRuntime(t, session, connection)

	waitFor(t, "network identity mismatch", func() bool {
		return recorder.has("health:unhealthy:" + networkIdentityMismatchReason)
	})
	if got := recorder.count("register:"); got != 0 {
		t.Fatalf("registrations = %d, want 0", got)
	}
	if got := session.recordedObservations(); len(got) != 0 {
		t.Fatalf("Observations = %d, want 0", len(got))
	}
	if got := session.recordedAvailability(); len(got) != 0 {
		t.Fatalf("availability reports = %d, want 0", len(got))
	}
}

func TestRuntimeReportsIncompatibleProtocolUnhealthy(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID))
	connection.startErr = &incompatibleSchemaVersionError{Minimum: 30, Maximum: 50}
	reconciledRuntime(t, session, connection)

	waitFor(t, "incompatible protocol", func() bool {
		return recorder.has("health:unhealthy:" + incompatibleProtocolReason)
	})
}

// This test protects the malformed-frame health mapping and fails if a version
// frame with no schema range is reported as a schema incompatibility instead of
// an invalid snapshot.
func TestRuntimeReportsMalformedVersionFrameAsInvalidSnapshot(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID))
	connection.startErr = &malformedVersionFrameError{Reason: "the version frame carried no schema range"}
	reconciledRuntime(t, session, connection)

	waitFor(t, "invalid snapshot", func() bool {
		return recorder.has("health:unhealthy:" + invalidSnapshotReason)
	})
	if recorder.has("health:unhealthy:" + incompatibleProtocolReason) {
		t.Fatal("a malformed version frame was reported as an incompatible protocol")
	}
}

func TestRuntimeReportsMissingCapabilityAndNodeStatesUnavailable(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	session.setMappings(
		ownedMappingFixture(23, "power", "ent-23-power"),
		ownedMappingFixture(23, "brightness", "ent-23-brightness"),
		ownedMappingFixture(99, "power", "ent-99-power"),
		ownedMappingFixture(30, "power", "ent-30-power"),
		ownedMappingFixture(31, "power", "ent-31-power"),
	)
	// Node 23 lost its Multilevel Switch capability: only Binary Switch remains.
	capabilityLost := switchNodeFixture(23, "Hallway Dimmer")
	notReady := switchNodeFixture(30, "Unfinished Switch")
	notReady.Ready = false
	asleep := switchNodeFixture(31, "Battery Switch")
	asleep.IsListening = false
	asleep.Status = nodeStatusAsleep
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, capabilityLost, notReady, asleep),
	)
	reconciledRuntime(t, session, connection)

	waitFor(t, "reconciled availability", func() bool {
		return recorder.has("availability")
	})
	waitFor(t, "all mapped Entities reported", func() bool {
		return len(session.recordedAvailability()) == 5
	})

	want := []struct {
		entityID string
		status   adapter.EntityAvailabilityStatus
		reason   string
	}{
		{"ent-23-power", adapter.AvailabilityAvailable, ""},
		{"ent-23-brightness", adapter.AvailabilityUnavailable, capabilityMissingReason},
		{"ent-99-power", adapter.AvailabilityUnavailable, nodeMissingReason},
		{"ent-30-power", adapter.AvailabilityUnavailable, nodeNotReadyReason},
		{"ent-31-power", adapter.AvailabilityUnavailable, nodeAsleepReason},
	}
	reports := session.recordedAvailability()
	for index, expected := range want {
		report := reports[index]
		if report.EntityID != expected.entityID || report.Status != expected.status ||
			report.ReasonCode != expected.reason {
			t.Fatalf("report[%d] = %s/%s/%s, want %s/%s/%s",
				index, report.EntityID, report.Status, report.ReasonCode,
				expected.entityID, expected.status, expected.reason)
		}
	}
	// A sleeping node is excluded from planning, so it must not be registered at
	// all even though its owned mappings are still reported unavailable.
	for _, registration := range session.recordedRegistrations() {
		if registration.BindingKey == nodeBindingKey(fixtureHomeIDText, 31) {
			t.Fatal("sleeping node was registered")
		}
	}
}

func TestRuntimeSleepInvalidatesRoutesAndWakeUpRestoresThem(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	powerID := routeEntityID(testNodeID, "power")
	command := commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute))
	responder := newFakeResponder(recorder, session)
	if err := <-submitCommand(t, zwave, command, responder); err != nil {
		t.Fatalf("first Command error: %v", err)
	}
	if _, _, total := responderCounts(responder); total != 1 {
		t.Fatalf("first Command responses = %d, want 1", total)
	}
	if got := len(connection.recordedSetCalls()); got != 1 {
		t.Fatalf("writes = %d, want 1", got)
	}

	connection.emit(nodeEvent(eventSleep))
	waitFor(t, "sleep availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityUnavailable && report.ReasonCode == nodeAsleepReason
	})

	// A sleeping node has no route, so a new Command is rejected without a write.
	asleepResponder := newFakeResponder(recorder, session)
	asleepResult := submitCommand(t, zwave, command, asleepResponder)
	select {
	case err := <-asleepResult:
		if err != nil {
			t.Fatalf("sleeping Command error: %v", err)
		}
	case <-time.After(harnessTimeout):
		t.Fatal("sleeping Command did not respond")
	}
	if accepted, rejected, total := responderCounts(asleepResponder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("sleeping Command responses = accepted %d rejected %d total %d", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 1 {
		t.Fatalf("writes while asleep = %d, want 1", got)
	}

	connection.emit(nodeEvent(eventWakeUp))
	waitFor(t, "wake up availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
	if got := len(connection.recordedGetCalls()); got != 1 {
		t.Fatalf("node refreshes = %d, want 1", got)
	}

	awakeResponder := newFakeResponder(recorder, session)
	if err := <-submitCommand(t, zwave, command, awakeResponder); err != nil {
		t.Fatalf("awake Command error: %v", err)
	}
	if accepted, _, _ := responderCounts(awakeResponder); accepted != 1 {
		t.Fatalf("awake Command accepted = %d, want 1", accepted)
	}
	waitFor(t, "second write", func() bool { return len(connection.recordedSetCalls()) == 2 })
}

func TestRuntimeZeroEntityRefreshInvalidatesRoutesWithoutRegistering(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	registrationsBefore := len(session.recordedRegistrations())

	// The refreshed node keeps its identity but loses every planned capability.
	stripped := dimmerNodeFixture(23, "Hallway Dimmer")
	stripped.Values = []valueState{
		snapshotValueFixture(37, 0, "duration", numberMetadata(true, false), "0"),
	}
	connection.setNodeState(stripped)
	connection.emit(nodeEvent(eventValueRemoved))

	powerID := routeEntityID(testNodeID, "power")
	brightnessID := routeEntityID(testNodeID, "brightness")
	waitFor(t, "capability removed availability", func() bool {
		report, ok := session.lastAvailability(brightnessID)
		return ok && report.ReasonCode == capabilityMissingReason
	})
	if got, want := len(session.recordedRegistrations()), registrationsBefore; got != want {
		t.Fatalf("registrations = %d, want %d (no zero-Entity registration)", got, want)
	}

	// Routes were invalidated, so a new Command is rejected as unavailable.
	responder := newFakeResponder(recorder, session)
	if err := <-submitCommand(
		t,
		zwave,
		commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, _ := responderCounts(responder); accepted != 0 || rejected != 1 {
		t.Fatalf("Command responses = accepted %d rejected %d, want 0/1", accepted, rejected)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("writes = %d, want 0", got)
	}
}

func TestRuntimeNodeRemovedReportsNodeMissing(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	connection.emit(controllerNodeEvent(eventNodeRemoved, node))
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "node missing availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.ReasonCode == nodeMissingReason
	})

	responder := newFakeResponder(recorder, session)
	if err := <-submitCommand(
		t,
		zwave,
		commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, _ := responderCounts(responder); accepted != 0 || rejected != 1 {
		t.Fatalf("Command responses = accepted %d rejected %d, want 0/1", accepted, rejected)
	}
}

func TestRuntimeValueUpdatePublishesOrdinaryObservationOnly(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)
	waitFor(t, "snapshot Observations", func() bool { return recorder.count("observation:") == 2 })

	connection.emit(valueUpdatedEvent(testValueID(commandClassMultilevelSwitch, 0, "currentValue"), "40"))
	waitFor(t, "updated Observations", func() bool { return recorder.count("observation:") == 4 })

	// A frame that maps to both power and brightness publishes power first.
	values := observationValues(session.recordedObservations())
	if got, want := values[2:], []string{"true", "40"}; !equalStrings(got, want) {
		t.Fatalf("updated Observation values = %v, want %v", got, want)
	}
	if got := session.recordedLinked(); len(got) != 0 {
		t.Fatalf("linked Observations = %d, want 0", len(got))
	}
}

// This test protects the receive order of live State and fails if one frame's
// Observations are published concurrently with the next frame's, which would let
// a later Value update reach Core before an earlier one.
func TestRuntimePublishesValueUpdatesInReceiveOrder(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)
	waitFor(t, "snapshot Observations", func() bool { return recorder.count("observation:") == 2 })

	// The first live frame's brightness publication blocks, so a frame that may
	// publish concurrently would overtake it.
	blocked := make(chan struct{})
	release := make(chan struct{})
	session.setPublishHook(func(ctx context.Context, observation adapter.Observation) error {
		if string(observation.Value) != "70" {
			return nil
		}
		select {
		case blocked <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	current := testValueID(commandClassMultilevelSwitch, 0, valuePropertyCurrentValue)
	connection.emit(valueUpdatedEvent(current, "70"))
	connection.emit(valueUpdatedEvent(current, "40"))
	awaitSignal(t, blocked, "the first live frame to publish")

	// The second frame must wait for the blocked first frame.
	time.Sleep(testQuietPeriod)
	if values := observationValues(session.recordedObservations()); slices.Contains(values, "40") {
		t.Fatalf("Observation values = %v, want no later frame before the blocked one", values)
	}
	close(release)
	waitFor(t, "both live frames published", func() bool { return recorder.count("observation:") == 6 })
	if got, want := observationValues(session.recordedObservations()),
		[]string{"true", "15", "true", "70", "true", "40"}; !equalStrings(got, want) {
		t.Fatalf("Observation values = %v, want %v", got, want)
	}
}

func TestRuntimeConnectionLossInvalidatesRoutesBeforeUnhealthy(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	release := make(chan struct{})
	connection.setValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
		_ json.RawMessage,
	) (setValueStatus, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return setValueStatusSuccess, nil
	}
	zwave, dialer := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	powerID := routeEntityID(testNodeID, "power")
	command := commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute))
	firstResponder := newFakeResponder(recorder, session)

	// The first Command occupies the node's FIFO slot; the second is queued and
	// still unaccepted.
	first := submitCommand(t, zwave, command, firstResponder)
	waitFor(t, "first write", func() bool { return len(connection.recordedSetCalls()) == 1 })
	secondResponder := newFakeResponder(recorder, session)
	second := submitCommand(t, zwave, command, secondResponder)

	connection.fail(errors.New("socket closed"))
	for index, result := range []chan error{first, second} {
		select {
		case <-result:
		case <-time.After(harnessTimeout):
			t.Fatalf("Command %d did not respond", index)
		}
	}

	waitFor(t, "unhealthy report", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})
	// The in-flight Command's rejection happens while the generation is
	// invalidated, strictly before the unhealthy report.
	assertNotBefore(t, recorder, "health:unhealthy:", "response:rejected")

	for index, responder := range []*fakeResponder{firstResponder, secondResponder} {
		if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
			t.Fatalf("Command %d responses = accepted %d rejected %d total %d, want 0/1/1",
				index, accepted, rejected, total)
		}
	}
	close(release)
	waitFor(t, "reconnect dial", func() bool { return dialer.dialCount() >= 2 })
}

func TestRuntimeReconnectsAndReconcilesFully(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(23, "Kitchen Switch")
	first := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID))
	first.startErr = errors.New("connection refused")
	second := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	_, dialer := reconciledRuntime(t, session, first, second)

	waitForRoutesActivated(t, session)
	assertNotBefore(t, recorder, "health:healthy", "health:unhealthy:")
	waitFor(t, "second dial", func() bool { return dialer.dialCount() == 2 })
	assertNotBefore(
		t,
		recorder,
		"register:"+nodeBindingKey(fixtureHomeIDText, 23),
		"health:unhealthy:",
	)
	if got := len(session.recordedObservations()); got != 1 {
		t.Fatalf("Observations = %d, want 1", got)
	}
}

func TestRuntimeBuffersEventsUntilReconciliationCompletes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	// The Event is already queued on the connection before the pump starts.
	connection.emit(valueUpdatedEvent(testValueID(commandClassMultilevelSwitch, 0, "currentValue"), "40"))

	entered := make(chan struct{})
	release := make(chan struct{})
	session.setHealthHook(func(_ context.Context, report adapter.HealthReport) error {
		if report.Status == adapter.HealthHealthy {
			close(entered)
			<-release
		}
		return nil
	})
	reconciledRuntime(t, session, connection)

	select {
	case <-entered:
	case <-time.After(harnessTimeout):
		t.Fatal("reconciliation never reported health")
	}
	// The pre-activation Event is buffered: nothing is published before the
	// ordered startup effect finishes.
	if got := len(session.recordedObservations()); got != 0 {
		t.Fatalf("Observations before activation = %d, want 0", got)
	}
	close(release)
	waitFor(t, "snapshot then Event Observations", func() bool {
		return recorder.count("observation:") == 4
	})
	values := observationValues(session.recordedObservations())
	if got, want := values[2:], []string{"true", "40"}; !equalStrings(got, want) {
		t.Fatalf("Observation values = %v, want snapshot values first", values)
	}
	assertNotBefore(t, recorder, "observation:", "availability")
}

//nolint:paralleltest // The leak check compares a process-wide goroutine count.
func TestRuntimeStopsWithoutLeakingGoroutines(t *testing.T) {
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(23, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	zwave := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- zwave.Run(ctx) }()
	waitForRoutesActivated(t, session)

	responder := newFakeResponder(recorder, session)
	result := submitCommand(
		t,
		zwave,
		commandFixture(routeEntityID(testNodeID, "brightness"), `{"value":15}`, time.Now().Add(time.Minute)),
		responder,
	)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Command error: %v", err)
		}
	case <-time.After(harnessTimeout):
		t.Fatal("Command did not respond")
	}
	if accepted, _, _ := responderCounts(responder); accepted != 1 {
		t.Fatalf("Command accepted = %d, want 1", accepted)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(harnessTimeout):
		t.Fatal("Run did not stop")
	}
	waitFor(t, "goroutines to settle", func() bool {
		return runtime.NumGoroutine() <= before
	})
}

// equalStrings reports whether two string slices are equal in order and length.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestRuntimeIdenticalRefreshKeepsAQueuedAttempt(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	gate := make(chan struct{})
	connection.setValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
		_ json.RawMessage,
	) (setValueStatus, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return setValueStatusUnrecognized, ctx.Err()
		}
		return setValueStatusSuccess, nil
	}
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	entityID := routeEntityID(testNodeID, "power")
	firstResponder := newFakeResponder(recorder, session)
	secondResponder := newFakeResponder(recorder, session)
	first := submitCommand(t, zwave, commandFixture(
		entityID, `{"value":true}`, time.Now().Add(time.Minute),
	), firstResponder)
	waitFor(t, "first write", func() bool { return len(connection.recordedSetCalls()) == 1 })
	second := submitCommand(t, zwave, commandFixture(
		entityID, `{"value":false}`, time.Now().Add(time.Minute),
	), secondResponder)

	// An inventory Event that replans to exactly the same routes must not
	// invalidate the queued attempt.
	connection.emit(nodeEvent(eventMetadataUpdated))
	waitFor(t, "refresh availability", func() bool { return recorder.count("availability") == 2 })

	close(gate)
	for index, result := range []chan error{first, second} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Command %d error: %v", index, err)
			}
		case <-time.After(harnessTimeout):
			t.Fatalf("Command %d did not respond", index)
		}
	}
	if got, want := writeValuesFor(connection, testNodeID), []string{"true", "false"}; !equalStrings(got, want) {
		t.Fatalf("write values = %v, want %v", got, want)
	}
	for index, responder := range []*fakeResponder{firstResponder, secondResponder} {
		if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
			t.Fatalf("Command %d responses = %d/%d/%d, want 1/0/1", index, accepted, rejected, total)
		}
	}
}

func TestRuntimeRouteChangeNeverDispatchesThroughAStaleRevision(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := nodeFixture(testNodeID, []endpointState{rootEndpointFixture(), {
		Index:         1,
		EndpointLabel: "Second",
	}}, binaryPairFixture(0))
	node.Name = "Kitchen Switch"
	node.Label = ""
	node.Values = append(node.Values, levelPairFixture(1)...)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	gate := make(chan struct{})
	connection.setValueHook = func(
		ctx context.Context,
		_ int,
		_ valueID,
		_ json.RawMessage,
	) (setValueStatus, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return setValueStatusUnrecognized, ctx.Err()
		}
		return setValueStatusSuccess, nil
	}
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	// The root power Command occupies the node's FIFO slot while an endpoint-1
	// brightness Command waits behind it. Endpoint 1 is the capability the
	// refreshed plan removes.
	powerID := routeEntityID(testNodeID, "power")
	brightnessID := routeEntityID(testNodeID, "brightness-ep1")
	firstResponder := newFakeResponder(recorder, session)
	secondResponder := newFakeResponder(recorder, session)
	first := submitCommand(t, zwave, commandFixture(
		powerID, `{"value":true}`, time.Now().Add(time.Minute),
	), firstResponder)
	waitFor(t, "first write", func() bool { return len(connection.recordedSetCalls()) == 1 })
	second := submitCommand(t, zwave, commandFixture(
		brightnessID, `{"value":15}`, time.Now().Add(time.Minute),
	), secondResponder)

	// Endpoint 1 loses its Multilevel Switch capability, so the node's route set
	// actually changes.
	stripped := nodeFixture(testNodeID, []endpointState{rootEndpointFixture()}, binaryPairFixture(0))
	stripped.Name = "Kitchen Switch"
	connection.setNodeState(stripped)
	connection.emit(nodeEvent(eventValueRemoved))
	waitFor(t, "refresh availability", func() bool { return recorder.count("availability") == 2 })

	close(gate)
	for index, result := range []chan error{first, second} {
		select {
		case <-result:
		case <-time.After(harnessTimeout):
			t.Fatalf("Command %d did not respond", index)
		}
	}
	if got := writeCountFor(connection, testNodeID); got != 1 {
		t.Fatalf("writes = %d, want 1 (the stale queued attempt must not dispatch)", got)
	}
	for index, responder := range []*fakeResponder{firstResponder, secondResponder} {
		if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
			t.Fatalf("Command %d responses = %d/%d/%d, want 0/1/1", index, accepted, rejected, total)
		}
	}
}

func TestAdapterBatchesAvailabilityAtTheSDKLimit(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	zwave := newRuntimeAdapter(t, session, &fakeDialer{})

	reports := make([]adapter.EntityAvailabilityReport, 300)
	for index := range reports {
		reports[index] = adapter.EntityAvailabilityReport{
			EntityID:         "ent-" + strconv.Itoa(index),
			Status:           adapter.AvailabilityAvailable,
			SourceObservedAt: time.Now().UTC(),
		}
	}
	if err := zwave.reportAvailability(t.Context(), reports); err != nil {
		t.Fatalf("reportAvailability: %v", err)
	}
	batches := session.recordedBatches()
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(batches))
	}
	if len(batches[0]) != availabilityPage || len(batches[1]) != 300-availabilityPage {
		t.Fatalf("batch sizes = %d/%d, want %d/%d",
			len(batches[0]), len(batches[1]), availabilityPage, 300-availabilityPage)
	}
}

func TestRuntimeNodeAddedRefreshesAndRegistersANewNode(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID),
	)
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	added := switchNodeFixture(testNodeID, "New Kitchen Switch")
	connection.setNodeState(added)
	connection.emit(controllerNodeEvent(eventNodeAdded, added))

	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "node added availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
	if got := len(connection.recordedGetCalls()); got != 1 {
		t.Fatalf("node refreshes = %d, want 1", got)
	}
	registrations := session.recordedRegistrations()
	if len(registrations) != 1 || registrations[0].BindingKey != nodeBindingKey(fixtureHomeIDText, testNodeID) {
		t.Fatalf("registrations = %+v, want the added node", registrations)
	}

	// The refreshed node's routes are live, so a Command dispatches.
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want 1/0/1", accepted, rejected, total)
	}
}

// This test protects the failed-generation recovery ordering and fails if an
// in-flight reconciliation can still report healthy, availability, or snapshot
// state after the generation was reported unhealthy.
func TestRuntimeConnectionLossDuringReconciliationDropsStaleRecovery(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	entered := make(chan struct{})
	release := make(chan struct{})
	session.setHealthHook(func(ctx context.Context, report adapter.HealthReport) error {
		if report.Status != adapter.HealthHealthy {
			return nil
		}
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	reconciledRuntime(t, session, connection)

	select {
	case <-entered:
	case <-time.After(harnessTimeout):
		t.Fatal("reconciliation never reported health")
	}
	connection.fail(errors.New("socket closed"))
	waitFor(t, "unhealthy report", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})

	// Release the blocked healthy report. A generation that already failed must
	// never continue into availability or snapshot Observations.
	close(release)
	time.Sleep(testQuietPeriod)
	if got := len(session.recordedAvailability()); got != 0 {
		t.Fatalf("availability reports after unhealthy = %d, want 0", got)
	}
	if got := len(session.recordedObservations()); got != 0 {
		t.Fatalf("Observations after unhealthy = %d, want 0", got)
	}
	if session.logs.has("adapter.reconcile_completed") {
		t.Fatal("a failed generation completed reconciliation")
	}
	assertNotBefore(t, recorder, "health:unhealthy:", "health:healthy")
}

// This test protects the failed-generation refresh boundary and fails if an
// in-flight node refresh can still Register, install routes, or report fresh
// availability after its generation was reported unhealthy.
func TestRuntimeConnectionLossDuringRefreshNeverRegistersOrReportsAvailability(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	connection.getStateHook = func(ctx context.Context, _ int) (nodeState, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return nodeState{}, ctx.Err()
		case <-release:
			return node, nil
		}
	}
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	registrationsBefore := len(session.recordedRegistrations())
	availabilityBefore := len(session.recordedAvailability())

	connection.emit(nodeEvent(eventMetadataUpdated))
	select {
	case <-entered:
	case <-time.After(harnessTimeout):
		t.Fatal("the refresh never started")
	}
	connection.fail(errors.New("socket closed"))
	waitFor(t, "unhealthy report", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})

	// Release the blocked refresh. A generation that already failed must never
	// register the refreshed node or report its availability.
	close(release)
	time.Sleep(testQuietPeriod)
	if got := len(session.recordedRegistrations()); got != registrationsBefore {
		t.Fatalf("registrations after loss = %d, want %d", got, registrationsBefore)
	}
	if got := len(session.recordedAvailability()); got != availabilityBefore {
		t.Fatalf("availability reports after loss = %d, want %d", got, availabilityBefore)
	}
}

// This test protects the failed-refresh boundary and fails if a refresh that
// fails unexpectedly reports availability after its generation was dropped,
// which would describe a route that no longer exists as healthy.
func TestRuntimeUnexpectedRefreshFailureReportsNoStaleAvailability(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	connection.getStateHook = func(context.Context, int) (nodeState, error) {
		return nodeState{}, errors.New("socket read failed")
	}
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	availabilityBefore := len(session.recordedAvailability())

	connection.emit(nodeEvent(eventMetadataUpdated))
	waitFor(t, "the failed refresh to drop the generation", func() bool {
		return recorder.has("health:unhealthy:" + externalSystemUnavailableReason)
	})
	time.Sleep(testQuietPeriod)

	if !session.logs.has("adapter.node_refresh_failed") {
		t.Fatalf("logs = %v, want a failed refresh diagnosis", session.logs.events)
	}
	if got := len(session.recordedAvailability()); got != availabilityBefore {
		t.Fatalf(
			"availability reports after the failed refresh = %d, want %d",
			got, availabilityBefore,
		)
	}
}

// This test protects the node life-cycle event paths and fails if alive or dead
// events stop updating availability.
func TestRuntimeAliveAndDeadEventsUpdateAvailability(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "initial availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})

	connection.emit(nodeEvent(eventDead))
	waitFor(t, "dead availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityUnavailable &&
			report.ReasonCode == nodeDeadReason
	})

	connection.emit(nodeEvent(eventAlive))
	waitFor(t, "alive availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
}

// This test protects the assessed-unknown availability path and fails if a node
// that was previously known and returns to Unknown is reported available.
func TestRuntimeAssessedNodeUnknownReportsUnavailable(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	node.Status = nodeStatusUnknown
	session.setMappings(ownedMappingFixture(testNodeID, "power", "ent-23-power"))
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)

	waitFor(t, "unknown availability", func() bool {
		report, ok := session.lastAvailability("ent-23-power")
		return ok && report.Status == adapter.AvailabilityUnavailable &&
			report.ReasonCode == nodeUnknownReason
	})
}

// This test protects the value added path and fails if an added Value stops
// publishing an ordinary Observation or stops refreshing the node inventory.
func TestRuntimeValueAddedPublishesObservationAndRefreshes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(testNodeID, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)
	waitFor(t, "snapshot Observations", func() bool { return recorder.count("observation:") == 2 })

	added := valueUpdatedEvent(testValueID(commandClassMultilevelSwitch, 0, valuePropertyCurrentValue), "40")
	added.Event.Event = eventValueAdded
	connection.emit(added)
	waitFor(t, "value added Observations", func() bool { return recorder.count("observation:") == 4 })
	waitFor(t, "value added refresh", func() bool {
		return len(connection.recordedGetCalls()) == 1
	})
	if got, want := observationValues(session.recordedObservations()),
		[]string{"true", "15", "true", "40"}; !equalStrings(got, want) {
		t.Fatalf("Observation values = %v, want %v", got, want)
	}
}

// This test protects the interview completed path and fails if a completed
// interview stops refreshing and re-registering an eligible node.
func TestRuntimeInterviewCompletedRefreshesAndReRegisters(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	connection.emit(nodeEvent(eventInterviewCompleted))
	waitFor(t, "interview completed refresh", func() bool {
		return len(connection.recordedGetCalls()) == 1
	})
	waitFor(t, "interview completed re-registration", func() bool {
		return len(session.recordedRegistrations()) == 2
	})
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "post-refresh availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityAvailable
	})
}

// This test protects the same-generation refresh boundary and fails if an
// in-flight node.get_state whose inventory a later sleep or removal Event
// superseded still re-registers the node, reinstalls its routes, and reports it
// available again.
func TestRuntimeSupersededRefreshNeverRestoresRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		event      serverEvent
		reasonCode string
	}{
		{"sleep", nodeEvent(eventSleep), nodeAsleepReason},
		{
			"removal",
			controllerNodeEvent(eventNodeRemoved, dimmerNodeFixture(testNodeID, "Hallway Dimmer")),
			nodeMissingReason,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			requireSupersededRefreshInstallsNothing(t, testCase.event, testCase.reasonCode)
		})
	}
}

// requireSupersededRefreshInstallsNothing starts one held refresh, applies one
// superseding Event, then proves that releasing the stale read changes nothing.
func requireSupersededRefreshInstallsNothing(
	t *testing.T,
	event serverEvent,
	reasonCode string,
) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(testNodeID, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	connection.getStateHook = func(ctx context.Context, _ int) (nodeState, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return nodeState{}, ctx.Err()
		case <-release:
			// The read was issued before the superseding Event, so it still
			// describes the node as awake and eligible.
			return node, nil
		}
	}
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	// A topology Event starts a refresh whose inventory still describes the
	// node as awake and eligible.
	connection.emit(nodeEvent(eventMetadataUpdated))
	select {
	case <-entered:
	case <-time.After(harnessTimeout):
		t.Fatal("the refresh never started")
	}
	connection.emit(event)
	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "the superseding availability", func() bool {
		report, ok := session.lastAvailability(powerID)
		return ok && report.Status == adapter.AvailabilityUnavailable &&
			report.ReasonCode == reasonCode
	})
	registrationsBefore := len(session.recordedRegistrations())
	availabilityBefore := len(session.recordedAvailability())

	// Releasing the superseded read must change nothing: no re-registration,
	// no fresh availability, and no restored routes.
	close(release)
	time.Sleep(testQuietPeriod)
	if got := len(session.recordedRegistrations()); got != registrationsBefore {
		t.Fatalf("registrations = %d, want %d", got, registrationsBefore)
	}
	if got := len(session.recordedAvailability()); got != availabilityBefore {
		t.Fatalf("availability reports = %d, want %d", got, availabilityBefore)
	}
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
	if got := writeCountFor(connection, testNodeID); got != 0 {
		t.Fatalf("writes = %d, want 0 (a superseded refresh must not restore routes)", got)
	}
}

// This test protects the coalesced refresh rule and fails if a superseded
// completion stops the pending refresh from running, or if the pending refresh
// reuses the superseded read instead of reading the node again.
func TestRuntimeCoalescedRefreshRunsAfterASupersededRead(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(testNodeID, "Hallway Dimmer")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	// The first read is held, so a second Event coalesces a pending refresh. The
	// first read still describes the full dimmer; only the second read observes
	// the stripped node.
	stripped := switchNodeFixture(testNodeID, "Hallway Dimmer")
	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	connection.getStateHook = func(ctx context.Context, _ int) (nodeState, error) {
		if calls.Add(1) == 1 {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				return nodeState{}, ctx.Err()
			case <-release:
				return node, nil
			}
		}
		return stripped, nil
	}
	reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	connection.emit(nodeEvent(eventMetadataUpdated))
	select {
	case <-entered:
	case <-time.After(harnessTimeout):
		t.Fatal("the first refresh never started")
	}
	connection.emit(nodeEvent(eventValueRemoved))

	// Releasing the superseded read must not install it; the pending refresh
	// then runs and installs the stripped node.
	close(release)
	brightnessID := routeEntityID(testNodeID, "brightness")
	waitFor(t, "the pending refresh to remove the capability", func() bool {
		report, ok := session.lastAvailability(brightnessID)
		return ok && report.ReasonCode == capabilityMissingReason
	})
	if got := len(connection.recordedGetCalls()); got != 2 {
		t.Fatalf("node refreshes = %d, want 2 (the coalesced refresh must run)", got)
	}
	if got := writeCountFor(connection, testNodeID); got != 0 {
		t.Fatalf("writes = %d, want 0", got)
	}
}

// This test protects the terminal-event classification and fails if an
// availability completion from another generation, or one that only reports a
// canceled operation, is treated as a Session failure. A canceled availability
// report is exactly what tearing a generation down produces.
func TestTaskCompletedStaleScopeOrCancellationIsIgnored(t *testing.T) {
	t.Parallel()
	zwave := newRuntimeAdapter(t, newRuntimeSession(&runtimeRecorder{}), &fakeDialer{})
	coordinator := newRuntimeCoordinator(t.Context(), zwave)

	stale := newGenerationScope(t.Context())
	t.Cleanup(stale.end)
	if err := coordinator.handle(taskCompleted{
		scope:      stale,
		generation: coordinator.generation,
		err:        errors.New("a stale generation failed"),
	}); err != nil {
		t.Fatalf("stale completion = %v, want it ignored", err)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if err := coordinator.handle(taskCompleted{
			generation: coordinator.generation,
			err:        &sessionOperationError{operation: "report availability", err: cause},
		}); err != nil {
			t.Fatalf("canceled completion = %v, want it ignored", err)
		}
	}

	// A real Session failure is still terminal.
	failure := &sessionOperationError{
		operation: "report availability",
		err:       errors.New("the Session is unavailable"),
	}
	failureCompletion := taskCompleted{generation: coordinator.generation, err: failure}
	if err := coordinator.handle(failureCompletion); !errors.Is(err, failure) {
		t.Fatalf("Session failure completion = %v, want it returned", err)
	}
}

// This test protects the runtime against a generation-scoped availability report
// that returns a cancellation and fails if that completion stops the runtime
// instead of being ignored as teardown.
func TestRuntimeCanceledAvailabilityCompletionKeepsRunning(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, node),
	)
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	var canceled atomic.Int64
	session.setAvailabilityHook(func(context.Context, []adapter.EntityAvailabilityReport) error {
		canceled.Add(1)
		return context.Canceled
	})
	connection.emit(nodeEvent(eventAlive))
	waitFor(t, "the canceled availability report", func() bool { return canceled.Load() >= 1 })
	// The runtime must still serve the node: a canceled completion is not a
	// Session failure, and its generation is still the active one.
	time.Sleep(testQuietPeriod)
	session.setAvailabilityHook(nil)
	powerID := routeEntityID(testNodeID, "power")
	responder := newFakeResponder(recorder, session)
	if err := runCommand(
		t,
		zwave,
		commandFixture(powerID, `{"value":true}`, time.Now().Add(time.Minute)),
		responder,
	); err != nil {
		t.Fatalf("Command after a canceled availability report: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 1 || rejected != 0 || total != 1 {
		t.Fatalf("responses = %d/%d/%d, want 1/0/1", accepted, rejected, total)
	}
}
