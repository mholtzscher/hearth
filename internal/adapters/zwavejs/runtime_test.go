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

// This test protects reconnect backoff after an interrupted reconciliation and
// fails if a connection lost during health reporting resets the accumulated delay.
func TestReconnectBackoffResetsOnlyAfterReconciliationCompletes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := dimmerNodeFixture(testNodeID, "Hallway Dimmer")
	failed := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	failed.startErr = errors.New("first generation failed before reconciliation")
	incomplete := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	healthStarted := make(chan struct{})
	releaseHealth := make(chan struct{})
	session.setHealthHook(func(ctx context.Context, report adapter.HealthReport) error {
		if report.Status != adapter.HealthHealthy {
			return nil
		}
		close(healthStarted)
		select {
		case <-releaseHealth:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	dialer := &fakeDialer{connections: []*fakeConnection{failed, incomplete}}
	zwave := newRuntimeAdapter(t, session, dialer)
	delays := make(chan time.Duration, 4)
	zwave.retryDelay = func(delay time.Duration) time.Duration {
		delays <- delay
		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- zwave.Run(ctx) }()
	t.Cleanup(func() {
		close(releaseHealth)
		cancel()
	})

	var delay time.Duration
	select {
	case delay = <-delays:
	case <-time.After(harnessTimeout):
		t.Fatal("first connection did not request a retry")
	}
	if delay != reconnectMinimum {
		t.Fatalf("first reconnect delay = %v, want %v", delay, reconnectMinimum)
	}
	select {
	case <-healthStarted:
	case <-time.After(harnessTimeout):
		t.Fatal("second generation did not begin healthy reconciliation")
	}
	incomplete.fail(errors.New("connection ended before reconciliation completed"))
	select {
	case delay = <-delays:
	case <-time.After(harnessTimeout):
		t.Fatal("interrupted reconciliation did not request a retry")
	}
	if delay != 2*reconnectMinimum {
		t.Fatalf("interrupted reconciliation retry delay = %v, want %v", delay, 2*reconnectMinimum)
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

func TestRuntimeTopologyEventReconnectsAndReconcilesFullSnapshot(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	first := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID),
	)
	added := switchNodeFixture(testNodeID, "New Kitchen Switch")
	second := newFakeConnection(
		recorder,
		versionFixture(),
		snapshotFixture(testHomeID, added),
	)
	dialer := &fakeDialer{connections: []*fakeConnection{first, second}}
	zwave := newRuntimeAdapter(t, session, dialer)
	zwave.retryDelay = func(time.Duration) time.Duration { return 0 }
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)
	first.emit(controllerNodeEvent(eventNodeAdded, added))

	powerID := routeEntityID(testNodeID, "power")
	waitFor(t, "full snapshot availability after reconnect", func() bool {
		report, ok := session.lastAvailability(powerID)
		return dialer.dialCount() >= 2 && ok && report.Status == adapter.AvailabilityAvailable
	})
	registrations := session.recordedRegistrations()
	if len(registrations) != 1 || registrations[0].BindingKey != nodeBindingKey(fixtureHomeIDText, testNodeID) {
		t.Fatalf("registrations = %+v, want registration from the replacement snapshot", registrations)
	}

	// The replacement snapshot's routes are live, so a Command dispatches.
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

// This test protects removed-node reconciliation and fails if a removed node's
// previously owned Entity remains available after the replacement snapshot.
func TestRuntimeNodeRemovalReconcilesMissingEntity(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	first := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	second := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID))
	dialer := &fakeDialer{connections: []*fakeConnection{first, second}}
	zwave := newRuntimeAdapter(t, session, dialer)
	zwave.retryDelay = func(time.Duration) time.Duration { return 0 }
	startRuntime(t, zwave)
	waitForRoutesActivated(t, session)

	powerID := routeEntityID(testNodeID, "power")
	first.emit(controllerNodeEvent(eventNodeRemoved, node))
	waitFor(t, "removed Entity unavailable from the replacement snapshot", func() bool {
		report, ok := session.lastAvailability(powerID)
		return dialer.dialCount() >= 2 && ok &&
			report.Status == adapter.AvailabilityUnavailable && report.ReasonCode == nodeMissingReason
	})
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

// This test protects assessed-unknown snapshot handling and fails if an unknown
// node is reported available or retains a Command route from its capabilities.
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
	zwave, _ := reconciledRuntime(t, session, connection)

	waitFor(t, "unknown availability", func() bool {
		report, ok := session.lastAvailability("ent-23-power")
		return ok && report.Status == adapter.AvailabilityUnavailable &&
			report.ReasonCode == nodeUnknownReason
	})
	if got := len(session.recordedObservations()); got != 0 {
		t.Fatalf("snapshot Observations = %d, want none for an unroutable unknown node", got)
	}

	responder := newFakeResponder(recorder, session)
	if err := runCommand(t, zwave, commandFixture(
		routeEntityID(testNodeID, "power"), `{"value":true}`, time.Now().Add(time.Minute),
	), responder); err != nil {
		t.Fatalf("unknown-node Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("unknown-node Command responses = %d/%d/%d, want 0/1/1", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("unknown-node writes = %d, want 0", got)
	}
}

// This test protects dead-node snapshot handling and fails if a dead node's
// otherwise valid capabilities are installed as Command routes.
func TestRuntimeDeadSnapshotDoesNotInstallCommandRoutes(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Dead Switch")
	node.Status = nodeStatusDead
	session.setMappings(ownedMappingFixture(testNodeID, "power", "ent-23-power"))
	connection := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	zwave, _ := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	waitFor(t, "dead availability", func() bool {
		report, ok := session.lastAvailability("ent-23-power")
		return ok && report.Status == adapter.AvailabilityUnavailable && report.ReasonCode == nodeDeadReason
	})
	if got := len(session.recordedObservations()); got != 0 {
		t.Fatalf("snapshot Observations = %d, want none for a dead node", got)
	}

	responder := newFakeResponder(recorder, session)
	if err := runCommand(t, zwave, commandFixture(
		routeEntityID(testNodeID, "power"), `{"value":true}`, time.Now().Add(time.Minute),
	), responder); err != nil {
		t.Fatalf("dead-node Command error: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("dead-node Command responses = %d/%d/%d, want 0/1/1", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("dead-node writes = %d, want 0", got)
	}
}

// This test protects immediate dead-event invalidation and fails if a route
// remains dispatchable between the dead Event and the replacement snapshot.
func TestRuntimeDeadEventClearsRoutesBeforeReconnect(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	node := switchNodeFixture(testNodeID, "Kitchen Switch")
	connection := newFakeConnection(recorder, versionFixture(), snapshotFixture(testHomeID, node))
	zwave, dialer := reconciledRuntime(t, session, connection)
	waitForRoutesActivated(t, session)

	connection.emit(nodeEvent(eventDead))
	waitFor(t, "dead Event generation recycle", func() bool { return dialer.dialCount() >= 2 })
	responder := newFakeResponder(recorder, session)
	if err := runCommand(t, zwave, commandFixture(
		routeEntityID(testNodeID, "power"), `{"value":true}`, time.Now().Add(time.Minute),
	), responder); err != nil {
		t.Fatalf("Command after dead Event: %v", err)
	}
	if accepted, rejected, total := responderCounts(responder); accepted != 0 || rejected != 1 || total != 1 {
		t.Fatalf("post-dead Command responses = %d/%d/%d, want 0/1/1", accepted, rejected, total)
	}
	if got := len(connection.recordedSetCalls()); got != 0 {
		t.Fatalf("writes after dead Event = %d, want 0", got)
	}
}

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
