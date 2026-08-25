package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	observationReceiptRetention = 192 * time.Hour
	maintenanceInterval         = time.Hour
	commandPersistenceTimeout   = 5 * time.Second
)

type serviceTicker interface {
	C() <-chan time.Time
	Stop()
}

type serviceControls struct {
	now                func() time.Time
	newDeviceID        func() (DeviceID, error)
	newEntityID        func() (EntityID, error)
	newCommandID       func() (CommandID, error)
	newCorrelationID   func() (CorrelationID, error)
	newCatalog         func() (*typeCatalog, error)
	withDeadline       func(context.Context, time.Time) (context.Context, context.CancelFunc)
	newTicker          func(time.Duration) serviceTicker
	receiptRetention   time.Duration
	pruneInterval      time.Duration
	persistenceTimeout time.Duration
}

type timeTicker struct{ *time.Ticker }

func (ticker timeTicker) C() <-chan time.Time { return ticker.Ticker.C }

func productionServiceControls() serviceControls {
	return serviceControls{
		now:                time.Now,
		newDeviceID:        NewDeviceID,
		newEntityID:        NewEntityID,
		newCommandID:       NewCommandID,
		newCorrelationID:   NewCorrelationID,
		newCatalog:         newBuiltinTypeCatalog,
		withDeadline:       context.WithDeadline,
		newTicker:          func(interval time.Duration) serviceTicker { return timeTicker{time.NewTicker(interval)} },
		receiptRetention:   observationReceiptRetention,
		pruneInterval:      maintenanceInterval,
		persistenceTimeout: commandPersistenceTimeout,
	}
}

type Service struct {
	database  *sql.DB
	delivery  CommandDelivery
	catalog   *typeCatalog
	logger    *slog.Logger
	controls  serviceControls
	waiters   commandWaiters
	lifecycle serviceLifecycle
}

type commandWaiters struct {
	mutex sync.Mutex
	byID  map[CommandID]chan CommandResult
}

type serviceState uint8

const (
	serviceConstructed serviceState = iota
	serviceRunning
	serviceStopping
	serviceStopped
)

type serviceLifecycle struct {
	mutex       sync.Mutex
	state       serviceState
	claimed     bool
	ready       chan struct{}
	workContext context.Context
	cancelWork  context.CancelCauseFunc
	work        sync.WaitGroup
}

func New(ctx context.Context, database *sql.DB, logger *slog.Logger) (*Service, error) {
	return newService(ctx, database, logger, productionServiceControls())
}

func newService(
	ctx context.Context,
	database *sql.DB,
	logger *slog.Logger,
	controls serviceControls,
) (*Service, error) {
	if database == nil {
		return nil, errors.New("Device / Entity database is required")
	}
	if err := validateServiceControls(controls); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	catalog, err := controls.newCatalog()
	if err != nil {
		return nil, fmt.Errorf("construct entity type catalog: %w", err)
	}
	if catalog == nil {
		return nil, errors.New("construct entity type catalog: catalog is nil")
	}
	now := controls.now().UTC()
	if now.IsZero() {
		return nil, errors.New("Device / Entity clock returned zero time")
	}
	tx, err := database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin Device / Entity startup: %w", err)
	}
	defer tx.Rollback()
	if err := interruptActiveCommands(ctx, tx, now); err != nil {
		return nil, err
	}
	if err := deleteExpiredObservationReceipts(ctx, tx, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit Device / Entity startup: %w", err)
	}
	return &Service{
		database:  database,
		catalog:   catalog,
		logger:    logger,
		controls:  controls,
		waiters:   commandWaiters{byID: make(map[CommandID]chan CommandResult)},
		lifecycle: serviceLifecycle{state: serviceConstructed, ready: make(chan struct{})},
	}, nil
}

func validateServiceControls(controls serviceControls) error {
	if controls.now == nil || controls.newDeviceID == nil || controls.newEntityID == nil ||
		controls.newCommandID == nil || controls.newCorrelationID == nil || controls.newCatalog == nil ||
		controls.withDeadline == nil || controls.newTicker == nil || controls.receiptRetention <= 0 ||
		controls.pruneInterval <= 0 || controls.persistenceTimeout <= 0 {
		return errors.New("Device / Entity service controls are incomplete")
	}
	return nil
}

func (service *Service) Run(ctx context.Context, delivery CommandDelivery) error {
	if delivery == nil {
		return ErrCommandDeliveryRequired
	}
	service.lifecycle.mutex.Lock()
	if service.lifecycle.claimed {
		service.lifecycle.mutex.Unlock()
		return ErrServiceAlreadyRun
	}
	service.lifecycle.claimed = true
	service.delivery = delivery
	service.lifecycle.workContext, service.lifecycle.cancelWork = context.WithCancelCause(context.Background())
	service.lifecycle.state = serviceRunning
	close(service.lifecycle.ready)
	service.lifecycle.mutex.Unlock()

	ticker := service.controls.newTicker(service.controls.pruneInterval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C():
			service.pruneReceipts(now)
		case <-ctx.Done():
			service.stop()
			return nil
		}
	}
}

func (service *Service) pruneReceipts(now time.Time) {
	ctx, done, ok := service.beginMaintenance()
	if !ok {
		return
	}
	defer done()
	if err := deleteExpiredObservationReceipts(ctx, service.database, now.UTC()); err != nil {
		if !errors.Is(normalizeServiceError(ctx, err), ErrServiceStopped) {
			service.logger.Error("prune observation receipts", "error", err)
		}
	}
}

func (service *Service) beginMaintenance() (context.Context, func(), bool) {
	service.lifecycle.mutex.Lock()
	defer service.lifecycle.mutex.Unlock()
	if service.lifecycle.state != serviceRunning {
		return nil, nil, false
	}
	service.lifecycle.work.Add(1)
	return service.lifecycle.workContext, service.lifecycle.work.Done, true
}

func (service *Service) stop() {
	service.lifecycle.mutex.Lock()
	service.lifecycle.state = serviceStopping
	service.lifecycle.cancelWork(ErrServiceStopped)
	service.lifecycle.mutex.Unlock()
	service.lifecycle.work.Wait()
	service.lifecycle.mutex.Lock()
	service.lifecycle.state = serviceStopped
	service.lifecycle.mutex.Unlock()
}

func (service *Service) beginWork(caller context.Context) (context.Context, context.Context, func(), error) {
	for {
		service.lifecycle.mutex.Lock()
		switch service.lifecycle.state {
		case serviceConstructed:
			ready := service.lifecycle.ready
			service.lifecycle.mutex.Unlock()
			select {
			case <-ready:
				continue
			case <-caller.Done():
				return nil, nil, nil, caller.Err()
			}
		case serviceRunning:
			moduleContext := service.lifecycle.workContext
			service.lifecycle.work.Add(1)
			service.lifecycle.mutex.Unlock()
			requestContext, cancel := withModuleCancellation(caller, moduleContext)
			return requestContext, moduleContext, func() {
				cancel()
				service.lifecycle.work.Done()
			}, nil
		default:
			service.lifecycle.mutex.Unlock()
			return nil, nil, nil, ErrServiceStopped
		}
	}
}

func withModuleCancellation(parent, module context.Context) (context.Context, context.CancelFunc) {
	ctx, cancelCause := context.WithCancelCause(parent)
	stop := context.AfterFunc(module, func() { cancelCause(ErrServiceStopped) })
	return ctx, func() {
		stop()
		cancelCause(context.Canceled)
	}
}

func normalizeServiceError(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		if errors.Is(cause, ErrServiceStopped) {
			return ErrServiceStopped
		}
		return cause
	}
	return err
}

func (service *Service) now() (time.Time, error) {
	now := service.controls.now().UTC()
	if now.IsZero() {
		return time.Time{}, errors.New("Device / Entity clock returned zero time")
	}
	return now, nil
}
