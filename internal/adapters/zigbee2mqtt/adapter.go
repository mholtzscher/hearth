package zigbee2mqtt

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	reconnectMinimum            = 250 * time.Millisecond
	reconnectMaximum            = 5 * time.Second
	mqttQoS                     = byte(1)
	mappingPageLimit            = 200
	availabilityPage            = 256
	messageChannelBuffer        = 64
	runtimeEventBuffer          = 64
	concurrentRuntimeComponents = 2
	jitterDivisor               = 2

	externalSystemUnavailableReason = "hearth.external_system_unavailable"
	bridgeOfflineReason             = "adapter.hearth-adapter-zigbee2mqtt.bridge_offline"
	incompatibleConfigurationReason = "adapter.hearth-adapter-zigbee2mqtt.incompatible_configuration"
	invalidInventoryReason          = "adapter.hearth-adapter-zigbee2mqtt.invalid_inventory"
	deviceOfflineReason             = "adapter.hearth-adapter-zigbee2mqtt.device_offline"
	deviceMissingReason             = "adapter.hearth-adapter-zigbee2mqtt.device_missing"
	deviceDisabledReason            = "adapter.hearth-adapter-zigbee2mqtt.device_disabled"
	capabilityMissingReason         = "adapter.hearth-adapter-zigbee2mqtt.capability_missing"
)

type Session interface {
	ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
	MQTTURL   string
	BaseTopic string
	ClientID  string
}

type Adapter struct {
	session  Session
	config   Config
	profiles *ProfileCatalog
	logger   *slog.Logger
	dialer   mqttDialer

	runtimeEvents chan runtimeEvent
	runtimeDone   chan struct{}

	knownMappings map[mappingKey]adapter.OwnedMapping
	knownOrder    []mappingKey
	retryDelay    func(time.Duration) time.Duration
}

type sessionOperationError struct {
	operation string
	err       error
}

func (err *sessionOperationError) Error() string { return err.operation + ": " + err.err.Error() }
func (err *sessionOperationError) Unwrap() error { return err.err }

// New builds a Zigbee2MQTT adapter that plans discovery from an explicit
// embedded profile catalog. The catalog must come from the catalog loader;
// a nil or zero catalog fails startup before any external connection.
func New(session Session, config Config, profiles *ProfileCatalog, logger *slog.Logger) (*Adapter, error) {
	return newAdapter(session, config, profiles, logger, newPahoDialer())
}

func newAdapter(
	session Session,
	config Config,
	profiles *ProfileCatalog,
	logger *slog.Logger,
	dialer mqttDialer,
) (*Adapter, error) {
	switch {
	case session == nil:
		return nil, errors.New("Zigbee2MQTT adapter Session is required")
	case strings.TrimSpace(config.MQTTURL) == "":
		return nil, errors.New("Zigbee2MQTT MQTT URL is required")
	case !validRouteSlug(config.BaseTopic):
		return nil, errors.New("Zigbee2MQTT base topic must be a route-safe slug")
	case strings.TrimSpace(config.ClientID) == "":
		return nil, errors.New("Zigbee2MQTT MQTT client ID is required")
	case profiles == nil || !profiles.loaded:
		return nil, errors.New("Zigbee2MQTT profile catalog is required")
	case dialer == nil:
		return nil, errors.New("Zigbee2MQTT MQTT dialer is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(slog.String("component", adapterComponent))
	return &Adapter{
		session: session, config: config, profiles: profiles, logger: logger, dialer: dialer,
		runtimeEvents: make(chan runtimeEvent, runtimeEventBuffer),
		runtimeDone:   make(chan struct{}),
		knownMappings: make(map[mappingKey]adapter.OwnedMapping),
		retryDelay:    jitterReconnect,
	}, nil
}

func (z2m *Adapter) Run(ctx context.Context) error {
	runContext, cancel := context.WithCancel(ctx)
	results := make(chan error, concurrentRuntimeComponents)
	coordinator := newRuntimeCoordinator(runContext, z2m)
	go func() { results <- coordinator.run() }()
	go func() { results <- z2m.runConnections(runContext) }()

	first := <-results
	cancel()
	second := <-results
	if ctx.Err() != nil {
		return nil //nolint:nilerr // Parent cancellation is graceful shutdown.
	}
	for _, err := range []error{first, second} {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

func (z2m *Adapter) runConnections(ctx context.Context) error {
	delay := reconnectMinimum
	var generation uint64
	for {
		generation++
		synchronized, err := z2m.runConnection(ctx, generation)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, ok := errors.AsType[*sessionOperationError](err); ok {
			return err
		}
		if healthErr := z2m.reportUnhealthy(
			ctx,
			generation,
			externalSystemUnavailableReason,
		); healthErr != nil {
			return healthErr
		}
		if synchronized {
			delay = reconnectMinimum
		}
		wait := z2m.retryDelay(delay)
		z2m.logger.DebugContext(
			ctx,
			"Zigbee2MQTT connection retrying",
			slog.String(eventKey, "dependency.retrying"),
			slog.String("dependency", adapterComponent),
			slog.String("error_code", zigbee2MQTTErrorCode(err)),
			slog.Int64("retry_in_ms", max(wait.Milliseconds(), 0)),
		)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < reconnectMaximum {
			delay *= 2
			if delay > reconnectMaximum {
				delay = reconnectMaximum
			}
		}
	}
}

//nolint:gosec // Backoff jitter needs no cryptographic randomness.
func jitterReconnect(delay time.Duration) time.Duration {
	half := delay / jitterDivisor
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}
