package devices

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type manualServiceTicker struct {
	ticks chan time.Time
	once  sync.Once
}

func (ticker *manualServiceTicker) C() <-chan time.Time { return ticker.ticks }
func (ticker *manualServiceTicker) Stop()               { ticker.once.Do(func() {}) }

type lockedTestBuffer struct {
	mutex sync.Mutex
	bytes.Buffer
}

func (buffer *lockedTestBuffer) Write(value []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.Buffer.Write(value)
}

func (buffer *lockedTestBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.Buffer.String()
}

func TestNewRequiresDatabaseAndDefaultsLogger(t *testing.T) {
	if _, err := New(context.Background(), nil, nil); err == nil {
		t.Fatal("nil database was accepted")
	}
	database := openMigratedDeviceTestDB(t)
	service, err := New(context.Background(), database, nil)
	if err != nil {
		t.Fatal(err)
	}
	if service.logger != slog.Default() {
		t.Fatal("nil logger did not default to slog.Default")
	}
	if err := database.PingContext(context.Background()); err != nil {
		t.Fatalf("New closed the application-owned database: %v", err)
	}
	if state := service.lifecycle.state; state != serviceConstructed {
		t.Fatalf("state = %v, want constructed", state)
	}
}

func TestNewRecoversAndPrunesAtomically(t *testing.T) {
	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	controls := productionServiceControls()
	controls.now = func() time.Time { return base }
	service, database := newDeviceTestService(t, controls)
	running := runDeviceTestService(t, service, acceptingTestDelivery())
	entityID := registerCommandEntity(t, service)
	oldObservation := newObservation(t, "ent_01890f47-7a6b-7c4d-8e9f-111111111111", `true`, base.Add(-300*time.Hour))
	receipt, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: oldObservation, ObservedAt: base.Add(-300 * time.Hour),
	})
	if err != nil || receipt.Disposition != DispositionRejected {
		t.Fatalf("old receipt = %#v, %v", receipt, err)
	}
	command := commandRecord{
		id: commandTestID, entityID: entityID, adapterID: "simulator", operationName: OperationNameSet,
		parameters: CommandParameters(`{"value":true}`), correlationID: commandTestCorrelationID,
		status: commandStatusRequested, requestedAt: base.Add(-time.Minute), deadlineAt: base.Add(time.Minute),
	}
	if err := service.createCommand(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	running.stop(t)

	if _, err := database.Exec(`
		CREATE TRIGGER fail_receipt_prune
		BEFORE DELETE ON observation_receipts
		BEGIN SELECT RAISE(ABORT, 'simulated prune failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := newService(context.Background(), database, nil, controls); err == nil {
		t.Fatal("startup pruning failure was ignored")
	}
	stored, err := getCommand(context.Background(), database, commandTestID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusRequested {
		t.Fatalf("interruption escaped rolled-back startup transaction: %#v", stored)
	}
	assertReceiptCount(t, database, 1)

	if _, err := database.Exec("DROP TRIGGER fail_receipt_prune"); err != nil {
		t.Fatal(err)
	}
	recovered, err := newService(context.Background(), database, nil, controls)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.lifecycle.state != serviceConstructed {
		t.Fatalf("recovered state = %v", recovered.lifecycle.state)
	}
	stored, err = getCommand(context.Background(), database, commandTestID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusInterrupted || stored.failureCode == nil || *stored.failureCode != commandFailureCoreRestarted {
		t.Fatalf("recovered Command = %#v", stored)
	}
	assertReceiptCount(t, database, 0)
}

func TestRunRequiresDeliveryAndIsOneShot(t *testing.T) {
	service, _ := newDeviceTestService(t, productionServiceControls())
	if err := service.Run(context.Background(), nil); !errors.Is(err, ErrCommandDeliveryRequired) {
		t.Fatalf("nil delivery error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- service.Run(ctx, acceptingTestDelivery()) }()

	binding, err := service.Register(context.Background(), "simulator", validDomainRegistration())
	if err != nil || binding.DeviceID == "" {
		t.Fatalf("service did not become operational: %#v, %v", binding, err)
	}
	if err := service.Run(context.Background(), acceptingTestDelivery()); !errors.Is(err, ErrServiceAlreadyRun) {
		t.Fatalf("repeat Run error = %v", err)
	}
	cancel()
	if err := <-errorsChannel; err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background(), acceptingTestDelivery()); !errors.Is(err, ErrServiceAlreadyRun) {
		t.Fatalf("post-stop Run error = %v", err)
	}
}

func TestRunRetriesPruning(t *testing.T) {
	ticker := &manualServiceTicker{ticks: make(chan time.Time)}
	controls := productionServiceControls()
	controls.newTicker = func(time.Duration) serviceTicker { return ticker }
	var logs lockedTestBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	database := openMigratedDeviceTestDB(t)
	service, err := newService(context.Background(), database, logger, controls)
	if err != nil {
		t.Fatal(err)
	}
	runDeviceTestService(t, service, acceptingTestDelivery())

	old := time.Now().UTC().Add(-300 * time.Hour)
	observation := newObservation(t, "ent_01890f47-7a6b-7c4d-8e9f-222222222222", `true`, old)
	if _, err := service.ReceiveObservation(context.Background(), ReceivedObservation{
		AdapterID: "simulator", Observation: observation, ObservedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TRIGGER fail_periodic_prune
		BEFORE DELETE ON observation_receipts
		BEGIN SELECT RAISE(ABORT, 'simulated periodic failure'); END`); err != nil {
		t.Fatal(err)
	}
	ticker.ticks <- time.Now().UTC()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(logs.String(), "prune observation receipts") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(logs.String(), "prune observation receipts") {
		t.Fatal("periodic prune failure was not logged")
	}
	if _, err := database.Exec("DROP TRIGGER fail_periodic_prune"); err != nil {
		t.Fatal(err)
	}
	ticker.ticks <- time.Now().UTC()
	deadline = time.Now().Add(time.Second)
	for {
		var receipts int
		if err := database.QueryRow("SELECT count(*) FROM observation_receipts").Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		if receipts == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("receipt count = %d, want 0 after retry", receipts)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestServiceMethodsWaitForRunAndRejectAfterStop(t *testing.T) {
	service, database := newDeviceTestService(t, productionServiceControls())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Register(canceled, "simulator", validDomainRegistration()); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting Register error = %v", err)
	}
	if _, err := service.ReceiveObservation(canceled, ReceivedObservation{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting ReceiveObservation error = %v", err)
	}
	if _, err := service.GetEntity(canceled, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting GetEntity error = %v", err)
	}
	if _, err := service.ExecuteCommand(canceled, "", "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting ExecuteCommand error = %v", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- service.Run(ctx, acceptingTestDelivery()) }()
	if _, err := service.Register(context.Background(), "simulator", validDomainRegistration()); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-errorsChannel; err != nil {
		t.Fatal(err)
	}

	if _, err := service.Register(context.Background(), "simulator", validDomainRegistration()); !errors.Is(err, ErrServiceStopped) {
		t.Fatalf("stopped Register error = %v", err)
	}
	if _, err := service.ReceiveObservation(context.Background(), ReceivedObservation{}); !errors.Is(err, ErrServiceStopped) {
		t.Fatalf("stopped ReceiveObservation error = %v", err)
	}
	if _, err := service.GetEntity(context.Background(), ""); !errors.Is(err, ErrServiceStopped) {
		t.Fatalf("stopped GetEntity error = %v", err)
	}
	if _, err := service.ExecuteCommand(context.Background(), "", "", nil); !errors.Is(err, ErrServiceStopped) {
		t.Fatalf("stopped ExecuteCommand error = %v", err)
	}
	assertRegistrationCounts(t, database, 1, 1)
}

func TestNewInterruptsCommandsLeftByGracefulStop(t *testing.T) {
	service, database := newDeviceTestService(t, commandTestControls())
	delivered := make(chan struct{})
	delivery := commandDeliveryFunc(func(ctx context.Context, _ string, _ CommandDispatch) (CommandAcceptance, error) {
		close(delivered)
		<-ctx.Done()
		return CommandAcceptance{}, ctx.Err()
	})
	ctx, cancelRun := context.WithCancel(context.Background())
	runErrors := make(chan error, 1)
	go func() { runErrors <- service.Run(ctx, delivery) }()
	entityID := registerCommandEntity(t, service)
	commandErrors := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(context.Background(), entityID, OperationNameSet, CommandParameters(`{"value":true}`))
		commandErrors <- err
	}()
	<-delivered
	cancelRun()
	if err := <-runErrors; err != nil {
		t.Fatal(err)
	}
	if err := <-commandErrors; !errors.Is(err, ErrServiceStopped) {
		t.Fatalf("Command shutdown error = %v", err)
	}
	stored, err := getCommand(context.Background(), database, commandTestID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusRequested {
		t.Fatalf("graceful stop wrote a false outcome: %#v", stored)
	}

	recovered, err := newService(context.Background(), database, nil, commandTestControls())
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil {
		t.Fatal("recovery returned nil service")
	}
	stored, err = getCommand(context.Background(), database, commandTestID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.status != commandStatusInterrupted || stored.failureCode == nil || *stored.failureCode != commandFailureCoreRestarted {
		t.Fatalf("recovered Command = %#v", stored)
	}
}
