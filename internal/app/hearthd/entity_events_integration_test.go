//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// entityEventWire is the test's own view of the entity-event payload, kept
// local so the assembly test never depends on the transport DTO.
type entityEventWire struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
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
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	stored := testDeviceStores(devicessqlite.NewDeviceRepository(database, catalog), database)
	service := devices.NewService(stored, nil, catalog, devices.Dependencies{})
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if claimErr := adapterServiceForTest(database).ClaimAdapterRuntime(ctx, adapters.ClaimAdapterRuntimeParams{
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
		HouseholdTimezone: "UTC", HTTPAddr: "127.0.0.1:8080",
		NATSURL: server.ClientURL(), SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
		Agent: requiredAgentConfig(t),
	}, slog.New(slog.DiscardHandler))
	if runErr == nil {
		t.Fatal("Run accepted an incompatible Entity Event stream")
	}
	if stage := ErrorStage(runErr); stage != "provision_jetstream" {
		t.Fatalf("Run stage = %q, want provision_jetstream (err: %v)", stage, runErr)
	}
}

func publishEntityEventForTest(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	adapterID string,
	runtimeID adapters.RuntimeID,
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
