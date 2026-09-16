package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsnats "github.com/mholtzscher/hearth/internal/modules/automations/nats"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

// Fixed canonical identities for the two recovery facts. They are distinct fact
// identities so one Automation can admit each independently; both are canonical
// lowercase UUIDv7 with the RFC 4122 variant, matching the strict schemas.
const (
	recoveryFreshFactID   = "fct_01890f47-7a6b-7c4d-8e9f-0123456789f1"
	recoveryStaleFactID   = "fct_01890f47-7a6b-7c4d-8e9f-0123456789f2"
	recoveryFreshObsID    = "obs_01890f47-7a6b-7c4d-8e9f-0123456789c1"
	recoveryStaleObsID    = "obs_01890f47-7a6b-7c4d-8e9f-0123456789c2"
	recoveryCorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789e1"
)

// TestAutomationConsumerRecoversFreshFactAndSkipsStaleFactAcrossRestart
// protects A12 at the app boundary. One Core process creates the named durable
// automation Device Fact consumer and an HTTP-created Observation Automation;
// the process then stops. Two Device Facts are stored by the broker while no
// Core process is consuming: one emitted now and one emitted past the fixed
// freshness bound. The next Core process must resume that same durable consumer
// from its acknowledgement floor, execute the fresh Fact's Command for real, and
// record a per-Automation `stale_fact` Skip for the old Fact without dispatching
// a Command or recording a Run. A further restart with nothing pending must not
// invent a duplicate outcome.
//
// It fails if first provisioning jumps the tail across the fact backlog, if a
// restart recreates the consumer at the tail, if the stale Fact is executed
// instead of explained, or if durable recovery replays an acknowledged Fact. No
// lower-level test crosses the broker, the durable consumer, admission, the
// devices Command path, and HTTP history together, which is the boundary this
// test owns.
func TestAutomationConsumerRecoversFreshFactAndSkipsStaleFactAcrossRestart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")

	// This publisher owns the two recovery facts exactly as the broker stores
	// them. It never depends on Core's relay, so it can publish while Core is
	// stopped and prove the durable consumer, not the outbox, recovering them.
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	validator := mustDeviceFactValidator(t)

	// First process: create the durable consumer and the Automation.
	firstAddress, stopFirst, firstErrors := startDeviceFactsCore(
		ctx, t, server.ClientURL(), databasePath,
	)
	waitForCoreHealthz(ctx, t, firstAddress, firstErrors)
	waitForCoreReady(ctx, t, firstAddress, firstErrors)
	adapter := startSliceAdapter(ctx, t, server, firstAddress)
	defer adapter.stop()
	consumer := deviceFactConsumer(ctx, t, js)
	consumerBefore := deviceFactConsumerInfo(ctx, t, consumer)
	if consumerBefore.Name != automationsnats.DeviceFactConsumerName {
		t.Fatalf("consumer name = %q, want the durable %q",
			consumerBefore.Name, automationsnats.DeviceFactConsumerName)
	}
	if consumerBefore.Config.DeliverPolicy != jetstream.DeliverNewPolicy {
		t.Fatalf("first consumer DeliverPolicy = %v, want DeliverNew",
			consumerBefore.Config.DeliverPolicy)
	}
	if consumerBefore.Created.IsZero() {
		t.Fatal("durable consumer has no creation time")
	}
	automationID := createObservationAutomation(ctx, t, firstAddress, adapter.powerEntityID)
	// The adapter's initialization Observation is admitted and acknowledged
	// before the crash window opens, so the only pending facts afterwards are the
	// two this test publishes.
	waitForDeviceFactConsumerIdle(ctx, t, consumer)
	stopDeviceFactsCore(t, stopFirst, firstErrors)

	// Crash window: no Core process consumes. The fresh Fact stays inside the
	// freshness bound; the stale Fact is one second past it.
	freshEmittedAt := time.Now().UTC()
	staleEmittedAt := freshEmittedAt.Add(-automations.FactMaximumAge - time.Second)
	publishRecoveredObservationFact(ctx, t, js, validator, adapter.powerEntityID,
		recoveryFreshFactID, recoveryFreshObsID, freshEmittedAt, "true")
	publishRecoveredObservationFact(ctx, t, js, validator, adapter.powerEntityID,
		recoveryStaleFactID, recoveryStaleObsID, staleEmittedAt, "true")
	waitForDeviceFactConsumerPending(ctx, t, consumer, 2)

	// Second process: the same durable consumer resumes its acknowledgement floor
	// and recovers both facts as one real execution and one stale Skip.
	secondAddress, stopSecond, secondErrors := startDeviceFactsCore(
		ctx, t, server.ClientURL(), databasePath,
	)
	waitForCoreHealthz(ctx, t, secondAddress, secondErrors)
	waitForCoreReady(ctx, t, secondAddress, secondErrors)
	runID := waitForAutomationRun(ctx, t, secondAddress, automationID)
	entries := waitForRecoveredOutcomes(ctx, t, secondAddress, automationID)
	assertRecoveredRun(ctx, t, secondAddress, automationID, runID, adapter.powerEntityID)
	assertStaleSkip(ctx, t, secondAddress, automationID, entries)
	assertSameConsumerDrained(ctx, t, consumer, consumerBefore)

	// Third process: nothing is pending, so a restart must not replay the
	// acknowledged facts or invent a second outcome for either.
	stopDeviceFactsCore(t, stopSecond, secondErrors)
	thirdAddress, stopThird, thirdErrors := startDeviceFactsCore(
		ctx, t, server.ClientURL(), databasePath,
	)
	waitForCoreHealthz(ctx, t, thirdAddress, thirdErrors)
	waitForCoreReady(ctx, t, thirdAddress, thirdErrors)
	waitForDeviceFactConsumerIdle(ctx, t, consumer)
	assertHistoryUnchanged(ctx, t, thirdAddress, automationID, entries)
	stopDeviceFactsCore(t, stopThird, thirdErrors)
}

// recoveredObservationFactData is the strict Observation Fact payload this test
// encodes directly. It carries only the committed fields the schema requires.
type recoveredObservationFactData struct {
	ObservationID     string          `json:"observation_id"`
	EntityID          string          `json:"entity_id"`
	Disposition       string          `json:"disposition"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	ObservedAt        string          `json:"observed_at"`
}

// publishRecoveredObservationFact stores one schema-valid Observation Fact
// directly on the canonical fact subject with the stable fact identity as
// Nats-Msg-Id, exactly as the broker holds any published fact. The Fact's
// envelope `emitted_at` is controllable so one test can cross the freshness
// bound deterministically without waiting.
func publishRecoveredObservationFact(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	entityID, factID, observationID string,
	emittedAt time.Time,
	value string,
) {
	t.Helper()
	subject, err := natswire.ObservationFactSubject(entityID, natswire.ObservationFactApplied)
	if err != nil {
		t.Fatal(err)
	}
	emitted := emittedAt.UTC().Format(time.RFC3339Nano)
	causationID := observationID
	payload, err := natswire.Encode(validator, contractsv1.ObservationFactSchemaID,
		natswire.Envelope[recoveredObservationFactData]{
			ID:            factID,
			Schema:        contractsv1.ObservationFactSchemaID,
			EmittedAt:     emitted,
			CorrelationID: recoveryCorrelationID,
			CausationID:   &causationID,
			Data: recoveredObservationFactData{
				ObservationID: observationID, EntityID: entityID, Disposition: "applied",
				Value: json.RawMessage(value), AdapterReceivedAt: emitted, ObservedAt: emitted,
			},
		})
	if err != nil {
		t.Fatalf("encode recovered observation fact: %v", err)
	}
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, factID)
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject, Header: headers, Data: payload,
	}); publishErr != nil {
		t.Fatalf("publish recovered observation fact: %v", publishErr)
	}
}

// deviceFactConsumer opens the one durable automation consumer the broker owns.
func deviceFactConsumer(ctx context.Context, t *testing.T, js jetstream.JetStream) jetstream.Consumer {
	t.Helper()
	consumer, err := js.Consumer(ctx, devicesnats.DeviceFactStreamName, automationsnats.DeviceFactConsumerName)
	if err != nil {
		t.Fatalf("get automation device fact consumer: %v", err)
	}
	return consumer
}

func deviceFactConsumerInfo(
	ctx context.Context,
	t *testing.T,
	consumer jetstream.Consumer,
) *jetstream.ConsumerInfo {
	t.Helper()
	info, err := consumer.Info(ctx)
	if err != nil {
		t.Fatalf("read automation device fact consumer: %v", err)
	}
	return info
}

// waitForDeviceFactConsumerIdle blocks until the durable consumer has delivered
// and acknowledged everything currently stored. An acked floor with no pending
// or unacknowledged message is the broker's own verdict that no recovery work
// remains, so a later restart has nothing to replay.
func waitForDeviceFactConsumerIdle(
	ctx context.Context,
	t *testing.T,
	consumer jetstream.Consumer,
) {
	t.Helper()
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		info, err := consumer.Info(ctx)
		if err != nil {
			return false, err
		}
		return info.NumPending == 0 && info.NumAckPending == 0, nil
	})
}

// waitForDeviceFactConsumerPending blocks until exactly the supplied number of
// facts wait undelivered on the durable consumer, which proves the facts were
// stored across the crash window and are recoverable rather than lost.
func waitForDeviceFactConsumerPending(
	ctx context.Context,
	t *testing.T,
	consumer jetstream.Consumer,
	pending uint64,
) {
	t.Helper()
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		info, err := consumer.Info(ctx)
		if err != nil {
			return false, err
		}
		return info.NumPending == pending && info.NumAckPending == 0, nil
	})
}

// assertSameConsumerDrained proves recovery used the same durable consumer: its
// server-side creation time is unchanged, its delivery policy is still the
// first-creation DeliverNew policy, and its acknowledgement floor has advanced
// past both recovered facts with nothing left in flight.
func assertSameConsumerDrained(
	ctx context.Context,
	t *testing.T,
	consumer jetstream.Consumer,
	before *jetstream.ConsumerInfo,
) {
	t.Helper()
	after := deviceFactConsumerInfo(ctx, t, consumer)
	if !after.Created.Equal(before.Created) {
		t.Fatalf("durable consumer was recreated: created %s, want %s", after.Created, before.Created)
	}
	if after.Config.DeliverPolicy != jetstream.DeliverNewPolicy {
		t.Fatalf("consumer DeliverPolicy = %v, want DeliverNew", after.Config.DeliverPolicy)
	}
	if after.NumPending != 0 || after.NumAckPending != 0 {
		t.Fatalf("consumer still holds work: %d pending, %d unacknowledged",
			after.NumPending, after.NumAckPending)
	}
	if after.AckFloor.Stream < 2 {
		t.Fatalf("consumer ack floor = %d, want at least the two recovered facts",
			after.AckFloor.Stream)
	}
}

// automationHistorySummary is the local newest-first history projection.
type automationHistorySummary struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	Fact   *struct {
		FactID string `json:"fact_id"`
	} `json:"fact"`
}

func readAutomationHistory(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID string,
) []automationHistorySummary {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/automations/"+automationID+"/history?limit=200", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var page struct {
		Items []automationHistorySummary `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page.Items
}

// waitForRecoveredOutcomes blocks until history explains both recovered facts:
// exactly one terminal Run and exactly one `stale_fact` Skip, with no other
// outcome. It fails if a third outcome or an unexplained entry appears.
func waitForRecoveredOutcomes(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID string,
) []automationHistorySummary {
	t.Helper()
	var entries []automationHistorySummary
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		entries = readAutomationHistory(ctx, t, httpAddress, automationID)
		runs, skips := 0, 0
		for _, entry := range entries {
			switch entry.Kind {
			case "run":
				if entry.Status != "running" {
					runs++
				}
			case "skip":
				if entry.Reason == string(automations.SkipStaleFact) {
					skips++
				}
			}
		}
		return len(entries) == 2 && runs == 1 && skips == 1, nil
	})
	return entries
}

func assertRecoveredRun(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, runID, powerEntityID string,
) {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/automations/"+automationID+"/history/"+runID, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("run entry status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var entry struct {
		Kind string `json:"kind"`
		Run  *struct {
			Source            string   `json:"source"`
			Status            string   `json:"status"`
			MatchedTriggerIDs []string `json:"matched_trigger_ids"`
			Fact              *struct {
				Family  string `json:"family"`
				Variant string `json:"variant"`
				FactID  string `json:"fact_id"`
			} `json:"fact"`
			Steps []struct {
				StepID            string  `json:"step_id"`
				Status            string  `json:"status"`
				VerifiedCommandID *string `json:"verified_command_id"`
			} `json:"steps"`
		} `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "run" || entry.Run == nil {
		t.Fatalf("history entry = %#v", entry)
	}
	run := entry.Run
	if run.Source != "device_fact" || run.Status != "succeeded" {
		t.Fatalf("recovered run source/status = %q/%q, want device_fact/succeeded",
			run.Source, run.Status)
	}
	if len(run.MatchedTriggerIDs) != 1 || run.MatchedTriggerIDs[0] != "activity" {
		t.Fatalf("recovered run matched trigger IDs = %v, want [activity]", run.MatchedTriggerIDs)
	}
	if run.Fact == nil || run.Fact.FactID != recoveryFreshFactID ||
		run.Fact.Family != "observation" || run.Fact.Variant != "applied" {
		t.Fatalf("recovered run fact = %#v, want the fresh observation fact", run.Fact)
	}
	if len(run.Steps) != 1 || run.Steps[0].StepID != "turn_off" ||
		run.Steps[0].Status != "satisfied" || run.Steps[0].VerifiedCommandID == nil {
		t.Fatalf("recovered run steps = %#v", run.Steps)
	}
	assertTerminalLinkedCommand(ctx, t, httpAddress, *run.Steps[0].VerifiedCommandID, powerEntityID, false)
}

// assertStaleSkip proves the old Fact was explained, not executed: its Skip
// carries the immutable matched Trigger and the stale reason, and it names the
// stale Fact identity rather than the fresh one.
func assertStaleSkip(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID string,
	entries []automationHistorySummary,
) {
	t.Helper()
	var skipID string
	for _, entry := range entries {
		if entry.Kind == "skip" {
			skipID = entry.ID
		}
	}
	if skipID == "" {
		t.Fatal("recovery recorded no Skip")
	}
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/automations/"+automationID+"/history/"+skipID, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("skip entry status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var entry struct {
		Kind string `json:"kind"`
		Skip *struct {
			Reason string `json:"reason"`
			Fact   struct {
				Family  string `json:"family"`
				Variant string `json:"variant"`
				FactID  string `json:"fact_id"`
			} `json:"fact"`
			MatchedTriggers []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"matched_triggers"`
		} `json:"skip"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "skip" || entry.Skip == nil {
		t.Fatalf("history entry = %#v", entry)
	}
	skip := entry.Skip
	if skip.Reason != string(automations.SkipStaleFact) {
		t.Fatalf("skip reason = %q, want %q", skip.Reason, automations.SkipStaleFact)
	}
	if skip.Fact.FactID != recoveryStaleFactID || skip.Fact.Family != "observation" ||
		skip.Fact.Variant != "applied" {
		t.Fatalf("stale skip fact = %#v, want the stale observation fact", skip.Fact)
	}
	if len(skip.MatchedTriggers) != 1 || skip.MatchedTriggers[0].ID != "activity" ||
		skip.MatchedTriggers[0].Kind != "observation" {
		t.Fatalf("stale skip matched triggers = %#v", skip.MatchedTriggers)
	}
}

// assertHistoryUnchanged proves a restart with nothing pending invented no
// outcome: the retained history is the exact set of IDs and kinds observed
// before the restart.
func assertHistoryUnchanged(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID string,
	before []automationHistorySummary,
) {
	t.Helper()
	expected := make(map[string]string, len(before))
	for _, entry := range before {
		expected[entry.ID] = entry.Kind
	}
	after := readAutomationHistory(ctx, t, httpAddress, automationID)
	if len(after) != len(expected) {
		t.Fatalf("history after restart = %d entries, want %d", len(after), len(expected))
	}
	for _, entry := range after {
		kind, ok := expected[entry.ID]
		if !ok {
			t.Fatalf("restart invented history entry %s (%s)", entry.ID, entry.Kind)
		}
		if entry.Kind != kind {
			t.Fatalf("history entry %s kind = %q, want %q", entry.ID, entry.Kind, kind)
		}
	}
}
