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
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// entityEventWire is the test's own view of the entity-event payload, kept
// local so the assembly test never depends on the transport DTO.
type entityEventWire struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

// This test protects the Core-offline vertical slice from the Devices spec and
// fails if broker-acknowledged reports do not survive a Core restart exactly
// once, if heartbeat retries end the Session, or if the event source Entity
// reads synthetic State.
//
//nolint:gocognit,gocyclo,cyclop // The offline recovery sequence is clearer as one causal test.
func TestCoreOfflineEntityEventRecoveryVerticalSlice(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sessionLogger, sessionRecords := withRecording(slog.LevelDebug)
	client := newNonPoolingHTTPClient(t)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	server := startCoreNATSServer(t)

	firstAddress := unusedLoopbackAddress(t)
	firstContext, stopFirstCore := context.WithCancel(ctx)
	defer stopFirstCore()
	firstErrors := make(chan error, 1)
	go func() {
		firstErrors <- Run(firstContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: firstAddress,
			NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHTTPStatus(ctx, t, client, firstAddress, "/healthz", firstErrors)

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(), Logger: sessionLogger,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil && !errors.Is(closeErr, adapter.ErrRuntimeFenced) {
			t.Errorf("close simulator Session: %v", closeErr)
		}
	})
	scriptedRuntime, err := scripted.New(session, []scripted.DeviceSpec{
		sliceEntityEventsDevice("simulated-light", "Simulated light"),
	})
	if err != nil {
		t.Fatal(err)
	}
	registrations := scriptedRuntime.Registrations()
	bindings := make([]adapter.Binding, 0, len(registrations))
	for _, registration := range registrations {
		binding, registerErr := session.Register(ctx, registration)
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		bindings = append(bindings, binding)
	}
	if attachErr := scriptedRuntime.Attach(bindings); attachErr != nil {
		t.Fatal(attachErr)
	}
	powerEntityID := bindingEntityID(t, bindings[0], "power")
	eventsEntityID := bindingEntityID(t, bindings[0], "events")
	if powerEntityID == eventsEntityID {
		t.Fatalf("registration reused one Entity ID for power and events: %s", powerEntityID)
	}
	if err = scriptedRuntime.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCoreHTTPStatus(ctx, t, client, firstAddress, "/readyz", firstErrors)

	stopFirstCore()
	select {
	case runErr := <-firstErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop")
	}

	// Core is offline: a connected, registered Session stores reports in the
	// durable stream and returns JetStream acknowledgements only.
	expected := map[string]string{}
	for _, name := range []string{
		sliceEntityEventSinglePress,
		sliceEntityEventDoublePress,
		sliceEntityEventSinglePress,
	} {
		result, publishErr := scriptedRuntime.PublishNow(
			ctx, string(eventsEntityID), json.RawMessage(fmt.Sprintf(`{"name":%q}`, name)),
		)
		if publishErr != nil {
			t.Fatalf("offline PublishNow(%q): %v", name, publishErr)
		}
		eventID := result.EventID
		if eventID == "" {
			t.Fatalf("offline PublishNow(%q) returned no identity", name)
		}
		if _, duplicate := expected[string(eventID)]; duplicate {
			t.Fatalf("offline reports reused event ID %s", eventID)
		}
		expected[string(eventID)] = name
	}
	// Heartbeat retries must not end the Session: wait for more than one
	// missed heartbeat, then prove the Session still publishes.
	waitForMatrixCondition(t, 25*time.Second, func() (bool, error) {
		retries := 0
		for _, record := range recordsWithEvent(sessionRecords.snapshot(), "dependency.retrying") {
			if operation, ok := recordAttr(record, "operation"); ok && operation.String() == "heartbeat" {
				retries++
			}
		}
		return retries >= 2, nil
	})
	lateResult, err := scriptedRuntime.PublishNow(
		ctx, string(eventsEntityID),
		json.RawMessage(fmt.Sprintf(`{"name":%q}`, sliceEntityEventSinglePress)),
	)
	if err != nil {
		t.Fatalf("PublishNow after missed heartbeats: %v", err)
	}
	expected[string(lateResult.EventID)] = sliceEntityEventSinglePress
	if stopped := recordsWithEvent(sessionRecords.snapshot(), "adapter.heartbeat_stopped"); len(stopped) != 0 {
		t.Fatalf("missed heartbeats stopped the Session: %#v", stopped)
	}

	secondAddress := unusedLoopbackAddress(t)
	secondContext, stopSecondCore := context.WithCancel(ctx)
	defer stopSecondCore()
	secondErrors := make(chan error, 1)
	go func() {
		secondErrors <- Run(secondContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: secondAddress,
			NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHTTPStatus(ctx, t, client, secondAddress, "/healthz", secondErrors)
	waitForCoreHTTPStatus(ctx, t, client, secondAddress, "/readyz", secondErrors)

	// Every broker-acknowledged report is recorded exactly once after restart.
	var collection devicesapi.EntityEventCollectionBody
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		collection = getEntityEvents(ctx, t, client, secondAddress, string(eventsEntityID))
		return len(collection.Items) == len(expected), nil
	})
	recorded := map[string]devicesapi.EntityEventBody{}
	for _, item := range collection.Items {
		if _, duplicate := recorded[item.EventID]; duplicate {
			t.Fatalf("event %s was recorded more than once", item.EventID)
		}
		recorded[item.EventID] = item
	}
	for eventID, wantName := range expected {
		item, ok := recorded[eventID]
		if !ok {
			t.Fatalf("event %s was not recorded", eventID)
		}
		if item.Name != wantName || item.EntityID != string(eventsEntityID) ||
			item.Disposition != string(devices.EntityEventDispositionAccepted) || item.RejectionCode != nil {
			t.Fatalf("recorded event = %#v", item)
		}
	}
	// Events are not State: the event source Entity still reads state null.
	assertEventSourceStateIsNull(ctx, t, client, secondAddress, string(eventsEntityID))

	stopSecondCore()
	select {
	case runErr := <-secondErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop after restart")
	}
}

// This test protects assembly consumer lifecycle and restart durability and
// fails if a drained consumer stays active, abandons an already dispatched
// report, or loses input it never read instead of leaving it for the next Core
// process. It also pins the prior shutdown defect: both durable consumers used
// to inherit the dependency context, which Run cancels before transports drain,
// so a report that had already entered RecordEntityEvent returned
// [context.Canceled] instead of committing.
//
//nolint:gocognit // The drain and restart sequence is clearer as one causal test.
func TestEntityEventDrainCommitsInFlightReportAndLeavesUnreadInputForNextProcess(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger := slog.New(slog.DiscardHandler)
	server := startCoreNATSServer(t)
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionEntityEventResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	stored := devicessqlite.DeviceStores(devicessqlite.NewDeviceRepository(database, catalog))
	service := devices.NewService(stored, nil, catalog, devices.Dependencies{})
	runtimeID := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if claimErr := service.ClaimAdapterRuntime(ctx, devices.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: runtimeID,
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := service.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: "drain-light",
		Device:     devices.DeviceDescriptor{Name: "Drain light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{{
			Key: "events", ExternalID: "drain.events", Name: "Events",
			TypeID:  devices.EntityTypeEnumeventV1,
			Support: devices.EntitySupport(`{"state":{},"operations":{},"events":{"names":["single_press"]}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID := binding.Entities[0].EntityID

	// The assembly under test is the production one: dependencies get their own
	// context and the durable consumers get a detached lifecycle context whose
	// cancel happens only after they stop.
	dependencyContext, cancelDependencies := context.WithCancel(context.WithoutCancel(ctx))
	consumers := newCoreConsumers(ctx)
	t.Cleanup(consumers.close)
	inFlight := newGatedCallback()
	recorder := newGatedEntityEventRecorder(service, inFlight)
	if startErr := consumers.startEntityEvents(durable, validator, recorder, logger); startErr != nil {
		t.Fatal(startErr)
	}
	committedEventID := publishEntityEventForTest(
		ctx, t, js, validator, "simulator", runtimeID, entityID,
		"single_press", "evt_01890f47-7a6b-7c4d-8e9f-0123456789b1",
	)
	waitForGatedCallback(t, inFlight, "in-flight report did not reach persistence")
	// Shutdown cancels its dependency contexts while this report is gated in
	// flight. The callback context must stay live, because cancelDependencies
	// runs before transports drain in production.
	cancelDependencies()
	if dependencyContext.Err() == nil {
		t.Fatal("dependency context was not canceled")
	}
	if callbackErr := recorder.callbackContext().Err(); callbackErr != nil {
		t.Fatalf("dependency cancellation reached the in-flight Entity Event callback: %v", callbackErr)
	}
	// Drain or stop can be called while a report is in flight. The already
	// dispatched report still commits, so shutdown never leaves a partial row;
	// the consumer stops before its database and NATS dependencies.
	drained := make(chan struct{})
	go func() {
		consumers.drain()
		close(drained)
	}()
	select {
	case <-drained:
		// Drain does not wait for the in-flight callback in this client.
	case <-time.After(200 * time.Millisecond):
		// Drain is waiting for the in-flight callback to return.
	}
	close(inFlight.release)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not finish after the in-flight report committed")
	}
	entityEvents := consumers.entityEvents
	select {
	case <-entityEvents.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("drained consumer did not close")
	}
	if entityEvents.Active() {
		t.Fatal("drained consumer still reports active")
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return countEntityEventRows(t, database, entityID) == 1, nil
	})
	// A committed row is not enough on its own: the report is acknowledged only
	// after that commit, so an empty ack backlog is the evidence that the
	// callback context outlived dependency cancellation.
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		info, infoErr := durable.Info(ctx)
		if infoErr != nil {
			return false, infoErr
		}
		return info.NumAckPending == 0, nil
	})
	// The consumer lifecycle context is canceled last, after every started
	// consumer has stopped, and that cancel still reaches callbacks.
	consumers.close()
	if cancelErr := recorder.callbackContext().Err(); !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("consumer context after close = %v, want context.Canceled", cancelErr)
	}

	// Input the drained consumer never read stays in the stream and is
	// recorded exactly once by the next Core process.
	unreadEventID := publishEntityEventForTest(
		ctx, t, js, validator, "simulator", runtimeID, entityID,
		"single_press", "evt_01890f47-7a6b-7c4d-8e9f-0123456789b2",
	)
	if rows := countEntityEventRows(t, database, entityID); rows != 1 {
		t.Fatalf("entity event rows before the next process = %d", rows)
	}
	nextDurable, err := devicesnats.ProvisionEntityEventResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	nextProcess, err := devicesnats.StartEntityEventConsumer(ctx, nextDurable, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nextProcess.Stop)
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return countEntityEventRows(t, database, entityID) == 2, nil
	})
	history, err := service.ListEntityEvents(ctx, devices.ListEntityEventsParams{
		EntityID: entityID, Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[devices.EntityEventID]int{}
	for _, entry := range history.Items {
		seen[entry.EventID]++
	}
	if seen[committedEventID] != 1 || seen[unreadEventID] != 1 || len(history.Items) != 2 {
		t.Fatalf("retained history = %#v", history.Items)
	}
}

// This test protects startup resource validation and fails if an incompatible
// existing Entity Event stream is accepted instead of failing the assembly.
func TestCoreStartupRejectsIncompatibleEntityEventResources(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	server := startCoreNATSServer(t)
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name:     devicesnats.EntityEventStreamName,
		Subjects: []string{natswire.EntityEventWildcard()},
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, infoErr := stream.Info(ctx); infoErr != nil {
		t.Fatal(infoErr)
	}
	runErr := Run(ctx, Config{
		HouseholdTimezone: "UTC", HTTPAddr: unusedLoopbackAddress(t),
		NATSURL: server.ClientURL(), SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}, slog.New(slog.DiscardHandler))
	if runErr == nil {
		t.Fatal("Run accepted an incompatible Entity Event stream")
	}
	if stage := ErrorStage(runErr); stage != "provision_jetstream" {
		t.Fatalf("Run stage = %q, want provision_jetstream (err: %v)", stage, runErr)
	}
}

// startCoreNATSServer starts a JetStream-enabled NATS server for an assembly
// test that needs Core's durable Observation or Entity Event consumer.
func startCoreNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func bindingEntityID(t *testing.T, binding adapter.Binding, key string) devices.EntityID {
	t.Helper()
	for _, entity := range binding.Entities {
		if entity.Key == key {
			entityID, err := devices.ParseEntityID(entity.EntityID)
			if err != nil {
				t.Fatal(err)
			}
			return entityID
		}
	}
	t.Fatalf("registration response omitted Entity key %q", key)
	return ""
}

func publishEntityEventForTest(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	adapterID string,
	runtimeID devices.RuntimeID,
	entityID devices.EntityID,
	name, eventIDValue string,
) devices.EntityEventID {
	t.Helper()
	eventID, err := devices.ParseEntityEventID(eventIDValue)
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(validator, contractsv1.EntityEventSchemaID,
		natswire.Envelope[entityEventWire]{
			ID: string(eventID), Schema: contractsv1.EntityEventSchemaID,
			EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: string(correlationID),
			Data: entityEventWire{EntityID: string(entityID), Name: name},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.EntityEventSubject(adapterID, string(runtimeID), string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	message := &natsgo.Msg{Subject: subject, Data: payload, Header: make(natsgo.Header)}
	message.Header.Set(natsgo.MsgIdHdr, string(eventID))
	if _, publishErr := js.PublishMsg(ctx, message); publishErr != nil {
		t.Fatal(publishErr)
	}
	return eventID
}

func countEntityEventRows(t *testing.T, database *sql.DB, entityID devices.EntityID) int {
	t.Helper()
	var rows int
	if err := database.QueryRow(
		`SELECT count(*) FROM entity_events WHERE entity_id = ?`, string(entityID),
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// newNonPoolingHTTPClient returns a client whose requests never share a pooled
// connection and never reuse the process-wide default client. net/http can dial
// a connection while another request is served on a pooled one, leaving a
// socket that no request was ever sent on. Core cannot tell that socket apart
// from a slow client, so a test that keeps the default client's pooling could
// let that client-side artifact decide its shutdown evidence; the
// unfinished-connection case is covered deliberately by
// TestRunCancellationForceClosesUnfinishedClientConnection.
func newNonPoolingHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func getEntityEvents(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, entityID string,
) devicesapi.EntityEventCollectionBody {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+baseURL+"/v1/entities/"+entityID+"/events?limit=50", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return devicesapi.EntityEventCollectionBody{}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("event history status = %d, body = %s", response.StatusCode, body)
	}
	var collection devicesapi.EntityEventCollectionBody
	if decodeErr := json.Unmarshal(body, &collection); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	return collection
}

func assertEventSourceStateIsNull(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	baseURL, entityID string,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+baseURL+"/v1/entities/"+entityID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Type  string          `json:"type"`
		State json.RawMessage `json:"state"`
	}
	if decodeErr := json.Unmarshal(body, &view); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if view.Type != "hearth.enumevent/v1" || string(view.State) != "null" {
		t.Fatalf("event source Entity read = %s", body)
	}
}

func waitForCoreHTTPStatus(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	httpAddress, path string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+httpAddress+path, nil,
		)
		if requestErr != nil {
			return false, requestErr
		}
		response, responseErr := client.Do(request)
		if responseErr != nil {
			return false, nil
		}
		// Draining before closing releases the connection for the next poll
		// instead of leaving an unread body behind; a failed read only means the
		// next poll retries.
		_, _ = io.Copy(io.Discard, response.Body)
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	})
}
