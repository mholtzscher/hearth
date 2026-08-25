package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

const (
	observationReceiptRetention = 192 * time.Hour
	commandPersistenceTimeout   = 5 * time.Second
	defaultPruneInterval        = time.Hour
)

type CommandDelivery interface {
	Deliver(context.Context, string, CommandDispatch) (CommandAcceptance, error)
}

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

type timeServiceTicker struct{ ticker *time.Ticker }

func (ticker *timeServiceTicker) C() <-chan time.Time { return ticker.ticker.C }
func (ticker *timeServiceTicker) Stop()               { ticker.ticker.Stop() }

func productionServiceControls() serviceControls {
	return serviceControls{
		now:              func() time.Time { return time.Now().UTC() },
		newDeviceID:      NewDeviceID,
		newEntityID:      NewEntityID,
		newCommandID:     NewCommandID,
		newCorrelationID: NewCorrelationID,
		newCatalog:       newBuiltinTypeCatalog,
		withDeadline:     context.WithDeadline,
		newTicker: func(interval time.Duration) serviceTicker {
			return &timeServiceTicker{ticker: time.NewTicker(interval)}
		},
		receiptRetention:   observationReceiptRetention,
		pruneInterval:      defaultPruneInterval,
		persistenceTimeout: commandPersistenceTimeout,
	}
}

type serviceState uint8

const (
	serviceConstructed serviceState = iota
	serviceRunning
	serviceStopping
	serviceStopped
)

type serviceLifecycle struct {
	mutex         sync.Mutex
	state         serviceState
	ready         chan struct{}
	stopped       chan struct{}
	moduleContext context.Context
	cancelModule  context.CancelCauseFunc
	work          sync.WaitGroup
	observations  sync.WaitGroup
}

type commandWaiters struct {
	mutex sync.Mutex
	byID  map[CommandID]chan CommandResult
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
	now, err := serviceNow(controls.now, "startup")
	if err != nil {
		return nil, err
	}
	if err := recoverAndPrune(ctx, database, now); err != nil {
		return nil, err
	}

	moduleContext, cancelModule := context.WithCancelCause(context.Background())
	return &Service{
		database: database,
		catalog:  catalog,
		logger:   logger,
		controls: controls,
		waiters:  commandWaiters{byID: make(map[CommandID]chan CommandResult)},
		lifecycle: serviceLifecycle{
			state: serviceConstructed, ready: make(chan struct{}), stopped: make(chan struct{}),
			moduleContext: moduleContext, cancelModule: cancelModule,
		},
	}, nil
}

func validateServiceControls(controls serviceControls) error {
	switch {
	case controls.now == nil:
		return errors.New("Device / Entity clock is required")
	case controls.newDeviceID == nil:
		return errors.New("Device ID generator is required")
	case controls.newEntityID == nil:
		return errors.New("Entity ID generator is required")
	case controls.newCommandID == nil:
		return errors.New("Command ID generator is required")
	case controls.newCorrelationID == nil:
		return errors.New("Correlation ID generator is required")
	case controls.newCatalog == nil:
		return errors.New("entity type catalog factory is required")
	case controls.withDeadline == nil:
		return errors.New("Command deadline control is required")
	case controls.newTicker == nil:
		return errors.New("maintenance ticker factory is required")
	case controls.receiptRetention <= 0:
		return errors.New("Observation receipt retention must be positive")
	case controls.pruneInterval <= 0:
		return errors.New("Observation receipt prune interval must be positive")
	case controls.persistenceTimeout <= 0:
		return errors.New("Command persistence timeout must be positive")
	default:
		return nil
	}
}

func (service *Service) Run(ctx context.Context, delivery CommandDelivery) error {
	if nilCommandDelivery(delivery) {
		return ErrCommandDeliveryRequired
	}

	service.lifecycle.mutex.Lock()
	if service.lifecycle.state != serviceConstructed {
		service.lifecycle.mutex.Unlock()
		return ErrServiceAlreadyRun
	}
	ticker := service.controls.newTicker(service.controls.pruneInterval)
	service.delivery = delivery
	service.lifecycle.state = serviceRunning
	service.lifecycle.work.Add(1)
	close(service.lifecycle.ready)
	service.lifecycle.mutex.Unlock()

	go service.runMaintenance(ticker)

	<-ctx.Done()
	service.lifecycle.mutex.Lock()
	if service.lifecycle.state == serviceRunning {
		service.lifecycle.state = serviceStopping
		service.lifecycle.cancelModule(ErrServiceStopped)
	}
	service.lifecycle.mutex.Unlock()

	service.lifecycle.work.Wait()
	service.lifecycle.mutex.Lock()
	service.lifecycle.state = serviceStopped
	close(service.lifecycle.stopped)
	service.lifecycle.mutex.Unlock()
	return nil
}

func nilCommandDelivery(delivery CommandDelivery) bool {
	if delivery == nil {
		return true
	}
	value := reflect.ValueOf(delivery)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (service *Service) runMaintenance(ticker serviceTicker) {
	defer service.lifecycle.work.Done()
	defer ticker.Stop()
	for {
		select {
		case <-service.lifecycle.moduleContext.Done():
			return
		case now := <-ticker.C():
			if err := pruneExpiredObservationReceipts(service.lifecycle.moduleContext, service.database, now.UTC()); err != nil {
				if !errors.Is(operationError(service.lifecycle.moduleContext, err), ErrServiceStopped) {
					service.logger.Error("prune observation receipts", "error", err)
				}
			}
		}
	}
}

func (service *Service) beginWork(ctx context.Context, observation bool) (func(), error) {
	for {
		service.lifecycle.mutex.Lock()
		switch service.lifecycle.state {
		case serviceRunning:
			service.lifecycle.work.Add(1)
			if observation {
				service.lifecycle.observations.Add(1)
			}
			service.lifecycle.mutex.Unlock()
			return func() {
				if observation {
					service.lifecycle.observations.Done()
				}
				service.lifecycle.work.Done()
			}, nil
		case serviceConstructed:
			ready := service.lifecycle.ready
			service.lifecycle.mutex.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case serviceStopping, serviceStopped:
			service.lifecycle.mutex.Unlock()
			return nil, ErrServiceStopped
		default:
			service.lifecycle.mutex.Unlock()
			return nil, ErrServiceStopped
		}
	}
}

func (service *Service) requestContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	stop := context.AfterFunc(service.lifecycle.moduleContext, func() {
		cancel(context.Cause(service.lifecycle.moduleContext))
	})
	if cause := context.Cause(service.lifecycle.moduleContext); cause != nil {
		cancel(cause)
	}
	return ctx, func() {
		stop()
		cancel(context.Canceled)
	}
}

func (service *Service) workContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	stop := context.AfterFunc(service.lifecycle.moduleContext, func() {
		cancel(context.Cause(service.lifecycle.moduleContext))
	})
	if cause := context.Cause(service.lifecycle.moduleContext); cause != nil {
		cancel(cause)
	}
	return ctx, func() {
		stop()
		cancel(context.Canceled)
	}
}

func (service *Service) writeContext(workContext context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(workContext, service.controls.persistenceTimeout)
}

func (service *Service) waitForObservationPublications() {
	service.lifecycle.observations.Wait()
}

func operationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(context.Cause(ctx), ErrServiceStopped) {
		return ErrServiceStopped
	}
	return err
}

func serviceNow(now func() time.Time, purpose string) (time.Time, error) {
	value := now().UTC()
	if value.IsZero() {
		return time.Time{}, fmt.Errorf("%s clock returned zero time", purpose)
	}
	return value, nil
}
