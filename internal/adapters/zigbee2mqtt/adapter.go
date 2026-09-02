package zigbee2mqtt

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	reconnectMinimum     = 250 * time.Millisecond
	reconnectMaximum     = 5 * time.Second
	mqttQoS              = byte(1)
	mappingPageLimit     = 200
	availabilityPage     = 256
	messageChannelBuffer = 64
	jitterDivisor        = 2

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
	session Session
	config  Config
	logger  *slog.Logger
	dialer  mqttDialer

	routeLifecycle   sync.RWMutex
	mutex            sync.Mutex
	connection       mqttConnection
	connectionCancel context.CancelCauseFunc
	generation       uint64
	healthy          bool
	routeSerial      uint64
	routes           map[string]commandRoute
	devices          map[string]runtimeDevice
	matchers         map[string]*commandMatcher
	deviceLocks      map[string]*contextLock

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

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error) {
	return newAdapter(session, config, logger, newPahoDialer())
}

func newAdapter(session Session, config Config, logger *slog.Logger, dialer mqttDialer) (*Adapter, error) {
	switch {
	case session == nil:
		return nil, errors.New("Zigbee2MQTT adapter Session is required")
	case strings.TrimSpace(config.MQTTURL) == "":
		return nil, errors.New("Zigbee2MQTT MQTT URL is required")
	case !validRouteSlug(config.BaseTopic):
		return nil, errors.New("Zigbee2MQTT base topic must be a route-safe slug")
	case strings.TrimSpace(config.ClientID) == "":
		return nil, errors.New("Zigbee2MQTT MQTT client ID is required")
	case dialer == nil:
		return nil, errors.New("Zigbee2MQTT MQTT dialer is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		session: session, config: config, logger: logger, dialer: dialer,
		routes: make(map[string]commandRoute), devices: make(map[string]runtimeDevice),
		matchers: make(map[string]*commandMatcher), deviceLocks: make(map[string]*contextLock),
		knownMappings: make(map[mappingKey]adapter.OwnedMapping),
		retryDelay:    jitterReconnect,
	}, nil
}

func (z2m *Adapter) Run(ctx context.Context) error {
	delay := reconnectMinimum
	for {
		synchronized, err := z2m.runConnection(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Parent cancellation is graceful shutdown.
		}
		if _, ok := errors.AsType[*sessionOperationError](err); ok {
			return err
		}
		if healthErr := z2m.reportUnhealthy(ctx, externalSystemUnavailableReason); healthErr != nil {
			return healthErr
		}
		if synchronized {
			delay = reconnectMinimum
		}
		wait := z2m.retryDelay(delay)
		z2m.logger.WarnContext(ctx, "Zigbee2MQTT connection ended; reconnecting", "error", err, "retry_in", wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
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
