package ecowitt //nolint:testpackage // Runtime tests drive the package-private serial coordinator.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// deferredEffects records effects without running them, so a test can hold one
// chain open and inspect superseded completions.
type deferredEffects struct {
	pending []func()
}

// Go defers one effect.
func (effects *deferredEffects) Go(effect func()) { effects.pending = append(effects.pending, effect) }

// Wait joins no goroutines.
func (effects *deferredEffects) Wait() {}

// drain runs deferred effects and their completions until nothing is left, so
// a test can advance one full chain at a time.
func (effects *deferredEffects) drain(t *testing.T, harness *runtimeHarness) {
	t.Helper()
	for range 1000 {
		if len(effects.pending) == 0 {
			return
		}
		effects.runAll()
		harness.pump(t)
	}
	t.Fatal("deferred effects did not quiesce")
}

// runAll runs every deferred effect.
func (effects *deferredEffects) runAll() {
	pending := effects.pending
	effects.pending = nil
	for _, effect := range pending {
		effect()
	}
}

// fixtureReportMessage builds one broker delivery carrying the sanitized
// fixture plus a unique ignored field, so two deliveries are never duplicates.
func fixtureReportMessage(t *testing.T, sequence int) []byte {
	t.Helper()
	payload := string(loadFixture(t, "gw2000-ws90-report.txt"))
	if sequence == 0 {
		return []byte(payload)
	}
	return []byte(fmt.Sprintf("%s&ignored_sequence=%d", payload, sequence))
}

// laterDateReportMessage returns the fixture with a later station date.
func laterDateReportMessage(t *testing.T) []byte {
	t.Helper()
	payload := string(loadFixture(t, "gw2000-ws90-report.txt"))
	return []byte(strings.Replace(payload, "dateutc=2026-09-12+14%3A30%3A00", "dateutc=2026-09-12+14%3A30%3A08", 1))
}

// TestRuntimeStaysHealthUnknownUntilEvidence protects that SDK health stays
// unknown until a live report or a deadline proves the external system's state,
// and that SUBACK starts the initial three-interval deadline.
func TestRuntimeStaysHealthUnknownUntilEvidence(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.advance(t, 3*fixtureUploadInterval-time.Second)
	if reports := harness.session.health(); len(reports) != 0 {
		t.Fatalf("health reports before the deadline = %#v, want none", reports)
	}
	harness.advance(t, time.Second)
	reports := harness.session.health()
	if len(reports) != 1 {
		t.Fatalf("health reports at the deadline = %#v, want exactly one", reports)
	}
	if reports[0].Status != adapter.HealthUnhealthy || reports[0].ReasonCode != stationSilentReason {
		t.Fatalf("health at the deadline = %#v, want unhealthy %s", reports[0], stationSilentReason)
	}
}

// TestRuntimeMQTTFailureReportsExternalSystemUnavailable protects the immediate
// unhealthy transition for a connect failure, disconnect, subscription failure,
// or relay overflow, and that the silent-report deadline stays disarmed
// afterwards.
func TestRuntimeMQTTFailureReportsExternalSystemUnavailable(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.submit(t, upstreamUnavailable{generation: 1})
	reports := harness.session.health()
	if len(reports) != 1 || reports[0].Status != adapter.HealthUnhealthy ||
		reports[0].ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("health after MQTT failure = %#v, want unhealthy %s", reports, externalSystemUnavailableReason)
	}
	harness.advance(t, 10*fixtureUploadInterval)
	if len(harness.session.health()) != 1 {
		t.Fatalf("the station-silent deadline fired after the generation ended: %#v", harness.session.health())
	}
	if !harness.coordinator.generationEnded {
		t.Fatal("the MQTT generation stayed live after a failure")
	}
}

// TestRuntimeReportRecoversHealthThenAvailabilityThenObservations protects the
// fixed startup and recovery order: a live compatible report reports healthy
// and waits for the acknowledgement before Core-cleared availability and before
// its Observations in catalog order.
func TestRuntimeReportRecoversHealthThenAvailabilityThenObservations(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.advance(t, 3*fixtureUploadInterval)
	if health := harness.session.health(); len(health) != 1 || health[0].ReasonCode != stationSilentReason {
		t.Fatalf("precondition: health = %#v, want one silent report", health)
	}

	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)

	health := harness.session.health()
	if len(health) != 2 || health[1].Status != adapter.HealthHealthy {
		t.Fatalf("health after recovery = %#v, want a healthy transition", health)
	}
	recovered := harness.session.availability()
	snapshot := snapshotFor(t, testConfig(t))
	if len(recovered) != len(snapshot.entities) {
		t.Fatalf("availability after recovery = %d reports, want %d", len(recovered), len(snapshot.entities))
	}
	for index, report := range recovered {
		if report.EntityID != snapshot.entities[index].EntityID {
			t.Fatalf("availability[%d] Entity = %q, want %q",
				index, report.EntityID, snapshot.entities[index].EntityID)
		}
		if report.Status != adapter.AvailabilityAvailable || report.ReasonCode != "" {
			t.Fatalf("availability[%d] = %#v, want an available report with no reason", index, report)
		}
	}
	published := publishedEntityIDs(harness.session.published())
	if len(published) != len(snapshot.entities) {
		t.Fatalf("Observations after recovery = %d, want %d", len(published), len(snapshot.entities))
	}
	for index, entityID := range published {
		if entityID != snapshot.entities[index].EntityID {
			t.Fatalf("Observation[%d] Entity = %q, want catalog order", index, entityID)
		}
	}

	calls := harness.trace.snapshot()
	healthIndex := indexOfPrefix(t, calls, "health:healthy")
	availabilityIndex := indexOfPrefix(t, calls, "availability:19:available")
	observationIndex := indexOfPrefix(t, calls, "observation:")
	if healthIndex >= availabilityIndex || availabilityIndex >= observationIndex {
		t.Fatalf("recovery ordering = %v, want health before availability before Observations", calls)
	}
}

// TestRuntimeIgnoresEvidenceFreeDeliveries protects that retained, wrong-topic,
// wrong-PASSKEY, wrong-station-type, oversized, duplicate-key, over-field-limit,
// and malformed deliveries produce no evidence and never refresh the report
// deadline.
func TestRuntimeIgnoresEvidenceFreeDeliveries(t *testing.T) {
	t.Parallel()

	oversized := "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00&pad=" +
		strings.Repeat("x", maximumReportBytes)

	var distinctKeys strings.Builder
	distinctKeys.WriteString("PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00")
	for index := range maximumReportFields + 1 {
		fmt.Fprintf(&distinctKeys, "&field%d=1", index)
	}

	fixture := string(loadFixture(t, "gw2000-ws90-report.txt"))
	for _, testCase := range []struct {
		name     string
		topic    string
		payload  string
		retained bool
	}{
		{name: "retained replay", topic: fixtureTopic, payload: fixture, retained: true},
		{name: "wrong topic", topic: "ecowitt/000000000000", payload: fixture},
		{name: "wrong passkey", topic: fixtureTopic, payload: strings.Replace(
			fixture, "PASSKEY="+sanitizedPasskeyHex, "PASSKEY=ffffffffffffffffffffffffffffffff", 1)},
		{name: "wrong station type", topic: fixtureTopic, payload: strings.Replace(
			fixture, "stationtype=GW2000B_V3.3.2", "stationtype=GW1100A_V2.2.9", 1)},
		{name: "malformed escape", topic: fixtureTopic, payload: fixture + "%zz"},
		{name: "duplicate key", topic: fixtureTopic, payload: fixture + "&tempf=65.84"},
		{name: "oversized payload", topic: fixtureTopic, payload: oversized},
		{name: "over field limit", topic: fixtureTopic, payload: distinctKeys.String()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			harness := newRuntimeHarness(t, nil)
			harness.establish(t)
			harness.advance(t, 3*fixtureUploadInterval-time.Second)
			harness.deliver(t, testCase.topic, []byte(testCase.payload), testCase.retained)

			if published := harness.session.published(); len(published) != 0 {
				t.Fatalf("ignored delivery published %d Observations", len(published))
			}
			if availability := harness.session.availability(); len(availability) != 0 {
				t.Fatalf("ignored delivery reported %d availability entries", len(availability))
			}
			if health := harness.session.health(); len(health) != 0 {
				t.Fatalf("ignored delivery reported health %#v", health)
			}
			harness.advance(t, time.Second)
			health := harness.session.health()
			if len(health) != 1 || health[0].ReasonCode != stationSilentReason {
				t.Fatalf("ignored delivery refreshed the deadline: health = %#v", health)
			}
		})
	}
}

// TestRuntimeRejectsReportsFromAnEndedOrStaleGeneration protects the
// generation fence on the report path: evidence from a dead generation is never
// accepted and never refreshes the deadline.
func TestRuntimeRejectsReportsFromAnEndedOrStaleGeneration(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.submit(t, upstreamUnavailable{generation: 1})
	harness.advance(t, 3*fixtureUploadInterval)
	if !harness.coordinator.generationEnded {
		t.Fatal("precondition: the MQTT generation is still live")
	}
	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	harness.deliverAt(t, 0, fixtureTopic, fixtureReportMessage(t, 1), false, harness.clock.Now())
	if published := harness.session.published(); len(published) != 0 {
		t.Fatalf("an ended generation published %d Observations", len(published))
	}
	if availability := harness.session.availability(); len(availability) != 0 {
		t.Fatalf("an ended generation reported %d availability entries", len(availability))
	}
	health := harness.session.health()
	if len(health) != 1 || health[0].ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("health after an ended-generation report = %#v", health)
	}
}

// TestRuntimeSuppressesExactDuplicatesAndAcceptsLaterDates protects
// connection-local duplicate suppression: an immediately repeated report adds
// no evidence, while the same values at a later station date publish fresh
// Observations.
func TestRuntimeSuppressesExactDuplicatesAndAcceptsLaterDates(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	first := fixtureReportMessage(t, 0)
	harness.advance(t, time.Second)
	harness.deliverAt(t, 1, fixtureTopic, first, false, harness.clock.Now())
	if got := len(harness.session.published()); got != 19 {
		t.Fatalf("first report published %d Observations, want 19", got)
	}

	harness.advance(t, time.Second)
	harness.deliverAt(t, 1, fixtureTopic, first, false, harness.clock.Now())
	if got := len(harness.session.published()); got != 19 {
		t.Fatalf("duplicate report changed the Observation count to %d", got)
	}

	harness.advance(t, time.Second)
	harness.deliverAt(t, 1, fixtureTopic, laterDateReportMessage(t), false, harness.clock.Now())
	if got := len(harness.session.published()); got != 38 {
		t.Fatalf("later-date report published %d Observations, want 38", got)
	}

	harness.advance(t, 3*fixtureUploadInterval-time.Second)
	if health := harness.session.health(); len(health) != 1 {
		t.Fatalf("health before the refreshed deadline = %#v, want only the healthy transition", health)
	}
	harness.advance(t, time.Second)
	health := harness.session.health()
	if len(health) != 2 || health[1].ReasonCode != stationSilentReason {
		t.Fatalf("the accepted later report did not refresh the deadline: health = %#v", health)
	}
}

// TestRuntimeStaleGenerationCompletionCannotMutateState protects the
// generation fence and runtime evidence preservation: an SDK effect that
// completes after its MQTT generation ended must not commit health or
// availability, but the chain's already-acquired Observations still drain in
// catalog order, and recovery still requires a new live report.
func TestRuntimeStaleGenerationCompletionCannotMutateState(t *testing.T) {
	t.Parallel()

	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	harness.establish(t)
	harness.advance(t, 3*fixtureUploadInterval)
	effects.runAll()
	harness.pump(t)
	if health := harness.session.health(); len(health) != 1 || health[0].ReasonCode != stationSilentReason {
		t.Fatalf("precondition: health = %#v, want one silent report", health)
	}

	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	if len(effects.pending) != 1 {
		t.Fatalf("pending effects = %d, want the health-recovery acknowledgement", len(effects.pending))
	}

	harness.submit(t, upstreamUnavailable{generation: 1})
	effects.drain(t, harness)

	if harness.coordinator.health != adapter.HealthUnhealthy {
		t.Fatalf("coordinator health = %q after a stale completion, want unhealthy",
			harness.coordinator.health)
	}
	reports := harness.session.health()
	if last := reports[len(reports)-1]; last.Status != adapter.HealthUnhealthy ||
		last.ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("last health report = %#v, want unhealthy %s after the re-affirmation",
			last, externalSystemUnavailableReason)
	}
	if availability := harness.session.availability(); len(availability) != 0 {
		t.Fatalf("an ended generation reported %d availability entries", len(availability))
	}
	snapshot := snapshotFor(t, testConfig(t))
	published := publishedEntityIDs(harness.session.published())
	if len(published) != len(snapshot.entities) {
		t.Fatalf("already-acquired Observations = %d, want %d drained in catalog order",
			len(published), len(snapshot.entities))
	}
	for index, entityID := range published {
		if entityID != snapshot.entities[index].EntityID {
			t.Fatalf("Observation[%d] Entity = %q, want %q in catalog order",
				index, entityID, snapshot.entities[index].EntityID)
		}
	}
	if harness.coordinator.activeWork != nil {
		t.Fatal("the drained chain stayed active")
	}
}

// TestRuntimePendingReportOverflowEndsGeneration protects the bounded ordered
// queue: exceeding it ends the MQTT generation with the same visible behavior
// as a callback relay overflow instead of evicting a chosen report.
func TestRuntimePendingReportOverflowEndsGeneration(t *testing.T) {
	t.Parallel()

	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	var disconnectCause error
	harness.submit(t, generationStarting{generation: 1, result: make(chan error, 1)})
	harness.submit(t, generationEstablished{
		generation:   1,
		disconnect:   func(cause error) { disconnectCause = cause },
		subscribedAt: harness.clock.Now(),
	})

	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	if harness.coordinator.activeWork == nil {
		t.Fatal("the first report did not start the ordered chain")
	}
	for sequence := 1; sequence <= pendingReportLimit; sequence++ {
		harness.deliver(t, fixtureTopic, fixtureReportMessage(t, sequence), false)
	}
	if got := harness.coordinator.pendingReportCount(); got != pendingReportLimit {
		t.Fatalf("pending reports = %d, want the full bound of %d", got, pendingReportLimit)
	}
	if disconnectCause != nil {
		t.Fatalf("the bound ended the generation early: %v", disconnectCause)
	}

	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, pendingReportLimit+1), false)
	if !errors.Is(disconnectCause, errPendingReportOverflow) {
		t.Fatalf("disconnect cause = %v, want pending-report overflow", disconnectCause)
	}
	if !harness.coordinator.generationEnded {
		t.Fatal("the MQTT generation stayed live after overflow")
	}
	if harness.coordinator.pendingWork != nil {
		t.Fatal("overflow kept its copied work instead of ending the generation")
	}
	effects.drain(t, harness)
	if !hasUnhealthy(harness.session.health(), externalSystemUnavailableReason) {
		t.Fatalf("health after overflow = %#v, want unhealthy %s",
			harness.session.health(), externalSystemUnavailableReason)
	}
}

// assertNoHealthyAfterSilence fails when any healthy transition follows the
// station-silent unhealthy transition in the recorded order, which is exactly
// what stale pre-silence evidence would produce.
func assertNoHealthyAfterSilence(t *testing.T, reports []adapter.HealthReport) {
	t.Helper()
	silenceIndex := -1
	for index, report := range reports {
		if report.Status == adapter.HealthUnhealthy && report.ReasonCode == stationSilentReason {
			silenceIndex = index
			break
		}
	}
	if silenceIndex < 0 {
		t.Fatalf("health = %#v, want the silent transition", reports)
	}
	for _, report := range reports[silenceIndex+1:] {
		if report.Status == adapter.HealthHealthy {
			t.Fatalf("stale pre-silence evidence restored health: %#v", reports)
		}
	}
}

// hasUnhealthy reports whether one recorded transition is an unhealthy
// transition with the given reason.
func hasUnhealthy(reports []adapter.HealthReport, reason string) bool {
	for _, report := range reports {
		if report.Status == adapter.HealthUnhealthy && report.ReasonCode == reason {
			return true
		}
	}
	return false
}

// TestRuntimeAvailabilityRejectionIsSupersededAndResent protects the
// generation race: an availability report Core rejects because the adapter is
// unhealthy does not fail the process, re-sends after recovery instead of
// retrying the old report, and never suppresses the report's Observations.
func TestRuntimeAvailabilityRejectionIsSupersededAndResent(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.session.availabilityErr = &adapter.EntityAvailabilityRejectedError{
		Code: adapter.EntityAvailabilityAdapterUnhealthy, Message: "adapter is unhealthy",
	}
	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	if got := len(harness.session.published()); got != 19 {
		t.Fatalf("Observations after a superseded availability report = %d, want 19", got)
	}
	if harness.terminal != nil {
		t.Fatalf("superseded availability was terminal: %v", harness.terminal)
	}

	harness.session.availabilityErr = nil
	harness.advance(t, time.Second)
	harness.deliverAt(t, 1, fixtureTopic, laterDateReportMessage(t), false, harness.clock.Now())
	resent := harness.session.availability()
	if len(resent) != 19 {
		t.Fatalf("availability after recovery = %d entries, want all 19 resent", len(resent))
	}
}

// TestRuntimeTerminalSessionFailuresStopTheAdapter protects that an SDK failure
// other than cancellation is terminal, so the Adapter never compensates with a
// second Observation.
func TestRuntimeTerminalSessionFailuresStopTheAdapter(t *testing.T) {
	t.Parallel()

	publicationFailure := errors.New("sentinel publication failure")
	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.session.publishErr = publicationFailure
	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	if !errors.Is(harness.terminal, publicationFailure) {
		t.Fatalf("terminal error = %v, want the publication failure", harness.terminal)
	}
	if got := len(harness.session.published()); got != 0 {
		t.Fatalf("published %d Observations after a terminal failure", got)
	}

	healthFailure := errors.New("sentinel health failure")
	second := newRuntimeHarness(t, nil)
	second.establish(t)
	second.session.healthErr = healthFailure
	second.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	if !errors.Is(second.terminal, healthFailure) {
		t.Fatalf("terminal error = %v, want the health failure", second.terminal)
	}
}

// TestRuntimeStaleEntitiesBecomeUnavailableAtExactlyThreeIntervals protects
// per-Entity freshness timing and recovery under a fake clock.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func TestRuntimeStaleEntitiesBecomeUnavailableAtExactlyThreeIntervals(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	harness.deliverAt(t, 1, fixtureTopic, fixtureReportMessage(t, 0), false, harness.clock.Now())

	snapshot := snapshotFor(t, testConfig(t))
	entityIDOf := func(key string) string {
		for _, route := range snapshot.entities {
			if route.EntityKey == key {
				return route.EntityID
			}
		}
		t.Fatalf("no Entity %q", key)
		return ""
	}
	notObservedLater := []string{
		"indoor-temperature", "indoor-humidity", "absolute-pressure", "outdoor-temperature",
		"outdoor-humidity", "wind-speed", "solar-radiation", "uv-index", "rain-rate", "daily-rain",
		"monthly-rain",
	}
	firstBatch := harness.session.availability()
	if len(firstBatch) != len(snapshot.entities) {
		t.Fatalf("first availability batch = %d entries, want %d", len(firstBatch), len(snapshot.entities))
	}
	for index, report := range firstBatch {
		if report.EntityID != snapshot.entities[index].EntityID {
			t.Fatalf("availability[%d] Entity = %q, want %q", index, report.EntityID, snapshot.entities[index].EntityID)
		}
		if report.Status != adapter.AvailabilityAvailable || report.ReasonCode != "" {
			t.Fatalf("availability[%d] = %#v, want available with no reason", index, report)
		}
	}

	harness.advance(t, fixtureUploadInterval)
	missing := loadFixture(t, "gw2000-ws90-missing.txt")
	harness.deliverAt(t, 1, fixtureTopic, append(missing, []byte("&ignored_sequence=2")...), false, harness.clock.Now())
	baseline := len(harness.session.availability())
	if baseline != len(firstBatch) {
		t.Fatalf("a partially refreshed report added %d availability entries, want none",
			baseline-len(firstBatch))
	}

	harness.advance(t, 2*fixtureUploadInterval-time.Second)
	if got := len(harness.session.availability()); got != baseline {
		t.Fatalf("availability transitioned one second early to %d entries", got)
	}
	harness.advance(t, time.Second)
	stale := harness.session.availability()[baseline:]
	if len(stale) != len(notObservedLater) {
		t.Fatalf("stale availability batch = %d entries, want %d", len(stale), len(notObservedLater))
	}
	for index, key := range notObservedLater {
		if stale[index].EntityID != entityIDOf(key) {
			t.Fatalf("stale[%d] = %q, want %q in catalog order", index, stale[index].EntityID, key)
		}
		if stale[index].Status != adapter.AvailabilityUnavailable ||
			stale[index].ReasonCode != measurementStaleReason {
			t.Fatalf("stale[%d] = %#v, want unavailable %s", index, stale[index], measurementStaleReason)
		}
	}

	// The report timeout is exactly three upload intervals, so at T+64 the
	// station silence and the last-observed Entities' stale interval coincide.
	// Station silence wins: the Adapter reports unhealthy, Core clears every
	// Entity's availability, and no stale batch is sent for an adapter that
	// cannot be believed.
	afterStale := len(harness.session.availability())
	harness.advance(t, fixtureUploadInterval)
	health := harness.session.health()
	if len(health) != 2 || health[0].Status != adapter.HealthHealthy ||
		health[1].ReasonCode != stationSilentReason {
		t.Fatalf("health at the report timeout = %#v, want a silent transition", health)
	}
	if got := len(harness.session.availability()); got != afterStale {
		t.Fatalf("station silence sent %d extra availability entries", got-afterStale)
	}

	recoveredAt := len(harness.session.availability())
	harness.deliverAt(t, 1, fixtureTopic, laterDateReportMessage(t), false, harness.clock.Now())
	recovered := harness.session.availability()[recoveredAt:]
	if len(recovered) != len(snapshot.entities) {
		t.Fatalf("recovery availability = %d entries, want %d", len(recovered), len(snapshot.entities))
	}
	for index, report := range recovered {
		if report.Status != adapter.AvailabilityAvailable {
			t.Fatalf("recovered[%d] = %#v, want available", index, report)
		}
		if report.EntityID != snapshot.entities[index].EntityID {
			t.Fatalf("recovered[%d] = %q, want catalog order", index, report.EntityID)
		}
	}
}

// TestRuntimeStaleDeadlineDoesNotSpinWhileAChainIsActive protects ordered
// draining: a stale deadline that fires while a report chain is still
// publishing waits for the chain instead of re-arming an already due deadline
// and spinning the event loop.
func TestRuntimeStaleDeadlineDoesNotSpinWhileAChainIsActive(t *testing.T) {
	t.Parallel()

	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	harness.establish(t)
	harness.deliverAt(t, 1, fixtureTopic, fixtureReportMessage(t, 0), false, harness.clock.Now())
	effects.drain(t, harness)
	baseline := len(harness.session.availability())
	if baseline != 19 {
		t.Fatalf("precondition: availability = %d entries, want 19", baseline)
	}

	harness.advance(t, fixtureUploadInterval)
	missing := append(loadFixture(t, "gw2000-ws90-missing.txt"), []byte("&ignored_sequence=2")...)
	harness.deliverAt(t, 1, fixtureTopic, missing, false, harness.clock.Now())
	if harness.coordinator.activeWork == nil {
		t.Fatal("precondition: no ordered chain is active")
	}

	harness.advance(t, 2*fixtureUploadInterval-time.Second)
	if harness.coordinator.hasPendingStale() {
		t.Fatal("the stale deadline fired one second early")
	}
	harness.advance(t, time.Second)
	if !harness.coordinator.hasPendingStale() {
		t.Fatal("the stale deadline did not enqueue an ordered recomputation")
	}
	if harness.coordinator.measurementTimer != nil {
		t.Fatal("the stale deadline re-armed an already due deadline while a chain was active")
	}
	if got := len(harness.session.availability()); got != baseline {
		t.Fatalf("availability changed while the chain was active: %d entries", got)
	}

	effects.drain(t, harness)
	stale := harness.session.availability()[baseline:]
	if len(stale) != 11 {
		t.Fatalf("stale availability after the chain finished = %d entries, want 11", len(stale))
	}
	for _, report := range stale {
		if report.Status != adapter.AvailabilityUnavailable || report.ReasonCode != measurementStaleReason {
			t.Fatalf("stale report = %#v, want unavailable %s", report, measurementStaleReason)
		}
	}
}

// TestRuntimeKeepsProcessingWhileAnSDKEffectIsPending protects that the
// coordinator never blocks its event loop on a NATS acknowledgement: a blocked
// Observation publication must not stop a later health transition from being
// applied.
func TestRuntimeKeepsProcessingWhileAnSDKEffectIsPending(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarnessWithEffects(t, nil, &waitGroupEffects{})
	harness.session.publishGate = make(chan struct{})
	runResult := make(chan error, 1)
	go func() { runResult <- harness.coordinator.run() }()

	harness.send(t, generationStarting{generation: 1, result: make(chan error, 1)})
	harness.send(t, generationEstablished{
		generation:   1,
		disconnect:   func(error) {},
		subscribedAt: harness.clock.Now(),
	})
	harness.send(t, reportReceived{
		generation: 1,
		message:    fixtureMessage(t),
	})
	eventually(t, "a blocked Observation publication", func() bool {
		select {
		case <-harness.session.observedPublish():
			return true
		default:
			return false
		}
	})
	harness.send(t, upstreamUnavailable{generation: 1})

	eventually(t, "unhealthy transition while a publication is pending", func() bool {
		for _, report := range harness.session.health() {
			if report.Status == adapter.HealthUnhealthy && report.ReasonCode == externalSystemUnavailableReason {
				return true
			}
		}
		return false
	})
	// The in-flight publication is not cancelled by the disconnect: it already
	// acquired its observation and keeps its SDK envelope through retry.
	close(harness.session.publishGate)
	snapshot := snapshotFor(t, testConfig(t))
	eventually(t, "the accepted report's Observations to drain after the disconnect", func() bool {
		return len(harness.session.published()) == len(snapshot.entities)
	})
	harness.coordinator.cancel()
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("coordinator run error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not stop after cancellation")
	}
}

// TestRuntimeShutdownJoinsInFlightPublications protects that shutdown cancels
// and joins every tracked effect instead of returning while a publication is
// still in flight.
func TestRuntimeShutdownJoinsInFlightPublications(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarnessWithEffects(t, nil, &waitGroupEffects{})
	harness.session.publishGate = make(chan struct{})
	runResult := make(chan error, 1)
	go func() { runResult <- harness.coordinator.run() }()

	harness.send(t, generationStarting{generation: 1, result: make(chan error, 1)})
	harness.send(t, generationEstablished{
		generation:   1,
		disconnect:   func(error) {},
		subscribedAt: harness.clock.Now(),
	})
	harness.send(t, reportReceived{
		generation: 1,
		message:    fixtureMessage(t),
	})
	eventually(t, "a blocked Observation publication", func() bool {
		select {
		case <-harness.session.observedPublish():
			return true
		default:
			return false
		}
	})

	harness.coordinator.cancel()
	select {
	case err := <-runResult:
		t.Fatalf("shutdown returned while a publication was in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(harness.session.publishGate)
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("coordinator run error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not join its in-flight publication")
	}
}

// TestRunRegistersBothSlotsBeforeConnectingMQTT protects the fixed startup
// order and generation recovery: both Device slots register before the first
// MQTT dial, health stays unknown until evidence, and a disconnect reports
// external_system_unavailable and reconnects.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func TestRunRegistersBothSlotsBeforeConnectingMQTT(t *testing.T) {
	t.Parallel()

	clock := newFakeClock(fixtureReceivedAt)
	trace := &callLog{}
	session := &recordingSession{trace: trace, publishStarted: make(chan struct{})}
	dialer := newFakeDialer()
	dialer.trace = trace
	ecowitt, err := newAdapter(session, testConfig(t), slog.New(slog.DiscardHandler), dialer)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	ecowitt.clock = clock
	ecowitt.retryDelay = func(time.Duration) time.Duration { return 0 }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runResult := make(chan error, 1)
	go func() { runResult <- ecowitt.Run(ctx) }()

	eventually(t, "the first MQTT dial", func() bool { return len(dialer.configsSeen()) >= 1 })
	eventually(t, "the second registration", func() bool { return len(session.registrationsSnapshot()) >= 2 })
	calls := trace.snapshot()
	registerIndex := indexOfPrefix(t, calls, "register:outdoor-array")
	dialIndex := indexOfPrefix(t, calls, "dial:")
	if registerIndex < 0 || dialIndex < 0 || registerIndex > dialIndex {
		t.Fatalf("startup order = %v, want both registrations before the first dial", calls)
	}
	registration := session.registrationsSnapshot()[0]
	if registration.BindingKey != string(gatewaySlot) || registration.Device.Kind != deviceKindSensor {
		t.Fatalf("first registration = %#v, want the gateway sensor slot", registration)
	}
	if reports := session.health(); len(reports) != 0 {
		t.Fatalf("health reports before any evidence = %#v, want none", reports)
	}
	dial := dialer.configsSeen()[0]
	if dial.URL != testConfig(t).MQTTURL || dial.ClientID != testConfig(t).MQTTClientID {
		t.Fatalf("dial configuration = %#v, want the validated Adapter configuration", dial)
	}
	connection := dialer.connection
	connection.mutex.Lock()
	subscribedTopic, subscribedQoS := connection.topic, connection.qos
	connection.mutex.Unlock()
	if subscribedTopic != fixtureTopic || subscribedQoS != mqttQoS {
		t.Fatalf("subscription = %q at QoS %d, want the exact configured topic at QoS 1",
			subscribedTopic, subscribedQoS)
	}

	dialer.push(fixtureMessage(t))
	eventually(t, "a healthy transition", func() bool {
		for _, report := range session.health() {
			if report.Status == adapter.HealthHealthy {
				return true
			}
		}
		return false
	})

	connection.lost <- errors.New("broker stopped")
	eventually(t, "the external-system-unavailable transition", func() bool {
		for _, report := range session.health() {
			if report.Status == adapter.HealthUnhealthy && report.ReasonCode == externalSystemUnavailableReason {
				return true
			}
		}
		return false
	})
	eventually(t, "a reconnect attempt", func() bool { return len(dialer.configsSeen()) >= 2 })

	cancel()
	select {
	case runErr := <-runResult:
		if runErr != nil {
			t.Fatalf("Run error = %v, want graceful shutdown", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if !connection.closed {
		t.Fatal("the MQTT connection was not closed on shutdown")
	}
}

// TestRunReportsConnectAndSubscribeFailuresAndRetries protects that a
// failure to reach the broker and a failure to establish the subscription both
// report hearth.external_system_unavailable immediately and keep retrying with
// Adapter-owned reconnect.
func TestRunReportsConnectAndSubscribeFailuresAndRetries(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		mutate    func(*fakeDialer)
		wantAfter int
	}{
		{name: "connect failure", mutate: func(dialer *fakeDialer) {
			dialer.dialErr = errors.New("sentinel connect failure")
		}, wantAfter: 2},
		{name: "subscribe failure", mutate: func(dialer *fakeDialer) {
			dialer.connection.subscribeErr = errors.New("sentinel subscribe failure")
		}, wantAfter: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			session := &recordingSession{publishStarted: make(chan struct{})}
			dialer := newFakeDialer()
			testCase.mutate(dialer)
			ecowitt, err := newAdapter(session, testConfig(t), slog.New(slog.DiscardHandler), dialer)
			if err != nil {
				t.Fatalf("newAdapter: %v", err)
			}
			ecowitt.clock = newFakeClock(fixtureReceivedAt)
			ecowitt.retryDelay = func(time.Duration) time.Duration { return 0 }

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			runResult := make(chan error, 1)
			go func() { runResult <- ecowitt.Run(ctx) }()

			eventually(t, "the external-system-unavailable transition", func() bool {
				return hasUnhealthy(session.health(), externalSystemUnavailableReason)
			})
			eventually(t, "a retry attempt", func() bool {
				return len(dialer.configsSeen()) >= testCase.wantAfter
			})
			cancel()
			select {
			case runErr := <-runResult:
				if runErr != nil {
					t.Fatalf("Run error = %v, want graceful shutdown", runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return after cancellation")
			}
		})
	}
}

// TestRuntimeDisconnectDrainsAcceptedReportEvidence protects runtime evidence
// preservation: a disconnect must not discard accepted reports waiting behind
// the active chain or the active chain's remaining Observations. All of them
// still publish in original order, while availability from the dead generation
// never becomes current.
func TestRuntimeDisconnectDrainsAcceptedReportEvidence(t *testing.T) {
	t.Parallel()

	const reportCount = 4
	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	harness.establish(t)
	for sequence := range reportCount {
		harness.deliver(t, fixtureTopic, fixtureReportMessage(t, sequence), false)
	}
	if harness.coordinator.activeWork == nil {
		t.Fatal("the first report did not start the ordered chain")
	}
	if got := harness.coordinator.pendingReportCount(); got != reportCount-1 {
		t.Fatalf("pending reports = %d, want %d", got, reportCount-1)
	}

	harness.submit(t, upstreamUnavailable{generation: 1})
	if !harness.coordinator.generationEnded {
		t.Fatal("the MQTT generation stayed live after the disconnect")
	}
	effects.drain(t, harness)

	if availability := harness.session.availability(); len(availability) != 0 {
		t.Fatalf("a dead generation reported %d availability entries", len(availability))
	}
	snapshot := snapshotFor(t, testConfig(t))
	expected := make([]string, 0, reportCount*len(snapshot.entities))
	for range reportCount {
		for _, route := range snapshot.entities {
			expected = append(expected, route.EntityID)
		}
	}
	published := publishedEntityIDs(harness.session.published())
	if len(published) != len(expected) {
		t.Fatalf("published Observations = %d, want %d drained in original order",
			len(published), len(expected))
	}
	for index, entityID := range published {
		if entityID != expected[index] {
			t.Fatalf("Observation[%d] Entity = %q, want %q", index, entityID, expected[index])
		}
	}
	if harness.coordinator.activeWork != nil || len(harness.coordinator.pendingWork) != 0 {
		t.Fatal("queued report evidence stayed after draining")
	}
}

// TestRuntimeSilenceRevisionBlocksStaleHealthyCompletion protects the health
// revision: when the report deadline fires while a healthy acknowledgement is
// still in flight, the stale completion must not restore health, and recovery
// still requires a new live report.
func TestRuntimeSilenceRevisionBlocksStaleHealthyCompletion(t *testing.T) {
	t.Parallel()

	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	harness.establish(t)
	harness.advance(t, 3*fixtureUploadInterval)
	effects.runAll()
	harness.pump(t)
	if !hasUnhealthy(harness.session.health(), stationSilentReason) {
		t.Fatalf("precondition: health = %#v, want one silent transition", harness.session.health())
	}

	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	if len(effects.pending) != 1 {
		t.Fatalf("pending effects = %d, want the in-flight healthy acknowledgement", len(effects.pending))
	}
	// The report deadline fires again while the healthy acknowledgement is
	// still pending.
	harness.advance(t, 3*fixtureUploadInterval)

	effects.drain(t, harness)
	if harness.coordinator.health != adapter.HealthUnhealthy {
		t.Fatalf("coordinator health = %q after a stale healthy completion, want unhealthy",
			harness.coordinator.health)
	}
	health := harness.session.health()
	if last := health[len(health)-1]; last.Status != adapter.HealthUnhealthy ||
		last.ReasonCode != stationSilentReason {
		t.Fatalf("last health report = %#v, want unhealthy %s", last, stationSilentReason)
	}

	// A new live report is required before health recovers.
	harness.deliverAt(t, 1, fixtureTopic, laterDateReportMessage(t), false, harness.clock.Now())
	effects.drain(t, harness)
	health = harness.session.health()
	if last := health[len(health)-1]; last.Status != adapter.HealthHealthy {
		t.Fatalf("recovery after new live evidence = %#v, want the last transition healthy", health)
	}
}

// TestRuntimeQueuedPreSilenceEvidenceCannotRestoreHealth protects the
// evidence-revision fence: two reports are accepted and one still waits behind
// the active chain when the station goes silent, so neither report's evidence
// may restore health or availability afterwards. Both still publish their
// already-acquired Observations in original order, and a report accepted after
// the timeout recovers normally.
func TestRuntimeQueuedPreSilenceEvidenceCannotRestoreHealth(t *testing.T) {
	t.Parallel()

	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	harness.establish(t)

	// The first report starts the ordered chain and holds it open; the second is
	// accepted as copied, unprocessed evidence behind it.
	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 0), false)
	harness.deliver(t, fixtureTopic, fixtureReportMessage(t, 1), false)
	if got := harness.coordinator.pendingReportCount(); got != 1 {
		t.Fatalf("pending reports = %d, want the one queued report behind the active chain", got)
	}

	// The report timeout elapses before either chain finishes. The unhealthy
	// transition is queued behind the in-flight healthy acknowledgement, so only
	// the coordinator's local state shows it yet.
	harness.advance(t, 3*fixtureUploadInterval)
	if harness.coordinator.health != adapter.HealthUnhealthy {
		t.Fatalf("health at the report timeout = %q, want unhealthy", harness.coordinator.health)
	}
	if harness.coordinator.evidenceRevision == 0 {
		t.Fatal("the station silence did not revision the report evidence")
	}

	effects.drain(t, harness)

	if harness.coordinator.health != adapter.HealthUnhealthy {
		t.Fatalf("coordinator health = %q, want unhealthy: pre-silence evidence restored health",
			harness.coordinator.health)
	}
	assertNoHealthyAfterSilence(t, harness.session.health())
	if availability := harness.session.availability(); len(availability) != 0 {
		t.Fatalf("pre-silence evidence reported %d availability entries", len(availability))
	}

	snapshot := snapshotFor(t, testConfig(t))
	expected := make([]string, 0, 2*len(snapshot.entities))
	for range 2 {
		for _, route := range snapshot.entities {
			expected = append(expected, route.EntityID)
		}
	}
	published := publishedEntityIDs(harness.session.published())
	if !slices.Equal(published, expected) {
		t.Fatalf("published %d Observations, want both reports' %d in original order",
			len(published), len(expected))
	}

	// Only a report accepted after the timeout is fresh evidence.
	harness.deliverAt(t, 1, fixtureTopic, laterDateReportMessage(t), false, harness.clock.Now())
	effects.drain(t, harness)
	if harness.coordinator.health != adapter.HealthHealthy {
		t.Fatalf("health after a post-timeout report = %q, want healthy", harness.coordinator.health)
	}
	health := harness.session.health()
	if last := health[len(health)-1]; last.Status != adapter.HealthHealthy {
		t.Fatalf("recovery after a post-timeout report = %#v, want the last transition healthy", health)
	}
	if got := len(harness.session.availability()); got != len(snapshot.entities) {
		t.Fatalf("recovery availability = %d entries, want %d", got, len(snapshot.entities))
	}
	if got := len(harness.session.published()); got != 3*len(snapshot.entities) {
		t.Fatalf("published Observations after recovery = %d, want %d", got, 3*len(snapshot.entities))
	}
}

// TestRuntimeSilenceRevokesQueuedEvidenceAfterCommittedRecovery protects the
// same-generation silence case: the Adapter has already committed healthy while
// the MQTT generation stays live, a report accepted before the silence waits
// behind the active chain, and the station then goes silent. That queued
// evidence predates the silence, so it publishes its Observations only and
// cannot restore health; a report accepted after the timeout recovers again.
func TestRuntimeSilenceRevokesQueuedEvidenceAfterCommittedRecovery(t *testing.T) {
	t.Parallel()

	effects := &deferredEffects{}
	harness := newRuntimeHarnessWithEffects(t, nil, effects)
	harness.establish(t)

	// Report A recovers the Adapter and is held open at its availability stage.
	harness.deliverAt(t, 1, fixtureTopic, fixtureReportMessage(t, 0), false, harness.clock.Now())
	effects.runAll()
	harness.pump(t)
	if harness.coordinator.health != adapter.HealthHealthy {
		t.Fatalf("precondition: health = %q, want the committed healthy transition",
			harness.coordinator.health)
	}

	// Report B is accepted while A's chain is still mid-flight, so B waits as
	// copied evidence behind it.
	harness.deliverAt(t, 1, fixtureTopic, fixtureReportMessage(t, 1), false, harness.clock.Now())
	if got := harness.coordinator.pendingReportCount(); got != 1 {
		t.Fatalf("pending reports = %d, want the one queued report behind the active chain", got)
	}

	// The report timeout elapses while B still waits.
	harness.advance(t, 3*fixtureUploadInterval)
	if harness.coordinator.health != adapter.HealthUnhealthy {
		t.Fatalf("health at the report timeout = %q, want unhealthy", harness.coordinator.health)
	}
	silenceEvidenceRevision := harness.coordinator.evidenceRevision

	effects.drain(t, harness)

	reports := harness.session.health()
	assertNoHealthyAfterSilence(t, reports)
	if harness.coordinator.health != adapter.HealthUnhealthy {
		t.Fatalf("coordinator health = %q, want unhealthy", harness.coordinator.health)
	}

	snapshot := snapshotFor(t, testConfig(t))
	published := publishedEntityIDs(harness.session.published())
	if got, want := len(published), 2*len(snapshot.entities); got != want {
		t.Fatalf("published Observations = %d, want both reports' %d", got, want)
	}
	for index := range snapshot.entities {
		if published[index] != snapshot.entities[index].EntityID {
			t.Fatalf("Observation[%d] Entity = %q, want catalog order", index, published[index])
		}
	}

	// Only a report accepted after the silence is fresh evidence: it recovers
	// health at the current revision.
	harness.deliverAt(t, 1, fixtureTopic, laterDateReportMessage(t), false, harness.clock.Now())
	effects.drain(t, harness)
	if harness.coordinator.evidenceRevision != silenceEvidenceRevision {
		t.Fatalf("evidence revision = %d, want the recovery to stay at the silence revision %d",
			harness.coordinator.evidenceRevision, silenceEvidenceRevision)
	}
	health := harness.session.health()
	if last := health[len(health)-1]; last.Status != adapter.HealthHealthy {
		t.Fatalf("recovery after a post-timeout report = %#v, want the last transition healthy", health)
	}
}

// TestRuntimeSerializesHealthSubmissions protects Core health ordering: while a
// healthy SetHealth call is in flight, an unhealthy transition must not open a
// second concurrent call that could reorder Core's view. The unhealthy decision
// is queued and issued in order after the healthy call completes.
func TestRuntimeSerializesHealthSubmissions(t *testing.T) {
	t.Parallel()

	handler := &bufferHandler{}
	harness := newRuntimeHarnessWithEffects(t, slog.New(handler), &waitGroupEffects{})
	harness.session.healthGate = make(chan struct{})
	harness.session.healthStarted = make(chan struct{})
	runResult := make(chan error, 1)
	go func() { runResult <- harness.coordinator.run() }()

	harness.send(t, generationStarting{generation: 1, result: make(chan error, 1)})
	harness.send(t, generationEstablished{
		generation:   1,
		disconnect:   func(error) {},
		subscribedAt: harness.clock.Now(),
	})
	harness.send(t, reportReceived{
		generation: 1,
		message:    fixtureMessage(t),
	})
	eventually(t, "the healthy health call to start", func() bool {
		select {
		case <-harness.session.healthStarted:
			return true
		default:
			return false
		}
	})

	harness.send(t, upstreamUnavailable{generation: 1})
	eventually(t, "the unhealthy transition to be recorded", func() bool {
		return strings.Contains(handler.output(), "adapter.unhealthy")
	})
	if got := harness.session.healthMaxInFlight.Load(); got != 1 {
		t.Fatalf("concurrent SetHealth calls = %d, want 1 while a healthy call is in flight", got)
	}

	close(harness.session.healthGate)
	eventually(t, "the unhealthy SetHealth after the healthy call", func() bool {
		return hasUnhealthy(harness.session.health(), externalSystemUnavailableReason)
	})
	if got := harness.session.healthMaxInFlight.Load(); got != 1 {
		t.Fatalf("concurrent SetHealth calls = %d, want serialized health submissions", got)
	}
	health := harness.session.health()
	last := health[len(health)-1]
	if last.Status != adapter.HealthUnhealthy || last.ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("last health report = %#v, want unhealthy %s", last, externalSystemUnavailableReason)
	}

	harness.coordinator.cancel()
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("coordinator run error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not stop after cancellation")
	}
}

// TestRuntimeReconnectResetsDuplicateSignature protects connection-local
// duplicate suppression: an immediate redelivery is suppressed within one MQTT
// generation, but the same report signature on a new connection is fresh live
// evidence.
func TestRuntimeReconnectResetsDuplicateSignature(t *testing.T) {
	t.Parallel()

	harness := newRuntimeHarness(t, nil)
	harness.establish(t)
	payload := fixtureReportMessage(t, 0)
	harness.deliver(t, fixtureTopic, payload, false)
	harness.deliver(t, fixtureTopic, payload, false)
	if got := len(harness.session.published()); got != 19 {
		t.Fatalf("published Observations = %d, want 19: the exact duplicate was accepted", got)
	}

	harness.submit(t, generationStarting{generation: 2, result: make(chan error, 1)})
	harness.submit(t, generationEstablished{
		generation:   2,
		disconnect:   func(error) {},
		subscribedAt: harness.clock.Now(),
	})
	harness.deliver(t, fixtureTopic, payload, false)
	if got := len(harness.session.published()); got != 38 {
		t.Fatalf("published Observations = %d, want 38: a new connection suppressed fresh evidence", got)
	}
}

// indexOfPrefix returns the trace index of the first entry with a prefix.
func indexOfPrefix(t *testing.T, entries []string, prefix string) int {
	t.Helper()
	for index, entry := range entries {
		if strings.HasPrefix(entry, prefix) {
			return index
		}
	}
	t.Fatalf("trace %v has no entry with prefix %q", entries, prefix)
	return -1
}

// eventually waits for one condition, failing the test after a bounded wait.
func eventually(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
