package levoit

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const reasonPrefix = "adapter.hearth-adapter-levoit."

// Session is the SDK evidence and registration capability consumed by the Adapter.
type Session interface {
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

// Adapter has one Run owner; Command handlers may execute concurrently.
type Adapter struct {
	session   Session
	client    *client
	logger    *slog.Logger
	config    Config
	admission *lifecycle.AdmissionGroup
	mu        sync.RWMutex // Protects routes and runtime membership; other runtime fields use their gate.
	routes    map[string]adapter.CommandHandler
	devices   map[string]*deviceRuntime // Owned by Run.
}

type deviceRuntime struct {
	gate   chan struct{}
	device device
	active bool
	ids    map[string]string
	plans  []entityPlan
}

func New(session Session, config Config, credentials Credentials, transport http.RoundTripper,
	logger *slog.Logger) (*Adapter, error) {
	if session == nil {
		return nil, errors.New("levoit Session is required")
	}
	provider, err := newClient(config, credentials, transport)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{session: session, client: provider, logger: logger.With(slog.String("component", "levoit")),
		config: config, admission: lifecycle.NewAdmissionGroup(), routes: make(map[string]adapter.CommandHandler),
		devices: make(map[string]*deviceRuntime)}, nil
}

func (a *Adapter) Run(ctx context.Context) error {
	if err := a.loadMappings(ctx); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.cycle(ctx); err != nil {
			var rejected *adapter.RegistrationRejectedError
			if errors.Is(err, adapter.ErrClosed) || errors.Is(err, adapter.ErrRuntimeFenced) ||
				errors.As(err, &rejected) {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			a.logger.WarnContext(ctx, "Levoit evidence publication failed",
				slog.String("event", "levoit.publication_failed"), slog.String("error_code", "sdk_failed"))
		}
		timer := time.NewTimer(a.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// CloseCommands closes admission and joins actual handler returns, not just replies.
func (a *Adapter) CloseCommands(ctx context.Context) error {
	a.admission.CloseAdmission()
	return a.admission.Wait(ctx)
}

func (a *Adapter) HandleCommand(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
	reservation, ok := a.admission.TryAcquire()
	if !ok {
		return responder.RejectUnavailable("Levoit Adapter is stopping")
	}
	defer reservation.Release()
	a.mu.RLock()
	handler := a.routes[command.EntityID]
	a.mu.RUnlock()
	if handler == nil {
		return responder.RejectUnavailable("Purifier is not in the current account inventory")
	}
	return handler(ctx, command, responder)
}

func (a *Adapter) cycle(ctx context.Context) error {
	devices, err := a.client.devices(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		a.providerFailure(ctx, "inventory", err)
		return a.session.SetHealth(ctx, adapter.HealthReport{Status: adapter.HealthUnhealthy,
			SourceObservedAt: time.Now().UTC(), ReasonCode: reasonPrefix + failureCode(err)})
	}
	missing := a.reconcileMembership(devices)
	if err = a.session.SetHealth(ctx, adapter.HealthReport{Status: adapter.HealthHealthy,
		SourceObservedAt: time.Now().UTC()}); err != nil {
		return err
	}
	for _, runtime := range missing {
		if err = acquire(ctx, runtime.gate); err != nil {
			return err
		}
		err = a.unavailable(ctx, runtime, "device_missing")
		<-runtime.gate
		if err != nil {
			return err
		}
	}
	for _, d := range devices {
		if err = a.pollDevice(ctx, a.devices[bindingKey(d.CID)], d); err != nil {
			return err
		}
	}
	return nil
}

// Withdraw all omitted routes before waiting on any Device or SDK publication.
func (a *Adapter) reconcileMembership(devices []device) []*deviceRuntime {
	seen := make(map[string]bool, len(devices))
	for _, d := range devices {
		key := bindingKey(d.CID)
		seen[key] = true
		if a.devices[key] == nil {
			a.devices[key] = &deviceRuntime{gate: make(chan struct{}, 1), ids: make(map[string]string)}
		}
	}
	var missing []*deviceRuntime
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, runtime := range a.devices {
		if seen[key] {
			continue
		}
		runtime.active = false
		for _, id := range runtime.ids {
			delete(a.routes, id)
		}
		missing = append(missing, runtime)
	}
	return missing
}

func (a *Adapter) pollDevice(ctx context.Context, runtime *deviceRuntime, d device) error {
	if err := acquire(ctx, runtime.gate); err != nil {
		return err
	}
	defer func() { <-runtime.gate }()
	if err := a.register(ctx, runtime, d); err != nil {
		return err
	}
	if !d.Online {
		return a.unavailable(ctx, runtime, "device_offline")
	}
	report, err := a.client.status(ctx, d)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		a.providerFailure(ctx, "status", err)
		return a.unavailable(ctx, runtime, failureCode(err))
	}
	return a.publish(ctx, runtime, report, "", nil)
}

func (a *Adapter) providerFailure(ctx context.Context, operation string, err error) {
	a.logger.WarnContext(ctx, "Levoit request failed", slog.String("event", "levoit.request_failed"),
		slog.String("operation", operation), slog.String("error_code", failureCode(err)))
}

func acquire(ctx context.Context, gate chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
