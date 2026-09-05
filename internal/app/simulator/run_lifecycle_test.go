package simulator_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"

	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
)

// lifecycleStore shares records across With-derived handlers.
type lifecycleStore struct {
	mutex   sync.Mutex
	records []slog.Record
}

type lifecycleHandler struct {
	store    *lifecycleStore
	minLevel slog.Level
	attrs    []slog.Attr
}

func lifecycleLogger(level slog.Level) (*slog.Logger, *lifecycleHandler) {
	handler := &lifecycleHandler{store: &lifecycleStore{}, minLevel: level}
	return slog.New(handler), handler
}

func (handler *lifecycleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.minLevel
}

func (handler *lifecycleHandler) Handle(_ context.Context, record slog.Record) error {
	combined := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	combined.AddAttrs(handler.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		combined.AddAttrs(attr)
		return true
	})
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	handler.store.records = append(handler.store.records, combined)
	return nil
}

func (handler *lifecycleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append(append([]slog.Attr(nil), handler.attrs...), attrs...)
	return &lifecycleHandler{store: handler.store, minLevel: handler.minLevel, attrs: combined}
}

func (handler *lifecycleHandler) WithGroup(string) slog.Handler { return handler }

func lifecycleRecords(handler *lifecycleHandler) []slog.Record {
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	return append([]slog.Record(nil), handler.store.records...)
}

func lifecycleAttr(record slog.Record, key string) (slog.Value, bool) {
	var found slog.Value
	matched := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			found = attr.Value
			matched = true
			return false
		}
		return true
	})
	return found, matched
}

// This test protects the simulator application lifecycle and fails if
// initialization evidence is missing, carries the wrong identity, or clean
// cancellation reports an error.
func TestRunInitializesAndStopsCleanly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger, recorder := lifecycleLogger(slog.LevelInfo)

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
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)

	service := startSimulatorTestCore(ctx, t, coreConnection, logger)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- simulatorapp.Run(runContext, simulatorapp.Config{
			AdapterID:  "simulator",
			NATSURL:    server.ClientURL(),
			BindingKey: "simulated-light",
			Scenario:   "happy",
		}, logger)
	}()

	initialized := waitForLifecycleEvent(t, recorder, "simulator.initialized", 10*time.Second)
	requireSimulatorInitialized(t, initialized)
	instance, err := service.GetAdapter(ctx, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != devices.AdapterHealthHealthy {
		t.Fatalf("simulator health = %#v, want healthy", instance.Health)
	}

	stopRun()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after cancellation: %v", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after cancellation: %v", ctx.Err())
	}
	requireCleanSimulatorStopping(t, recorder)
}

// startSimulatorTestCore assembles the minimal core transports the simulator
// application needs: session, registration, availability, and JetStream
// observation resources.
func startSimulatorTestCore(
	ctx context.Context,
	t *testing.T,
	coreConnection *natsgo.Conn,
	logger *slog.Logger,
) *devices.Service {
	t.Helper()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(
		devices.SQLiteStores(repository),
		devicesnats.NewCommandSender(coreConnection, mustCompileValidator(t)),
		catalog,
		devices.Dependencies{Logger: slog.New(slog.DiscardHandler)},
	)
	validator := mustCompileValidator(t)
	js, err := jetstream.New(coreConnection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = devicesnats.ProvisionObservationResources(ctx, js); err != nil {
		t.Fatal(err)
	}
	sessions, err := devicesnats.StartSessionServer(coreConnection, validator, service, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	registrations, err := devicesnats.StartRegistrationServer(coreConnection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	availability, err := devicesnats.StartEntityAvailabilityServer(coreConnection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = availability.Drain() })
	return service
}

func requireSimulatorInitialized(t *testing.T, initialized slog.Record) {
	t.Helper()
	if scenario, ok := lifecycleAttr(initialized, "scenario"); !ok || scenario.String() != "happy" {
		t.Fatalf("simulator.initialized scenario = %#v, want happy", initialized)
	}
	if entityID, ok := lifecycleAttr(initialized, "entity_id"); !ok || entityID.String() == "" {
		t.Fatalf("simulator.initialized omitted entity_id: %#v", initialized)
	}
	if component, ok := lifecycleAttr(initialized, "component"); !ok || component.String() != "simulator" {
		t.Fatalf("simulator.initialized component = %#v, want simulator", initialized)
	}
}

func requireCleanSimulatorStopping(t *testing.T, recorder *lifecycleHandler) {
	t.Helper()
	var stopping *slog.Record
	for _, record := range lifecycleRecords(recorder) {
		if event, ok := lifecycleAttr(record, "event"); ok && event.String() == "process.stopping" {
			candidate := record
			stopping = &candidate
		}
	}
	if stopping == nil {
		t.Fatal("missing process.stopping after cancellation")
	}
	if reason, ok := lifecycleAttr(*stopping, "reason_code"); !ok || reason.String() != "context_cancelled" {
		t.Fatalf("process.stopping reason = %#v, want context_cancelled", stopping)
	}
	for _, record := range lifecycleRecords(recorder) {
		if record.Level >= slog.LevelError {
			t.Fatalf("clean simulator shutdown emitted Error record: %#v", record)
		}
	}
}

// This test protects startup-failure teardown and fails if a post-connect
// registration blocked by cancellation emits no process.stopping, emits it
// more than once, or releases the live session before stopping is recorded.
func TestRunBlockedRegistrationStopsBeforeRelease(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger, recorder := lifecycleLogger(slog.LevelInfo)
	natsURL := startSessionOnlyCore(ctx, t, logger)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- simulatorapp.Run(runContext, simulatorapp.Config{
			AdapterID:  "simulator",
			NATSURL:    natsURL,
			BindingKey: "simulated-light",
			Scenario:   "happy",
		}, logger)
	}()

	// The claim milestone proves the live session connected before
	// registration started, so cancelling now deterministically interrupts
	// the blocked Register retry.
	waitForLifecycleEvent(t, recorder, "adapter.session_claimed", 10*time.Second)
	stopRun()
	select {
	case runErr := <-runErrors:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Run error = %v, want canceled registration", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after cancellation: %v", ctx.Err())
	}
	requireStoppingBeforeRelease(t, recorder)
}

// startSessionOnlyCore starts NATS with only the session claim server: the
// SDK can Connect (live session) but Register has no responder, so it
// retries until cancelled.
func startSessionOnlyCore(ctx context.Context, t *testing.T, logger *slog.Logger) string {
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
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)

	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	validator := mustCompileValidator(t)
	service := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		devicesnats.NewCommandSender(coreConnection, validator),
		catalog,
		devices.Dependencies{Logger: slog.New(slog.DiscardHandler)},
	)
	sessions, err := devicesnats.StartSessionServer(coreConnection, validator, service, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	return server.ClientURL()
}

// requireStoppingBeforeRelease fails unless exactly one process.stopping
// with the cancellation reason precedes the live session release.
func requireStoppingBeforeRelease(t *testing.T, recorder *lifecycleHandler) {
	t.Helper()
	var stoppingIndexes []int
	releasedIndex := -1
	for index, record := range lifecycleRecords(recorder) {
		event, eventFound := lifecycleAttr(record, "event")
		if !eventFound {
			continue
		}
		switch event.String() {
		case "process.stopping":
			stoppingIndexes = append(stoppingIndexes, index)
			reason, reasonFound := lifecycleAttr(record, "reason_code")
			if !reasonFound || reason.String() != "context_cancelled" {
				t.Fatalf("process.stopping reason = %#v, want context_cancelled", record)
			}
		case "adapter.session_released":
			if releasedIndex == -1 {
				releasedIndex = index
			}
		}
	}
	if len(stoppingIndexes) != 1 {
		t.Fatalf("process.stopping records = %d, want exactly one before teardown", len(stoppingIndexes))
	}
	if releasedIndex == -1 {
		t.Fatal("missing adapter.session_released for the live session")
	}
	if stoppingIndexes[0] > releasedIndex {
		t.Fatal("process.stopping followed session release, want stopping first")
	}
}

func mustCompileValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func waitForLifecycleEvent(
	t *testing.T,
	recorder *lifecycleHandler,
	event string,
	timeout time.Duration,
) slog.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, record := range lifecycleRecords(recorder) {
			if value, ok := lifecycleAttr(record, "event"); ok && value.String() == event {
				return record
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log event %q", event)
	return slog.Record{}
}
