package august

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const reasonPrefix = "adapter.hearth-adapter-august."

// Session is the registration and evidence capability consumed by this Adapter.
type Session interface {
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

// Adapter has one Run owner. Each physical lock has an independent evidence gate.
type Adapter struct {
	session        Session
	client         *client
	logger         *slog.Logger
	config         Config
	admission      *lifecycle.AdmissionGroup
	devices        map[string]*deviceRuntime // Membership is owned by Run, never the push reader.
	lastInventory  time.Time
	forceInventory atomic.Bool // Authentication failures require a complete account check on the next scheduled cycle.
}

type deviceRuntime struct {
	gate             chan struct{}
	config           *BindingConfig // Nil for a previously owned, now unconfigured binding.
	entry            *inventoryEntry
	device           device
	online           bool
	channel          string
	pushRecoveryUsed bool // Coalesce recovery-triggering push until the next scheduled acquisition.
	ids              map[string]string
	fields           map[string]*fieldRuntime
}

type fieldRuntime struct {
	available bool
	source    *time.Time
	value     string // Canonical JSON of the last successfully published field, for equal-time conflicts.
}

func newDeviceRuntime() *deviceRuntime {
	return &deviceRuntime{
		gate:   make(chan struct{}, 1),
		ids:    make(map[string]string),
		fields: make(map[string]*fieldRuntime),
	}
}

func New(session Session, config Config, credentials Credentials, transport http.RoundTripper,
	logger *slog.Logger) (*Adapter, error) {
	if session == nil {
		return nil, errors.New("august Session is required")
	}
	provider, err := newClient(config, credentials, transport)
	if err != nil {
		return nil, err
	}
	for _, binding := range provider.config.Bindings {
		if binding.CommandsEnabled {
			return nil, errors.New("august commands require the uncompleted live evidence gate")
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	a := &Adapter{
		session:   session,
		client:    provider,
		config:    provider.config,
		logger:    logger.With(slog.String("component", "august")),
		admission: lifecycle.NewAdmissionGroup(),
		devices:   make(map[string]*deviceRuntime),
	}
	for _, binding := range provider.config.Bindings {
		runtime := newDeviceRuntime()
		runtime.config = &binding
		a.devices[binding.BindingKey] = runtime
	}
	return a, nil
}

// HandleCommand cannot dispatch while D4/D5 remain uncompleted. Monitoring never installs a set handler.
func (a *Adapter) HandleCommand(_ context.Context, _ adapter.Command, responder adapter.Responder) error {
	reservation, ok := a.admission.TryAcquire()
	if !ok {
		return responder.RejectUnavailable("August Adapter is stopping")
	}
	defer reservation.Release()
	return responder.RejectUnavailable("August lock commands are not enabled")
}

func (a *Adapter) CloseCommands(ctx context.Context) error {
	a.admission.CloseAdmission()
	return a.admission.Wait(ctx)
}

// cycle runs one acquisition without catch-up. Failed inventory is never omission evidence.
func (a *Adapter) cycle(ctx context.Context, refresh bool) (bool, error) {
	refresh = a.forceInventory.Swap(false) || refresh
	if refresh || a.lastInventory.IsZero() || time.Since(a.lastInventory) >= a.config.InventoryInterval {
		entries, err := a.client.inventory(ctx)
		if err != nil {
			a.forceInventory.Store(true)
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			a.providerFailure(ctx, "inventory", err)
			return false, a.accountUnavailable(ctx, failureCode(err))
		}
		a.reconcileMembership(entries)
		a.lastInventory = time.Now()
		refresh = true
		if err = a.session.SetHealth(
			ctx,
			adapter.HealthReport{Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC()},
		); err != nil {
			return false, err
		}
	}
	var workers sync.WaitGroup
	errorsOut := make(chan error, len(a.devices))
	for _, runtime := range a.devices {
		workers.Go(func() {
			if err := a.pollDevice(ctx, runtime, refresh); err != nil {
				errorsOut <- err
			}
		})
	}
	workers.Wait()
	close(errorsOut)
	var result error
	for err := range errorsOut {
		result = errors.Join(result, err)
	}
	return true, result
}

func (a *Adapter) reconcileMembership(entries []inventoryEntry) {
	byID := make(map[lockID]inventoryEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, runtime := range a.devices {
		runtime.entry = nil
		if runtime.config != nil {
			if entry, ok := byID[lockID(runtime.config.LockID)]; ok {
				runtime.entry = &entry
			}
		}
	}
}

func (a *Adapter) pollDevice(ctx context.Context, runtime *deviceRuntime, refresh bool) error {
	if err := acquire(ctx, runtime.gate); err != nil {
		return err
	}
	defer func() { <-runtime.gate }()
	runtime.pushRecoveryUsed = false
	if runtime.entry == nil {
		return a.unavailable(ctx, runtime, "device_missing")
	}
	if runtime.entry.UserType != roleSuperuser {
		return a.unavailable(ctx, runtime, "unsupported_role")
	}
	if refresh || needsRecovery(runtime) {
		if err := a.refreshDetail(ctx, runtime); err != nil {
			return err
		}
	}
	if !runtime.online {
		return nil
	}
	report, err := a.client.status(ctx, runtime.entry.ID)
	if err != nil {
		return a.targetFailure(ctx, runtime, "status", err)
	}
	if report.Online != nil && !*report.Online {
		return a.unavailable(ctx, runtime, "device_offline")
	}
	keys := []string{keyLock, keyDoorOpen}
	if report.Battery != nil {
		keys = append(keys, keyBattery)
	}
	return a.publish(ctx, runtime, report, keys)
}

func needsRecovery(runtime *deviceRuntime) bool {
	if !runtime.online || len(runtime.fields) != 3 {
		return true
	}
	for _, field := range runtime.fields {
		if !field.available {
			return true
		}
	}
	return false
}

func (a *Adapter) refreshDetail(ctx context.Context, runtime *deviceRuntime) error {
	detail, err := a.client.detail(ctx, *runtime.entry)
	if err != nil {
		return a.targetFailure(ctx, runtime, "detail", err)
	}
	if detail.Device.Model != supportedModel {
		return a.unavailable(ctx, runtime, "unsupported_model")
	}
	if err = a.register(ctx, runtime, detail.Device); err != nil {
		return err
	}
	runtime.channel = detail.Channel
	if detail.Report.Online == nil || !*detail.Report.Online {
		return a.unavailable(ctx, runtime, "device_offline")
	}
	runtime.online = true
	return a.publish(ctx, runtime, detail.Report, []string{keyLock, keyDoorOpen, keyBattery})
}

func (a *Adapter) targetFailure(ctx context.Context, runtime *deviceRuntime, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	a.providerFailure(ctx, operation, err)
	switch failureCode(err) {
	case codeAuthRejected, codeValidation, codeExpired:
		a.forceInventory.Store(true)
		if healthErr := a.session.SetHealth(ctx, adapter.HealthReport{Status: adapter.HealthUnhealthy,
			SourceObservedAt: time.Now().UTC(), ReasonCode: reasonPrefix + failureCode(err)}); healthErr != nil {
			return healthErr
		}
	}
	return a.unavailable(ctx, runtime, failureCode(err))
}

func (a *Adapter) accountUnavailable(ctx context.Context, reason string) error {
	if err := a.session.SetHealth(ctx, adapter.HealthReport{Status: adapter.HealthUnhealthy,
		SourceObservedAt: time.Now().UTC(), ReasonCode: reasonPrefix + reason}); err != nil {
		return err
	}
	for _, runtime := range a.devices {
		if err := acquire(ctx, runtime.gate); err != nil {
			return err
		}
		err := a.unavailable(ctx, runtime, reason)
		<-runtime.gate
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *Adapter) providerFailure(ctx context.Context, operation string, err error) {
	a.logger.WarnContext(ctx, "August request failed", slog.String("event", "august.request_failed"),
		slog.String("operation", operation), slog.String("error_code", failureCode(err)))
}
