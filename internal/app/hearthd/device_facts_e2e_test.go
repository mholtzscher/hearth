//go:build e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	contractenumeventv1 "github.com/mholtzscher/hearth/entitytypes/enumeventv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
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

// startDeviceFactsCore starts Core over one embedded JetStream server and
// returns the HTTP address and the process error channel.
func startDeviceFactsCore(
	ctx context.Context,
	t *testing.T,
	serverURL string,
	databasePath string,
) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	httpAddress, httpOptions := reserveLoopbackListener(t)
	runContext, stopCore := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runWithOptions(runContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: httpAddress,
			NATSURL: serverURL, SQLitePath: databasePath,
			Agent: requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler), httpOptions)
	}()
	return httpAddress, stopCore, runErrors
}

func stopDeviceFactsCore(t *testing.T, stopCore context.CancelFunc, runErrors <-chan error) {
	t.Helper()
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("hearthd run returned %v", runErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("hearthd did not stop")
	}
}

// waitForCoreReady waits until /readyz reports ready. Readiness is the
// assembly's own verdict that SQLite, the shared NATS connection, the fact
// stream, the relay, both durable resources and both consumers are live.
func waitForCoreReady(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+httpAddress+"/readyz", nil,
		)
		if requestErr != nil {
			return false, requestErr
		}
		response, responseErr := http.DefaultClient.Do(request)
		if responseErr != nil {
			return false, nil
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	})
}

// assertDeviceFactStreamSet pins Core's inbound evidence, Device Fact, and
// independently delivered Adapter health stream identities.
func assertDeviceFactStreamSet(ctx context.Context, t *testing.T, js jetstream.JetStream) {
	t.Helper()
	names := js.StreamNames(ctx)
	var streamNames []string
	for name := range names.Name() {
		streamNames = append(streamNames, name)
	}
	if nameErr := names.Err(); nameErr != nil {
		t.Fatal(nameErr)
	}
	expected := map[string]bool{
		adaptersnats.HealthStreamName:     true,
		devicesnats.ObservationStreamName: true,
		devicesnats.EntityEventStreamName: true,
		devicesnats.DeviceFactStreamName:  true,
	}
	if len(streamNames) != len(expected) {
		t.Fatalf("streams = %v, want exactly %v", streamNames, expected)
	}
	for _, name := range streamNames {
		if !expected[name] {
			t.Fatalf("unexpected stream %q", name)
		}
	}
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

// TestCorePublishesDeviceFactsForPreStartupBacklogAndRestart protects the
// backlog and restart contract: reports broker-acknowledged before Core started
// enter durable history and are then published with their originating trace,
// a report published while Core is down is published by the next Core process,
// and the durable outbox settles on each run.
func TestCorePublishesDeviceFactsForPreStartupBacklogAndRestart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	runtimeID, powerEntityID, eventEntityID := seedDeviceFactsRegistration(ctx, t, databasePath)

	server := startLifecycleNATSServer(t)
	validator := mustDeviceFactValidator(t)
	writer, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	js, err := jetstream.New(writer)
	if err != nil {
		t.Fatal(err)
	}
	// Only the inbound streams exist before Core starts, so the reports below are
	// broker-acknowledged backlog rather than anything Core has seen.
	if _, provisionErr := devicesnats.ProvisionObservationResources(ctx, js); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	if _, provisionErr := devicesnats.ProvisionEntityEventResources(ctx, js); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	subscriber := newDeviceFactSubscriber(t, server.ClientURL())
	backlogObservationID := publishBacklogObservation(
		ctx, t, js, validator, runtimeID, powerEntityID, "obs_01890f47-7a6b-7c4d-8e9f-0123456789b1", "true",
	)
	backlogEventID := publishBacklogEntityEvent(
		ctx, t, js, validator, runtimeID, eventEntityID, "evt_01890f47-7a6b-7c4d-8e9f-0123456789b1", "single_press",
	)

	httpAddress, stopCore, runErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), databasePath)
	defer stopCore()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	// Readiness proves the fact stream and relay are live before the backlog is
	// consumed, so a published fact cannot be a readiness artifact.
	waitForCoreReady(ctx, t, httpAddress, runErrors)

	backlogObservationFact := subscriber.waitForCausation(
		t, backlogObservationID, natswire.DeviceFactFamilyObservation,
	)
	backlogEventFact := subscriber.waitForCausation(
		t, backlogEventID, natswire.DeviceFactFamilyEntityEvent,
	)
	for name, fact := range map[string]deviceFactRecord{
		"backlog observation":  backlogObservationFact,
		"backlog entity event": backlogEventFact,
	} {
		if fact.traceparent != factsTraceparent || fact.tracestate != factsTracestate {
			t.Fatalf("%s fact trace = %q / %q, want %q / %q",
				name, fact.traceparent, fact.tracestate, factsTraceparent, factsTracestate)
		}
	}
	waitForDeviceFactOutboxEmpty(ctx, t, databasePath)
	// Both backlog reports entered durable Core history, and their facts were
	// published only after that record committed.
	backlogDatabase, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backlogDatabase.Close() }()
	var recordedObservations, recordedEvents int
	if queryErr := backlogDatabase.QueryRowContext(
		ctx, "SELECT count(*) FROM observations WHERE observation_id = ?", backlogObservationID,
	).Scan(&recordedObservations); queryErr != nil {
		t.Fatal(queryErr)
	}
	if queryErr := backlogDatabase.QueryRowContext(
		ctx, "SELECT count(*) FROM entity_events WHERE event_id = ?", backlogEventID,
	).Scan(&recordedEvents); queryErr != nil {
		t.Fatal(queryErr)
	}
	if recordedObservations != 1 || recordedEvents != 1 {
		t.Fatalf("backlog durable history = %d observations, %d entity events, want 1 and 1",
			recordedObservations, recordedEvents)
	}
	// Inbound evidence, Device Facts, and independent Adapter health streams exist.
	assertDeviceFactStreamSet(ctx, t, js)

	// Restart: a report acknowledged while Core is down is recorded by the next
	// Core process, which re-reads the durable stream from its own ack floor and
	// publishes the fact for the newly recorded evidence.
	stopDeviceFactsCore(t, stopCore, runErrors)
	restartObservationID := publishBacklogObservation(
		ctx, t, js, validator, runtimeID, powerEntityID, "obs_01890f47-7a6b-7c4d-8e9f-0123456789b2", "false",
	)
	restartAddress, stopRestartedCore, restartedErrors := startDeviceFactsCore(
		ctx, t, server.ClientURL(), databasePath,
	)
	waitForCoreHealthz(ctx, t, restartAddress, restartedErrors)
	waitForCoreReady(ctx, t, restartAddress, restartedErrors)
	restartFact := subscriber.waitForCausation(
		t, restartObservationID, natswire.DeviceFactFamilyObservation,
	)
	if restartFact.traceparent != factsTraceparent || restartFact.tracestate != factsTracestate {
		t.Fatalf("restarted fact trace = %q / %q", restartFact.traceparent, restartFact.tracestate)
	}
	waitForDeviceFactOutboxEmpty(ctx, t, databasePath)
	stopDeviceFactsCore(t, stopRestartedCore, restartedErrors)
	// The restarted run provisioned no duplicate stream set.
	assertDeviceFactStreamSet(ctx, t, js)
}

func mustDeviceFactValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
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

// seedDeviceFactsRegistration registers one power Entity and one event-source
// Entity, returning the claimed Runtime and both canonical Entity IDs.
func seedDeviceFactsRegistration(
	ctx context.Context,
	t *testing.T,
	databasePath string,
) (adapters.RuntimeID, devices.EntityID, devices.EntityID) {
	t.Helper()
	database := dbtest.OpenMigrated(t, databasePath)
	defer func() { _ = database.Close() }()
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		testDeviceStores(devicessqlite.NewDeviceRepository(database, catalog), database), nil, catalog,
		devices.Dependencies{},
	)
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if claimErr := adapterServiceForTest(database).ClaimAdapterRuntime(ctx, adapters.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: runtimeID,
		SoftwareName: "hearth-facts-test", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := service.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: "facts-light",
		Device:     devices.DeviceDescriptor{Name: "Facts light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key: "power", ExternalID: "facts.power", Name: "Power",
				TypeID:  devices.EntityTypePowerV1,
				Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
			{
				Key: "events", ExternalID: "facts.events", Name: "Events",
				TypeID: devices.EntityTypeEnumeventV1,
				Support: devices.EntitySupport(
					`{"state":{},"operations":{},"events":{"names":["single_press"]}}`,
				),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID, eventEntityID := binding.Entities[0].EntityID, binding.Entities[1].EntityID
	return runtimeID, powerEntityID, eventEntityID
}

// publishBacklogObservation stores one schema-valid Observation envelope carrying
// the fixed trace context in the inbound observation stream before or after Core
// starts, exactly as a traced SDK would.
func publishBacklogObservation(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID adapters.RuntimeID,
	entityID devices.EntityID,
	observationID string,
	value string,
) string {
	t.Helper()
	subject, err := natswire.ObservationSubject("simulator", string(runtimeID), string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	type wireObservation struct {
		EntityID          string          `json:"entity_id"`
		Value             json.RawMessage `json:"value"`
		AdapterReceivedAt string          `json:"adapter_received_at"`
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(validator, contractsv1.ObservationSchemaID,
		natswire.Envelope[wireObservation]{
			ID: observationID, Schema: contractsv1.ObservationSchemaID,
			EmittedAt: now.Format(time.RFC3339Nano), CorrelationID: string(correlationID),
			Data: wireObservation{
				EntityID: string(entityID), Value: json.RawMessage(value),
				AdapterReceivedAt: now.Format(time.RFC3339Nano),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{observationID}}
	headers.Set("traceparent", factsTraceparent)
	headers.Set("tracestate", factsTracestate)
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject,
		Header:  headers,
		Data:    payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	return observationID
}

// publishBacklogEntityEvent stores one schema-valid Entity Event envelope
// carrying the fixed trace context in the inbound entity event stream.
func publishBacklogEntityEvent(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID adapters.RuntimeID,
	entityID devices.EntityID,
	eventID string,
	name string,
) string {
	t.Helper()
	subject, err := natswire.EntityEventSubject("simulator", string(runtimeID), string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	type wireEntityEvent struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name"`
	}
	payload, err := natswire.Encode(validator, contractsv1.EntityEventSchemaID,
		natswire.Envelope[wireEntityEvent]{
			ID: eventID, Schema: contractsv1.EntityEventSchemaID,
			EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: string(correlationID),
			Data: wireEntityEvent{EntityID: string(entityID), Name: name},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{eventID}}
	headers.Set("traceparent", factsTraceparent)
	headers.Set("tracestate", factsTracestate)
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject,
		Header:  headers,
		Data:    payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	return eventID
}
