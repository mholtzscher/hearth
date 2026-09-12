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

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	contractenumeventv1 "github.com/mholtzscher/hearth/entitytypes/enumeventv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkadapterenumeventv1 "github.com/mholtzscher/hearth/sdk/adapter/enumeventv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

// deviceFactEnvelope is the generic Device Fact envelope these tests decode.
type deviceFactEnvelope struct {
	ID            string          `json:"id"`
	Schema        string          `json:"schema"`
	EmittedAt     string          `json:"emitted_at"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id"`
	Data          json.RawMessage `json:"data"`
}

// deviceFactRecord is one received, schema-validated Device Fact.
type deviceFactRecord struct {
	envelope deviceFactEnvelope
	route    natswire.DeviceFactRoute
	data     map[string]json.RawMessage
}

// deviceFactSubscriber is a plain external NATS subscriber: no SDK, no
// JetStream and no Core process coupling.
type deviceFactSubscriber struct {
	connection *natsgo.Conn
	messages   chan *natsgo.Msg
	validator  *contractsv1.Validator
}

func newDeviceFactSubscriber(t *testing.T, url string, options ...natsgo.Option) *deviceFactSubscriber {
	t.Helper()
	connection, err := natsgo.Connect(url, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	messages := make(chan *natsgo.Msg, 256)
	if _, subscribeErr := connection.Subscribe(natswire.DeviceFactWildcard(), func(message *natsgo.Msg) {
		messages <- message
	}); subscribeErr != nil {
		t.Fatal(subscribeErr)
	}
	if flushErr := connection.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return &deviceFactSubscriber{connection: connection, messages: messages, validator: validator}
}

func (subscriber *deviceFactSubscriber) next(t *testing.T) deviceFactRecord {
	t.Helper()
	select {
	case message := <-subscriber.messages:
		return subscriber.decode(t, message)
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for a Device Fact")
		return deviceFactRecord{}
	}
}

// assertNone fails when any fact arrives inside a short bounded window, so a
// forbidden publication is caught without relying on a fixed sleep.
func (subscriber *deviceFactSubscriber) assertNone(t *testing.T) {
	t.Helper()
	select {
	case message := <-subscriber.messages:
		t.Fatalf("unexpected Device Fact on %q: %s", message.Subject, message.Data)
	case <-time.After(300 * time.Millisecond):
	}
}

func (subscriber *deviceFactSubscriber) decode(t *testing.T, message *natsgo.Msg) deviceFactRecord {
	t.Helper()
	route, err := natswire.ParseDeviceFactSubject(message.Subject)
	if err != nil {
		t.Fatalf("parse Device Fact subject: %v", err)
	}
	schemaID := factSchemaIDForFamily(t, route.Family)
	if validateErr := subscriber.validator.Validate(schemaID, message.Data); validateErr != nil {
		t.Fatalf("Device Fact payload is not schema-valid: %v", validateErr)
	}
	var envelope deviceFactEnvelope
	if unmarshalErr := json.Unmarshal(message.Data, &envelope); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if envelope.Schema != schemaID {
		t.Fatalf("fact schema = %q, want %q", envelope.Schema, schemaID)
	}
	if _, idErr := devices.ParseDeviceFactID(envelope.ID); idErr != nil {
		t.Fatalf("fact ID %q is not canonical: %v", envelope.ID, idErr)
	}
	if message.Header.Get(natsgo.MsgIdHdr) != "" {
		t.Fatal("Device Fact carried a Nats-Msg-Id header")
	}
	var data map[string]json.RawMessage
	if dataErr := json.Unmarshal(envelope.Data, &data); dataErr != nil {
		t.Fatal(dataErr)
	}
	var payloadEntity string
	if entityErr := json.Unmarshal(data["entity_id"], &payloadEntity); entityErr != nil {
		t.Fatal(entityErr)
	}
	if payloadEntity != route.EntityID {
		t.Fatalf("payload Entity %q disagrees with subject Entity %q", payloadEntity, route.EntityID)
	}
	subscriber.assertVariantAgrees(t, route, data)
	return deviceFactRecord{envelope: envelope, route: route, data: data}
}

// assertVariantAgrees enforces the subject/payload agreement a subscriber must
// also check: the family-specific payload field must repeat the subject variant.
func (subscriber *deviceFactSubscriber) assertVariantAgrees(
	t *testing.T,
	route natswire.DeviceFactRoute,
	data map[string]json.RawMessage,
) {
	t.Helper()
	field := ""
	switch route.Family {
	case natswire.DeviceFactFamilyObservation:
		field = "disposition"
	case natswire.DeviceFactFamilyEntityEvent:
		field = "name"
	}
	var value string
	if err := json.Unmarshal(data[field], &value); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	if value != route.Variant {
		t.Fatalf("payload %s %q disagrees with subject variant %q", field, value, route.Variant)
	}
}

func factSchemaIDForFamily(t *testing.T, family natswire.DeviceFactFamily) string {
	t.Helper()
	switch family {
	case natswire.DeviceFactFamilyObservation:
		return contractsv1.ObservationFactSchemaID
	case natswire.DeviceFactFamilyEntityEvent:
		return contractsv1.EntityEventFactSchemaID
	}
	t.Fatalf("unknown Device Fact family %q", family)
	return ""
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
	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: httpAddress,
			NATSURL: serverURL, SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
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
// assembly's own verdict that SQLite, both NATS connections, both durable
// resources, both consumers and the Device Fact dispatcher are live.
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

// TestCorePublishesDeviceFactsForSDKAndHTTPActivity is the external vertical
// slice: a real SDK Observation, a real SDK Entity Event and the Observation
// published by a real SDK Command handler each produce schema-valid facts
// observable by a plain NATS subscriber while the authoritative SQLite records
// agree. Commands are no longer evidence, so no Command fact exists.
//
//nolint:gocognit,gocyclo,cyclop // The end-to-end fact slice is clearer as one integration test.
func TestCorePublishesDeviceFactsForSDKAndHTTPActivity(t *testing.T) {
	t.Parallel()
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

	observationID, err := session.PublishObservation(ctx, adapter.Observation{
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
	entityEventID, err := session.PublishEntityEvent(ctx, entityEvent)
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

	// Collect facts until the Observation driven by the Command outcome arrives.
	var facts []deviceFactRecord
	appliedIndex := -1
	entityEventIndex := -1
	outcomeIndex := -1
	for range 16 {
		record := subscriber.next(t)
		facts = append(facts, record)
		index := len(facts) - 1
		switch {
		case appliedIndex < 0 && record.route.Family == natswire.DeviceFactFamilyObservation &&
			record.envelope.CausationID == string(observationID):
			appliedIndex = index
		case entityEventIndex < 0 && record.route.Family == natswire.DeviceFactFamilyEntityEvent:
			entityEventIndex = index
		case outcomeIndex < 0 && record.route.Family == natswire.DeviceFactFamilyObservation &&
			record.envelope.CausationID == outcomeObservationID:
			outcomeIndex = index
		}
		if appliedIndex >= 0 && entityEventIndex >= 0 && outcomeIndex >= 0 {
			break
		}
	}
	if appliedIndex < 0 {
		t.Fatalf("no Observation fact for %q: %#v", observationID, facts)
	}
	if entityEventIndex < 0 {
		t.Fatalf("no Entity Event fact was published: %#v", facts)
	}
	if outcomeIndex < 0 {
		t.Fatalf("no Observation fact for the Command outcome %q: %#v", outcomeObservationID, facts)
	}
	if facts[entityEventIndex].envelope.CausationID != string(entityEventID) {
		t.Fatalf(
			"Entity Event fact causation = %q, want %q",
			facts[entityEventIndex].envelope.CausationID, entityEventID,
		)
	}

	// Authoritative agreement: SQLite holds the satisfied Command and the
	// committed State the facts reported.
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

	stopServing()
	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("serve commands: %v", serveErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the SDK command server did not stop")
	}
	subscriber.assertNone(t)
	_ = session.Close()
	stopDeviceFactsCore(t, stopCore, runErrors)
	// Only the external subscriber remains connected: shutdown drained and
	// closed both Core connections, including the dedicated fact connection.
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return server.NumClients() == 1, nil
	})
}

// TestCoreDoesNotPublishFactsForPreStartupBacklog protects the no-catch-up
// contract: reports broker-acknowledged before Core started enter durable
// history but emit no Device Fact, a later live report emits exactly one, and no
// fact stream or replay resource exists.
//
//nolint:gocognit // The backlog lifecycle is clearer as one integration test.
func TestCoreDoesNotPublishFactsForPreStartupBacklog(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	runtimeID, powerEntityID, eventEntityID := seedDeviceFactsRegistration(ctx, t, databasePath)

	server := startLifecycleNATSServer(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	js, err := jetstream.New(writer)
	if err != nil {
		t.Fatal(err)
	}
	if _, provisionErr := devicesnats.ProvisionObservationResources(ctx, js); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	if _, provisionErr := devicesnats.ProvisionEntityEventResources(ctx, js); provisionErr != nil {
		t.Fatal(provisionErr)
	}
	backlogObservationID := publishBacklogObservation(
		ctx, t, js, validator, runtimeID, powerEntityID, "obs_01890f47-7a6b-7c4d-8e9f-0123456789b1", "true",
	)
	backlogEventID := publishBacklogEntityEvent(
		ctx, t, js, validator, runtimeID, eventEntityID, "evt_01890f47-7a6b-7c4d-8e9f-0123456789b1", "single_press",
	)

	subscriber := newDeviceFactSubscriber(t, server.ClientURL())
	httpAddress, stopCore, runErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), databasePath)
	defer stopCore()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)

	// Both backlog reports are durably recorded before anything is asserted
	// absent, so a slower replay can never look like a missing fact.
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		var observations, events int
		if queryErr := database.QueryRowContext(
			ctx, "SELECT count(*) FROM observations WHERE observation_id = ? AND disposition = 'applied'",
			backlogObservationID,
		).Scan(&observations); queryErr != nil {
			return false, queryErr
		}
		if queryErr := database.QueryRowContext(
			ctx, "SELECT count(*) FROM entity_events WHERE event_id = ? AND disposition = 'accepted'",
			backlogEventID,
		).Scan(&events); queryErr != nil {
			return false, queryErr
		}
		return observations == 1 && events == 1, nil
	})
	subscriber.assertNone(t)

	// A report stored after startup is inside the live window and emits one
	// fact, proving suppression is freshness, not a broken fact path.
	liveObservationID := publishBacklogObservation(
		ctx, t, js, validator, runtimeID, powerEntityID, "obs_01890f47-7a6b-7c4d-8e9f-0123456789b2", "false",
	)
	live := subscriber.next(t)
	if live.route.Family != natswire.DeviceFactFamilyObservation ||
		live.route.Variant != natswire.ObservationFactApplied {
		t.Fatalf("live fact route = %#v", live.route)
	}
	if live.envelope.CausationID != liveObservationID {
		t.Fatalf("live fact causation = %q, want %q", live.envelope.CausationID, liveObservationID)
	}
	if backlogObservationID == liveObservationID {
		t.Fatal("the live report reused the backlog Observation identity")
	}
	subscriber.assertNone(t)

	// No fact stream or replay resource was provisioned: only the two inbound
	// durable streams exist.
	names := js.StreamNames(ctx)
	var streamNames []string
	for name := range names.Name() {
		streamNames = append(streamNames, name)
	}
	if nameErr := names.Err(); nameErr != nil {
		t.Fatal(nameErr)
	}
	for _, name := range streamNames {
		if strings.Contains(name, "FACT") {
			t.Fatalf("Core provisioned a fact stream %q", name)
		}
	}
	expectedStreams := map[string]bool{
		devicesnats.ObservationStreamName: true,
		devicesnats.EntityEventStreamName: true,
	}
	if len(streamNames) != len(expectedStreams) {
		t.Fatalf("streams = %v, want exactly %v", streamNames, expectedStreams)
	}
	for _, name := range streamNames {
		if !expectedStreams[name] {
			t.Fatalf("unexpected stream %q", name)
		}
	}
	stopDeviceFactsCore(t, stopCore, runErrors)
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
) (devices.RuntimeID, devices.EntityID, devices.EntityID) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)), nil, catalog,
		devices.Dependencies{},
	)
	runtimeID := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if claimErr := service.ClaimAdapterRuntime(ctx, devices.ClaimAdapterRuntimeParams{
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

// publishBacklogObservation stores one schema-valid Observation envelope in the
// inbound observation stream before or after Core starts, exactly as the SDK
// would.
func publishBacklogObservation(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID devices.RuntimeID,
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
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject,
		Header:  natsgo.Header{natsgo.MsgIdHdr: []string{observationID}},
		Data:    payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	return observationID
}

// publishBacklogEntityEvent stores one schema-valid Entity Event envelope in the
// inbound entity event stream.
func publishBacklogEntityEvent(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID devices.RuntimeID,
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
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject,
		Header:  natsgo.Header{natsgo.MsgIdHdr: []string{eventID}},
		Data:    payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	return eventID
}
