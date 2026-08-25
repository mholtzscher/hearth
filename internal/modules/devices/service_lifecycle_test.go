package devices

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type manualServiceTicker struct {
	ticks chan time.Time
}

func (ticker *manualServiceTicker) C() <-chan time.Time { return ticker.ticks }
func (*manualServiceTicker) Stop()                      {}

type signalLogHandler struct {
	once sync.Once
	seen chan struct{}
}

func (*signalLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *signalLogHandler) Handle(context.Context, slog.Record) error {
	handler.once.Do(func() { close(handler.seen) })
	return nil
}
func (handler *signalLogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *signalLogHandler) WithGroup(string) slog.Handler      { return handler }

func TestNewRequiresDatabaseAndDefaultsLogger(t *testing.T) {
	if _, err := New(context.Background(), nil, nil); err == nil {
		t.Fatal("New accepted a nil database")
	}
	database := openMigratedDeviceTestDB(t)
	before := database.Stats().MaxOpenConnections
	service, err := New(context.Background(), database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if service.logger != slog.Default() {
		t.Fatal("New did not default the logger")
	}
	if database.Stats().MaxOpenConnections != before {
		t.Fatal("New changed database connection policy")
	}
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatalf("New closed the database: %v", err)
	}
}

func TestNewRecoversAndPrunesAtomically(t *testing.T) {
	database := openMigratedDeviceTestDB(t)
	now := time.Now().UTC()
	commandID, receiptID := insertLifecycleRows(t, database, now)
	if _, err := database.Exec(`
		CREATE TEMP TRIGGER fail_receipt_prune
		BEFORE DELETE ON observation_receipts
		BEGIN SELECT RAISE(ABORT, 'prune failed'); END`); err != nil {
		t.Fatal(err)
	}
	controls := productionServiceControls()
	controls.now = func() time.Time { return now }
	if _, err := newService(context.Background(), database, nil, controls); err == nil {
		t.Fatal("startup unexpectedly committed through failing prune")
	}
	assertLifecycleRows(t, database, commandID, receiptID, "requested", 1)
	if _, err := database.Exec("DROP TRIGGER fail_receipt_prune"); err != nil {
		t.Fatal(err)
	}
	if _, err := newService(context.Background(), database, nil, controls); err != nil {
		t.Fatal(err)
	}
	assertLifecycleRows(t, database, commandID, receiptID, "interrupted", 0)
}

func TestRunRequiresDeliveryAndIsOneShot(t *testing.T) {
	service, _ := newDeviceTestService(t, productionServiceControls())
	if err := service.Run(context.Background(), nil); !errors.Is(err, ErrCommandDeliveryRequired) {
		t.Fatalf("nil delivery error = %v", err)
	}
	delivery := testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{Accepted: true}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, delivery) }()
	if _, err := service.Register(context.Background(), "simulator", validDomainRegistration()); err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background(), delivery); !errors.Is(err, ErrServiceAlreadyRun) {
		t.Fatalf("repeat Run error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	concurrent, _ := newDeviceTestService(t, productionServiceControls())
	canceled, stop := context.WithCancel(context.Background())
	stop()
	errorsSeen := make(chan error, 2)
	go func() { errorsSeen <- concurrent.Run(canceled, delivery) }()
	go func() { errorsSeen <- concurrent.Run(canceled, delivery) }()
	first, second := <-errorsSeen, <-errorsSeen
	if !((first == nil && errors.Is(second, ErrServiceAlreadyRun)) ||
		(second == nil && errors.Is(first, ErrServiceAlreadyRun))) {
		t.Fatalf("concurrent Run errors = %v, %v", first, second)
	}
}

func TestRunRetriesPruning(t *testing.T) {
	database := openMigratedDeviceTestDB(t)
	ticker := &manualServiceTicker{ticks: make(chan time.Time)}
	logs := &signalLogHandler{seen: make(chan struct{})}
	controls := productionServiceControls()
	controls.newTicker = func(time.Duration) serviceTicker { return ticker }
	service, err := newService(context.Background(), database, slog.New(logs), controls)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	observationID, err := NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO observation_receipts (
			observation_id, adapter_id, entity_id, disposition, rejection_code,
			adapter_received_at, observed_at, expires_at
		) VALUES (?, 'simulator', 'unknown', 'rejected', 'unknown_entity', ?, ?, ?)`,
		observationID, formatTime(now), formatTime(now), formatTime(now.Add(-time.Hour)),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TEMP TRIGGER fail_periodic_prune
		BEFORE DELETE ON observation_receipts
		BEGIN SELECT RAISE(ABORT, 'prune failed'); END`); err != nil {
		t.Fatal(err)
	}
	runContext, stopRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- service.Run(runContext, testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
			return CommandAcceptance{Accepted: true}, nil
		}))
	}()
	ticker.ticks <- now
	<-logs.seen
	if _, err := database.Exec("DROP TRIGGER fail_periodic_prune"); err != nil {
		t.Fatal(err)
	}
	ticker.ticks <- now
	stopRun()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts WHERE observation_id = ?", observationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expired receipt count = %d", count)
	}
}

func TestServiceMethodsWaitForRunAndRejectAfterStop(t *testing.T) {
	service, _ := newDeviceTestService(t, productionServiceControls())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	observationID, _ := NewObservationID()
	entityID, _ := NewEntityID()
	for name, call := range map[string]func() error{
		"Register": func() error { _, err := service.Register(canceled, "simulator", validDomainRegistration()); return err },
		"ReceiveObservation": func() error {
			_, err := service.ReceiveObservation(canceled, ReceivedObservation{AdapterID: "simulator", Observation: Observation{ID: observationID, EntityID: entityID, Value: Value(`true`), AdapterReceivedAt: time.Now()}, ObservedAt: time.Now()})
			return err
		},
		"GetEntity": func() error { _, err := service.GetEntity(canceled, entityID); return err },
		"ExecuteCommand": func() error {
			_, err := service.ExecuteCommand(canceled, entityID, OperationNameSet, CommandParameters(`{"value":true}`))
			return err
		},
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s constructed error = %v", name, err)
		}
	}

	runContext, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	delivery := testDelivery(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{Accepted: true}, nil
	})
	go func() { done <- service.Run(runContext, delivery) }()
	if _, err := service.Register(context.Background(), "simulator", validDomainRegistration()); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"Register": func() error { _, err := service.Register(context.Background(), "bad", Registration{}); return err },
		"ReceiveObservation": func() error {
			_, err := service.ReceiveObservation(context.Background(), ReceivedObservation{})
			return err
		},
		"GetEntity":      func() error { _, err := service.GetEntity(context.Background(), "bad"); return err },
		"ExecuteCommand": func() error { _, err := service.ExecuteCommand(context.Background(), "bad", "bad", nil); return err },
	} {
		if err := call(); !errors.Is(err, ErrServiceStopped) {
			t.Fatalf("%s stopped error = %v", name, err)
		}
	}
}

func insertLifecycleRows(t *testing.T, database *sql.DB, now time.Time) (CommandID, ObservationID) {
	t.Helper()
	deviceID, _ := NewDeviceID()
	entityID, _ := NewEntityID()
	commandID, _ := NewCommandID()
	correlationID, _ := NewCorrelationID()
	receiptID, _ := NewObservationID()
	timestamp := formatTime(now)
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES (?, 'light', 'Light', ?, ?)", []any{deviceID, timestamp, timestamp}},
		{"INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at) VALUES (?, ?, 'Power', ?, ?, ?, ?)", []any{entityID, deviceID, EntityTypePowerV1, `{"state":{},"operations":{"set":{}}}`, timestamp, timestamp}},
		{"INSERT INTO adapter_bindings (adapter_id, binding_key, device_id, created_at, updated_at) VALUES ('simulator', 'light', ?, ?, ?)", []any{deviceID, timestamp, timestamp}},
		{"INSERT INTO adapter_entity_mappings (adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at) VALUES ('simulator', 'light', 'power', ?, 'light.one', ?, ?)", []any{entityID, timestamp, timestamp}},
		{"INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at) VALUES (?, ?, 'simulator', 'set', '{\"value\":true}', ?, 'requested', ?, ?)", []any{commandID, entityID, correlationID, timestamp, formatTime(now.Add(time.Hour))}},
		{"INSERT INTO observation_receipts (observation_id, adapter_id, entity_id, disposition, rejection_code, adapter_received_at, observed_at, expires_at) VALUES (?, 'simulator', ?, 'rejected', 'unknown_entity', ?, ?, ?)", []any{receiptID, entityID, timestamp, timestamp, formatTime(now.Add(-time.Hour))}},
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return commandID, receiptID
}

func assertLifecycleRows(t *testing.T, database *sql.DB, commandID CommandID, receiptID ObservationID, status string, receipts int) {
	t.Helper()
	var gotStatus string
	if err := database.QueryRow("SELECT status FROM commands WHERE id = ?", commandID).Scan(&gotStatus); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status {
		t.Fatalf("command status = %q, want %q", gotStatus, status)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM observation_receipts WHERE observation_id = ?", receiptID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != receipts {
		t.Fatalf("receipt count = %d, want %d", count, receipts)
	}
}
