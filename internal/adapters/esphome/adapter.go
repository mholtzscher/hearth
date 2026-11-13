// Package esphome is the stateless ESPHome Adapter. It plans each allowed device's entities into Hearth Entities,
// runs one serial worker per device, and routes Commands to the owning worker.
package esphome

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	adapterComponent = "esphome"
	clientInfo       = "hearth-adapter-esphome"
	ownedMappingPage = 200
	connectTimeout   = 10 * time.Second
	bindingKeyPrefix = "esphome-"
)

// Session is the minimal SDK seam this Adapter consumes.
type Session interface {
	ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

// Device is a concrete endpoint with an authorized transport, ready for a worker.
type Device struct {
	MAC       MAC
	Address   string
	Port      uint16
	Transport Transport
	Exposure  Exposure
}

// Config is the Adapter's validated configuration. Process assembly owns file and format validation.
type Config struct {
	Static    []Device
	Overrides map[MAC]DiscoveryOverride
	Discovery DiscoveryConfig
	Blocklist map[MAC]struct{}
}

// Adapter supervises one worker per static or discovered device.
type Adapter struct {
	session   Session
	config    Config
	logger    *slog.Logger
	dialer    nativeapi.Dialer
	router    *router
	timing    timing
	discovery DiscoverySource
}

// timing holds the reconnect policy; tests replace jitter for determinism.
type timing struct {
	connectTimeout time.Duration
	initialBackoff time.Duration
	maximumBackoff time.Duration
	stableAfter    time.Duration
	jitter         func(time.Duration) time.Duration
}

func defaultTiming() timing {
	return timing{
		connectTimeout: connectTimeout,
		initialBackoff: time.Second,
		maximumBackoff: time.Minute,
		stableAfter:    time.Minute,
		jitter:         jitterDelay,
	}
}

// sessionError marks a failed SDK Session call. It is terminal: Run returns it rather than reconnecting.
type sessionError struct {
	operation string
	err       error
}

func (err *sessionError) Error() string { return err.operation + ": " + err.err.Error() }
func (err *sessionError) Unwrap() error { return err.err }

// New builds an Adapter that dials devices over TCP.
func New(session Session, config Config, logger *slog.Logger, discovery DiscoverySource) (*Adapter, error) {
	return newAdapter(session, config, logger, nativeapi.NetDialer{}, defaultTiming(), discovery)
}

func newAdapter(
	session Session,
	config Config,
	logger *slog.Logger,
	dialer nativeapi.Dialer,
	timing timing,
	discovery DiscoverySource,
) (*Adapter, error) {
	switch {
	case session == nil:
		return nil, errors.New("esphome adapter Session is required")
	case dialer == nil:
		return nil, errors.New("esphome dialer is required")
	case len(config.Static) == 0 && !config.Discovery.Enabled && len(config.Blocklist) == 0:
		return nil, errors.New("esphome adapter needs at least one device")
	case len(config.Static)+len(config.Overrides) > maximumDevices:
		return nil, errors.New("esphome adapter supports at most 64 configured devices")
	case config.Discovery.Enabled && discovery == nil:
		return nil, errors.New("esphome discovery source is required")
	}
	seen := map[MAC]struct{}{}
	for _, device := range config.Static {
		if _, duplicate := seen[device.MAC]; duplicate {
			return nil, errors.New("esphome device MAC addresses must be unique")
		}
		seen[device.MAC] = struct{}{}
		if _, overlap := config.Overrides[device.MAC]; overlap {
			return nil, errors.New("esphome device MAC addresses must be unique")
		}
		if device.Address == "" || device.Port == 0 || device.Transport.mode == transportUnset {
			return nil, errors.New("esphome static device requires an endpoint and transport")
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		session:   session,
		config:    config,
		logger:    logger.With(slog.String("component", adapterComponent)),
		dialer:    dialer,
		router:    newRouter(),
		timing:    timing,
		discovery: discovery,
	}, nil
}

// Run reconciles owned mappings, reports health, and supervises one worker per device until ctx ends. Parent
// cancellation returns nil; a Session failure returns it.
func (a *Adapter) Run(ctx context.Context) error {
	owned, err := a.listOwnedMappings(ctx)
	if err != nil {
		return a.terminal(ctx, err)
	}
	// Core admits Entity availability only after this runtime reports healthy.
	if setHealthErr := a.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); setHealthErr != nil {
		return a.terminal(ctx, &sessionError{operation: "set health", err: setHealthErr})
	}
	if reportUnconfiguredErr := a.reportUnconfigured(ctx, owned); reportUnconfiguredErr != nil {
		return a.terminal(ctx, reportUnconfiguredErr)
	}

	return a.terminal(ctx, a.runDevices(ctx, owned))
}

// HandleCommand is the SDK CommandHandler. It routes to the owning device worker.
func (a *Adapter) HandleCommand(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
	owner, found, ready := a.router.lookup(command.EntityID)
	switch {
	case !ready:
		return responder.RejectUnavailable("ESPHome adapter is starting")
	case !found:
		return responder.Reject("Entity is not owned by this ESPHome adapter")
	case owner == nil:
		return responder.RejectUnavailable("ESPHome device is awaiting discovery")
	}
	return owner.submit(ctx, command, responder)
}

// listOwnedMappings groups this Adapter's owned Entities by Binding key and then Entity key.
func (a *Adapter) listOwnedMappings(ctx context.Context) (map[string]map[string]string, error) {
	owned := map[string]map[string]string{}
	request := adapter.OwnedMappingPageRequest{Limit: ownedMappingPage}
	for {
		page, err := a.session.ListOwnedMappings(ctx, request)
		if err != nil {
			return nil, &sessionError{operation: "list owned mappings", err: err}
		}
		for _, item := range page.Items {
			if owned[item.BindingKey] == nil {
				owned[item.BindingKey] = map[string]string{}
			}
			owned[item.BindingKey][item.EntityKey] = item.EntityID
		}
		if page.NextCursor == "" {
			return owned, nil
		}
		request.Cursor = page.NextCursor
	}
}

// reportUnconfigured marks non-static owned Entities unavailable, distinguishing pending discovery from blocked config.
func (a *Adapter) reportUnconfigured(ctx context.Context, owned map[string]map[string]string) error {
	configured := map[string]struct{}{}
	for _, device := range a.config.Static {
		if _, blocked := a.config.Blocklist[device.MAC]; !blocked {
			configured[device.MAC.BindingKey()] = struct{}{}
		}
	}
	entityCount := 0
	for bindingKey, entities := range owned {
		if _, isConfigured := configured[bindingKey]; isConfigured || !strings.HasPrefix(bindingKey, bindingKeyPrefix) {
			continue
		}
		reason := reasonDeviceNotConfigured
		if a.config.Discovery.Enabled {
			reason = reasonDiscoveryPending
		}
		for mac := range a.config.Blocklist {
			if mac.BindingKey() == bindingKey {
				reason = reasonDeviceBlocked
				break
			}
		}
		if err := reportUnavailable(ctx, a.session, entities, reason); err != nil {
			return err
		}
		entityCount += len(entities)
	}
	if entityCount == 0 {
		return nil
	}
	a.logger.InfoContext(ctx, "unconfigured devices marked unavailable",
		slog.String("event", "device.unconfigured_unavailable"),
		slog.Int("entity_count", entityCount),
	)
	return nil
}

// terminal returns nil for parent cancellation and diagnoses every other failure once.
func (a *Adapter) terminal(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil
	}
	operation := "unknown"
	errorCode := "runtime_failed"
	if session, ok := errors.AsType[*sessionError](err); ok {
		operation = session.operation
		errorCode = "session_operation_failed"
	} else if _, discovery := errors.AsType[*discoveryError](err); discovery {
		operation = "discovery"
		errorCode = "discovery_failed"
	}
	a.logger.ErrorContext(ctx, "esphome adapter stopped",
		slog.String("event", "adapter.stopped"),
		slog.String("error_code", errorCode),
		slog.String("operation", operation),
	)
	return err
}

// jitterDelay spreads reconnects over [d/2, d].
func jitterDelay(delay time.Duration) time.Duration {
	half := delay / 2 //nolint:mnd // Half the delay is fixed, half is random.
	if half <= 0 {
		return delay
	}
	return half + rand.N(half+1) //nolint:gosec // Reconnect jitter is not security sensitive.
}
