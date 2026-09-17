package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// gatedCallback holds one durable consumer callback in flight so a test can
// decide when it proceeds: entered closes when the callback arrives, release is
// closed by the test, and finished closes when the callback returned. Tests
// therefore never sleep to observe an in-flight callback or its outcome.
type gatedCallback struct {
	entered      chan struct{}
	release      chan struct{}
	finished     chan struct{}
	enteredOnce  sync.Once
	finishedOnce sync.Once
}

func newGatedCallback() *gatedCallback {
	return &gatedCallback{
		entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
	}
}

// hold reports that the callback arrived and blocks until the test closes
// release.
func (call *gatedCallback) hold() {
	call.enteredOnce.Do(func() { close(call.entered) })
	<-call.release
}

// finish reports that the callback returned, after committing or failing, so a
// test never has to poll for the callback's outcome.
func (call *gatedCallback) finish() {
	call.finishedOnce.Do(func() { close(call.finished) })
}

// waitForGatedCallback blocks until the callback reached its gate.
func waitForGatedCallback(t *testing.T, call *gatedCallback, message string) {
	t.Helper()
	select {
	case <-call.entered:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

// gatedEntityEventRecorder holds one Entity Event record in flight and retains
// the callback context, so a test can prove both that dependency teardown never
// cancels a callback this consumer owns and that a canceled callback context
// does stop the record from committing.
type gatedEntityEventRecorder struct {
	inner devicesnats.EntityEventRecorder
	call  *gatedCallback
	mutex sync.Mutex
	ctx   context.Context
}

func newGatedEntityEventRecorder(
	inner devicesnats.EntityEventRecorder,
	call *gatedCallback,
) *gatedEntityEventRecorder {
	return &gatedEntityEventRecorder{inner: inner, call: call}
}

func (recorder *gatedEntityEventRecorder) RecordEntityEvent(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	event devices.EntityEvent,
	receivedAt time.Time,
) (devices.EntityEventRecordResult, error) {
	recorder.mutex.Lock()
	recorder.ctx = ctx
	recorder.mutex.Unlock()
	recorder.call.hold()
	result, err := recorder.inner.RecordEntityEvent(ctx, adapterID, runtimeID, event, receivedAt)
	recorder.call.finish()
	return result, err
}

// callbackContext is the context the durable consumer handed this callback.
func (recorder *gatedEntityEventRecorder) callbackContext() context.Context {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.ctx
}

// gatedObservationProjector is the Observation counterpart of
// gatedEntityEventRecorder.
type gatedObservationProjector struct {
	inner devicesnats.ObservationProjector
	call  *gatedCallback
	mutex sync.Mutex
	ctx   context.Context
}

func newGatedObservationProjector(
	inner devicesnats.ObservationProjector,
	call *gatedCallback,
) *gatedObservationProjector {
	return &gatedObservationProjector{inner: inner, call: call}
}

func (projector *gatedObservationProjector) ProjectObservation(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	observation devices.Observation,
	observedAt time.Time,
) (devices.ProjectionResult, error) {
	projector.mutex.Lock()
	projector.ctx = ctx
	projector.mutex.Unlock()
	projector.call.hold()
	result, err := projector.inner.ProjectObservation(ctx, adapterID, runtimeID, observation, observedAt)
	projector.call.finish()
	return result, err
}

// callbackContext is the context the durable consumer handed this callback.
func (projector *gatedObservationProjector) callbackContext() context.Context {
	projector.mutex.Lock()
	defer projector.mutex.Unlock()
	return projector.ctx
}

// This test protects the durable Observation consumer's callback context and
// fails if dependency cancellation reaches an already-dispatched projection.
// Core shutdown cancels the dependency context that command workers, health, and
// maintenance share before transports drain, and both durable consumers used to
// inherit it, so a projection that had already entered ProjectObservation
// returned [context.Canceled] instead of committing. The row and the empty
// acknowledgement backlog are the evidence: a canceled callback context fails
// the commit, leaves the row absent, and leaves the message unacknowledged.
func TestObservationConsumerCallbackSurvivesDependencyCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	harness := startCoreConsumerHarness(
		ctx, t,
		"run_01890f47-7a6b-7c4d-8e9f-0123456789ac",
		"callback-light",
		powerEntity(),
	)
	durable, err := devicesnats.ProvisionObservationResources(ctx, harness.js)
	if err != nil {
		t.Fatal(err)
	}
	// The assembly under test is the production one: dependencies get their own
	// context and the durable consumers get a detached lifecycle context whose
	// cancel happens only after they stop.
	dependencyContext, cancelDependencies := context.WithCancel(context.WithoutCancel(ctx))
	consumers := newCoreConsumers(ctx)
	t.Cleanup(consumers.close)
	inFlight := newGatedCallback()
	projector := newGatedObservationProjector(harness.service, inFlight)
	if startErr := consumers.startObservations(
		durable, harness.validator, projector, slog.New(slog.DiscardHandler),
	); startErr != nil {
		t.Fatal(startErr)
	}
	observationID := publishObservationForTest(
		ctx, t, harness.js, harness.validator, harness.runtimeID, harness.entityID, `true`,
	)
	waitForGatedCallback(t, inFlight, "in-flight observation did not reach projection")

	// Shutdown cancels its dependency contexts while this projection is gated in
	// flight. The callback context must stay live, because cancelDependencies
	// runs before transports drain in production.
	cancelDependencies()
	if dependencyContext.Err() == nil {
		t.Fatal("dependency context was not canceled")
	}
	if callbackErr := projector.callbackContext().Err(); callbackErr != nil {
		t.Fatalf("dependency cancellation reached the in-flight Observation callback: %v", callbackErr)
	}

	// Closing must drain before canceling callbacks, or this in-flight
	// Observation projection cannot commit.
	closed := make(chan struct{})
	go func() {
		consumers.close()
		close(closed)
	}()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return !consumers.observations.Active(), nil
	})
	if callbackErr := projector.callbackContext().Err(); callbackErr != nil {
		t.Fatalf("consumer close canceled the in-flight Observation before drain: %v", callbackErr)
	}
	close(inFlight.release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer close did not finish after the in-flight observation committed")
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return countObservationRows(t, harness.database, harness.entityID) == 1, nil
	})
	state, err := harness.service.GetEntity(ctx, harness.entityID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State == nil || state.State.ObservationID != observationID ||
		string(state.State.Value) != "true" {
		t.Fatalf("projected state = %#v, want observation %s", state.State, observationID)
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		info, infoErr := durable.Info(ctx)
		if infoErr != nil {
			return false, infoErr
		}
		return info.NumAckPending == 0, nil
	})

	// The consumer lifecycle context is canceled last, after every started
	// consumer has stopped, and that cancel still reaches callbacks.
	if cancelErr := projector.callbackContext().Err(); !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("consumer context after close = %v, want context.Canceled", cancelErr)
	}
}

// This test pins the other half of the shutdown contract and fails if a
// canceled consumer context no longer stops an in-flight callback from
// committing. Without it, the lifecycle tests above could pass for reasons
// unrelated to the callback context: they would keep reporting success even if
// a canceled context committed the row anyway. Canceling the consumer context
// before the gated callback finishes is exactly what the drain-before-cancel
// order prevents, so the row must stay absent here.
func TestCanceledConsumerContextAbortsInFlightCallback(t *testing.T) {
	t.Parallel()
	t.Run("entity event", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		harness := startCoreConsumerHarness(
			ctx, t,
			"run_01890f47-7a6b-7c4d-8e9f-0123456789ad",
			"control-events",
			eventSourceEntity(),
		)
		durable, err := devicesnats.ProvisionEntityEventResources(ctx, harness.js)
		if err != nil {
			t.Fatal(err)
		}
		inFlight := newGatedCallback()
		recorder := newGatedEntityEventRecorder(harness.service, inFlight)
		consumers := newCoreConsumers(ctx)
		t.Cleanup(consumers.close)
		if startErr := consumers.startEntityEvents(
			durable, harness.validator, recorder, slog.New(slog.DiscardHandler),
		); startErr != nil {
			t.Fatal(startErr)
		}
		publishEntityEventForTest(
			ctx, t, harness.js, harness.validator, "simulator", harness.runtimeID, harness.entityID,
			"single_press", "evt_01890f47-7a6b-7c4d-8e9f-0123456789c1",
		)
		waitForGatedCallback(t, inFlight, "in-flight report did not reach persistence")

		consumers.cancelCallbacks()
		if callbackErr := recorder.callbackContext().Err(); !errors.Is(callbackErr, context.Canceled) {
			t.Fatalf("consumer context for the in-flight callback = %v, want context.Canceled", callbackErr)
		}
		close(inFlight.release)
		select {
		case <-inFlight.finished:
		case <-time.After(5 * time.Second):
			t.Fatal("gated report did not return")
		}
		if rows := countEntityEventRows(t, harness.database, harness.entityID); rows != 0 {
			t.Fatalf("canceled callback context still committed %d entity event rows", rows)
		}
	})

	t.Run("observation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		harness := startCoreConsumerHarness(
			ctx, t,
			"run_01890f47-7a6b-7c4d-8e9f-0123456789ae",
			"control-light",
			powerEntity(),
		)
		durable, err := devicesnats.ProvisionObservationResources(ctx, harness.js)
		if err != nil {
			t.Fatal(err)
		}
		inFlight := newGatedCallback()
		projector := newGatedObservationProjector(harness.service, inFlight)
		consumers := newCoreConsumers(ctx)
		t.Cleanup(consumers.close)
		if startErr := consumers.startObservations(
			durable, harness.validator, projector, slog.New(slog.DiscardHandler),
		); startErr != nil {
			t.Fatal(startErr)
		}
		publishObservationForTest(
			ctx, t, harness.js, harness.validator, harness.runtimeID, harness.entityID, `true`,
		)
		waitForGatedCallback(t, inFlight, "in-flight observation did not reach projection")

		consumers.cancelCallbacks()
		if callbackErr := projector.callbackContext().Err(); !errors.Is(callbackErr, context.Canceled) {
			t.Fatalf("consumer context for the in-flight callback = %v, want context.Canceled", callbackErr)
		}
		close(inFlight.release)
		select {
		case <-inFlight.finished:
		case <-time.After(5 * time.Second):
			t.Fatal("gated observation did not return")
		}
		if rows := countObservationRows(t, harness.database, harness.entityID); rows != 0 {
			t.Fatalf("canceled callback context still committed %d observation rows", rows)
		}
	})
}

// coreConsumerHarness is the production-shaped assembly one durable-consumer
// callback test needs: a JetStream server, a migrated SQLite database, a claimed
// Adapter runtime, and one registered Entity the consumer callback persists.
// Tests add only the durable resource, the consumer, and the gated callback they
// exercise.
type coreConsumerHarness struct {
	js        jetstream.JetStream
	validator *contractsv1.Validator
	database  *sql.DB
	service   *devices.Service
	runtimeID devices.RuntimeID
	entityID  devices.EntityID
}

func startCoreConsumerHarness(
	ctx context.Context,
	t *testing.T,
	runtimeID devices.RuntimeID,
	bindingKey string,
	entity devices.EntityDescriptor,
) *coreConsumerHarness {
	t.Helper()
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
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devicessqlite.DeviceStores(devicessqlite.NewDeviceRepository(database, catalog)),
		nil,
		catalog,
		devices.Dependencies{},
	)
	if claimErr := service.ClaimAdapterRuntime(ctx, devices.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: runtimeID,
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := service.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: bindingKey,
		Device:     devices.DeviceDescriptor{Name: "Callback light", Kind: devices.DeviceKindLight},
		Entities:   []devices.EntityDescriptor{entity},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &coreConsumerHarness{
		js: js, validator: validator, database: database,
		service: service, runtimeID: runtimeID, entityID: binding.Entities[0].EntityID,
	}
}

func powerEntity() devices.EntityDescriptor {
	return devices.EntityDescriptor{
		Key: "power", ExternalID: "callback.power", Name: "Power",
		TypeID:  devices.EntityTypePowerV1,
		Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
	}
}

func eventSourceEntity() devices.EntityDescriptor {
	return devices.EntityDescriptor{
		Key: "events", ExternalID: "callback.events", Name: "Events",
		TypeID:  devices.EntityTypeEnumeventV1,
		Support: devices.EntitySupport(`{"state":{},"operations":{},"events":{"names":["single_press"]}}`),
	}
}

// observationWire is the test's own view of the observation payload, kept local
// so the assembly test never depends on the transport DTO.
type observationWire struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
}

func publishObservationForTest(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID devices.RuntimeID,
	entityID devices.EntityID,
	value string,
) devices.ObservationID {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	payload, err := natswire.Encode(
		validator,
		contractsv1.ObservationSchemaID,
		natswire.Envelope[observationWire]{
			ID: string(observationID), Schema: contractsv1.ObservationSchemaID,
			EmittedAt: receivedAt, CorrelationID: string(correlationID),
			Data: observationWire{
				EntityID: string(entityID), Value: json.RawMessage(value),
				AdapterReceivedAt: receivedAt,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.ObservationSubject("simulator", string(runtimeID), string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	message := &natsgo.Msg{Subject: subject, Data: payload, Header: make(natsgo.Header)}
	message.Header.Set(natsgo.MsgIdHdr, string(observationID))
	if _, publishErr := js.PublishMsg(ctx, message); publishErr != nil {
		t.Fatal(publishErr)
	}
	return observationID
}

func countObservationRows(t *testing.T, database *sql.DB, entityID devices.EntityID) int {
	t.Helper()
	var rows int
	if err := database.QueryRow(
		`SELECT count(*) FROM observations WHERE entity_id = ?`, string(entityID),
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
