//go:build e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	contractenumeventv1 "github.com/mholtzscher/hearth/entitytypes/enumeventv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkadapterenumeventv1 "github.com/mholtzscher/hearth/sdk/adapter/enumeventv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

// factsTraceContext returns a context carrying the fixed sampled span whose
// traceparent and tracestate the SDK injects into every report it publishes.
func factsTraceContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex(factsTraceIDHex)
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex(factsSpanIDHex)
	if err != nil {
		t.Fatal(err)
	}
	traceState, err := trace.ParseTraceState(factsTracestate)
	if err != nil {
		t.Fatal(err)
	}
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, TraceState: traceState,
	}))
}

// TestCorePublishesDurableDeviceFactsForSDKAndHTTPActivity is the external
// vertical slice: a real SDK Observation, a real SDK Entity Event and the
// Observation published by a real SDK Command handler each produce a
// schema-valid durable fact that a plain NATS subscriber sees live, with the
// stable fact identity as Nats-Msg-Id and the originating trace context
// restored. Commands are no longer evidence, so no Command fact exists, and the
// authoritative SQLite records agree with what the facts reported.
//
//nolint:gocognit,gocyclo,cyclop,paralleltest // Process-level integration tests share constrained CI resources.
func TestCorePublishesDurableDeviceFactsForSDKAndHTTPActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	subscriber := newDeviceFactSubscriber(t, server.ClientURL())
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	httpAddress, stopCore, runErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), databasePath)
	defer stopCore()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	waitForCoreReady(ctx, t, httpAddress, runErrors)

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-facts-test",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	powerSupport := contractpowerv1.Support{
		State:      contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}},
	}
	powerDescriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: "facts.power", Name: "Power",
	}, powerSupport)
	if err != nil {
		t.Fatal(err)
	}
	eventSupport := contractenumeventv1.Support{
		State:      contractenumeventv1.StateSupport{},
		Operations: contractenumeventv1.OperationSupport{},
		Events: contractenumeventv1.SupportEvents{
			Names: contractenumeventv1.SupportEventsNames{"single_press"},
		},
	}
	eventDescriptor, err := sdkadapterenumeventv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "events", ExternalID: "facts.events", Name: "Events",
	}, eventSupport)
	if err != nil {
		t.Fatal(err)
	}
	externalID := "facts-device"
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "facts-light",
		Device:     adapter.DeviceDescriptor{ExternalID: &externalID, Name: "Facts light", Kind: "light"},
		Entities:   []adapter.EntityDescriptor{powerDescriptor, eventDescriptor},
	})
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID := bindingEntityID(t, binding, "power")
	eventEntityID := bindingEntityID(t, binding, "events")
	powerEntity := string(powerEntityID)
	eventEntity := string(eventEntityID)
	now := time.Now().UTC()
	if healthErr := session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: now,
	}); healthErr != nil {
		t.Fatal(healthErr)
	}
	if availabilityErr := session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{
		{EntityID: powerEntity, Status: adapter.AvailabilityAvailable, SourceObservedAt: now},
		{EntityID: eventEntity, Status: adapter.AvailabilityAvailable, SourceObservedAt: now},
	}); availabilityErr != nil {
		t.Fatal(availabilityErr)
	}

	// Both reports are published inside one sampled trace, exactly as a traced
	// application would, so the fact must restore the originating trace.
	publicationContext := trace.ContextWithSpanContext(ctx, trace.SpanContextFromContext(factsTraceContext(t)))
	observationID, err := session.PublishObservation(publicationContext, adapter.Observation{
		EntityID: powerEntity, Value: json.RawMessage(`true`),
		AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	entityEvent, err := sdkadapterenumeventv1.NewEntityEvent(sdkadapterenumeventv1.EntityEventInput{
		EntityID: eventEntity, Support: eventSupport, Name: "single_press",
	})
	if err != nil {
		t.Fatal(err)
	}
	entityEventID, err := session.PublishEntityEvent(publicationContext, entityEvent)
	if err != nil {
		t.Fatal(err)
	}

	serveContext, stopServing := context.WithCancel(ctx)
	defer stopServing()
	// The handler reports the Observation identity it published, so the test can
	// wait for the fact of the Observation that satisfied the Command.
	outcomeObservations := make(chan string, 1)
	handler, err := sdkpowerv1.NewCommandHandler(powerEntity, powerSupport, sdkpowerv1.Handlers{
		Set: func(commandContext context.Context, command sdkpowerv1.SetCommand, responder adapter.Responder) error {
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			observation, observationErr := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
				EntityID: powerEntity, Support: powerSupport,
				State:             contractpowerv1.State(command.Parameters.Value),
				AdapterReceivedAt: time.Now().UTC(),
			})
			if observationErr != nil {
				return observationErr
			}
			publishedID, publishErr := evidence.PublishObservation(commandContext, observation)
			if publishErr != nil {
				return publishErr
			}
			outcomeObservations <- string(publishedID)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	baselineSubscriptions := server.NumSubscriptions()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- session.ServeCommands(serveContext, handler) }()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return server.NumSubscriptions() > baselineSubscriptions, nil
	})

	commandStatus, commandBody, err := postFactsCommand(ctx, httpAddress, powerEntity, true)
	if err != nil {
		t.Fatal(err)
	}
	if commandStatus != http.StatusOK {
		t.Fatalf("command response = %d: %s", commandStatus, commandBody)
	}
	var outcomeObservationID string
	select {
	case outcomeObservationID = <-outcomeObservations:
	case <-time.After(15 * time.Second):
		t.Fatal("the SDK command handler published no outcome Observation")
	}

	appliedFact := subscriber.waitForCausation(
		t, string(observationID), natswire.DeviceFactFamilyObservation,
	)
	eventFact := subscriber.waitForCausation(
		t, string(entityEventID), natswire.DeviceFactFamilyEntityEvent,
	)
	outcomeFact := subscriber.waitForCausation(
		t, outcomeObservationID, natswire.DeviceFactFamilyObservation,
	)
	if appliedFact.route.Variant != natswire.ObservationFactApplied {
		t.Fatalf("initial Observation fact variant = %q", appliedFact.route.Variant)
	}
	if eventFact.route.Variant != "single_press" {
		t.Fatalf("Entity Event fact variant = %q", eventFact.route.Variant)
	}

	// The relay restores the inbound trace context the report carried, so the
	// published fact continues the originating trace rather than starting one.
	for name, fact := range map[string]deviceFactRecord{
		"observation":  appliedFact,
		"entity event": eventFact,
	} {
		if fact.traceparent != factsTraceparent || fact.tracestate != factsTracestate {
			t.Fatalf("%s fact trace = %q / %q, want %q / %q",
				name, fact.traceparent, fact.tracestate, factsTraceparent, factsTracestate)
		}
	}
	// The Command outcome Observation carries no inbound trace because the HTTP
	// Command request started no span; the fact must not invent one.
	if outcomeFact.traceparent != "" || outcomeFact.tracestate != "" {
		t.Fatalf("outcome fact invented trace %q / %q", outcomeFact.traceparent, outcomeFact.tracestate)
	}

	// Durable agreement: SQLite holds the satisfied Command and the committed
	// State the facts reported.
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var status string
	var outcome sql.NullString
	if scanErr := database.QueryRowContext(ctx, `
		SELECT status, outcome_observation_id FROM commands
		ORDER BY requested_at DESC, id DESC LIMIT 1`,
	).Scan(&status, &outcome); scanErr != nil {
		t.Fatal(scanErr)
	}
	if status != string(devices.CommandStatusSatisfied) || !outcome.Valid || outcome.String != outcomeObservationID {
		t.Fatalf("authoritative command = %q, %#v", status, outcome)
	}
	var stateValue string
	if scanErr := database.QueryRowContext(
		ctx, "SELECT value_json FROM entity_states WHERE entity_id = ?", string(powerEntityID),
	).Scan(&stateValue); scanErr != nil {
		t.Fatal(scanErr)
	}
	if stateValue != "true" {
		t.Fatalf("authoritative state = %q, want true", stateValue)
	}
	// Every accepted report settled its durable outbox row once it was
	// acknowledged by the broker.
	waitForDeviceFactOutboxEmpty(ctx, t, databasePath)

	stopServing()
	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("serve commands: %v", serveErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the SDK command server did not stop")
	}
	// A satisfied Command produces no Device Fact: no Command fact exists.
	subscriber.assertNone(t)
	_ = session.Close()
	stopDeviceFactsCore(t, stopCore, runErrors)
	// Only the external subscriber remains connected: shutdown drained and
	// closed the one shared Core connection.
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return server.NumClients() == 1, nil
	})
}

func postFactsCommand(
	ctx context.Context,
	httpAddress string,
	entityID string,
	value bool,
) (int, []byte, error) {
	payload := fmt.Sprintf(`{"operation":"set","parameters":{"value":%t}}`, value)
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		"http://"+httpAddress+"/v1/entities/"+entityID+"/commands",
		strings.NewReader(payload),
	)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return response.StatusCode, body, err
}
