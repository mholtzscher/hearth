package ecowitt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// Reconnect and runtime bounds.
const (
	// reconnectMinimum and reconnectMaximum bound Adapter-owned reconnect
	// backoff.
	reconnectMinimum = 250 * time.Millisecond
	reconnectMaximum = 5 * time.Second
	// jitterDivisor splits one backoff delay into its jittered upper half.
	jitterDivisor = 2
	// concurrentRuntimeComponents is the number of supervised Run loops.
	concurrentRuntimeComponents = 2
	// minimumUploadInterval and maximumUploadInterval bound the operator's
	// gateway upload interval, which scales both the report timeout and the
	// measurement stale interval.
	minimumUploadInterval = 8 * time.Second
	maximumUploadInterval = 600 * time.Second
	// maximumClientIDBytes is the MQTT 3.1.1 client-identifier compatibility
	// bound.
	maximumClientIDBytes = 23
)

// Adapter health reason codes.
const (
	// externalSystemUnavailableReason covers MQTT connect failure, disconnect,
	// subscription failure, and relay overflow.
	externalSystemUnavailableReason = "hearth.external_system_unavailable"
	// stationSilentReason covers an established subscription with no accepted
	// fresh report before the report timeout.
	stationSilentReason = "adapter.hearth-adapter-ecowitt.station_silent"
	// measurementStaleReason covers one Entity without a valid measurement for
	// three upload intervals.
	measurementStaleReason = "adapter.hearth-adapter-ecowitt.measurement_stale"
)

// Session is the Hearth Adapter SDK surface this Adapter uses. Every method is
// a blocking SDK call, so all calls run in tracked effect goroutines owned by
// the serial runtime coordinator.
type Session interface {
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

// Config is the validated runtime configuration one Adapter instance needs.
// It carries no topic-derived or vendor identity beyond the exact subscription
// and the expected PASSKEY, and it is never logged.
type Config struct {
	MQTTURL          string
	MQTTTopic        string
	MQTTClientID     string
	GatewayName      string
	OutdoorArrayName string
	ExpectedPasskey  [16]byte
	UploadInterval   time.Duration
}

// Adapter owns one Ecowitt GW2000 station on one exact MQTT topic: static slot
// registrations, the reconnect loop, report processing, health, availability,
// and Observation publication.
type Adapter struct {
	session    Session
	config     Config
	logger     *slog.Logger
	dialer     mqttDialer
	clock      clock
	retryDelay func(time.Duration) time.Duration
	plans      []measurementPlan
}

// New validates the invariants a direct package caller needs and returns a
// concrete Adapter with the production Paho transport.
func New(session Session, config Config, logger *slog.Logger) (*Adapter, error) {
	return newAdapter(session, config, logger, newPahoDialer())
}

// newAdapter is the package-private constructor that tests use to inject a fake
// MQTT transport.
func newAdapter(
	session Session,
	config Config,
	logger *slog.Logger,
	dialer mqttDialer,
) (*Adapter, error) {
	if err := validateAdapterConfig(session, config, dialer); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	plans, err := ecowittMeasurementCatalog()
	if err != nil {
		return nil, fmt.Errorf("compile Ecowitt Entity capability catalog: %w", err)
	}
	return &Adapter{
		session:    session,
		config:     config,
		logger:     logger.With(slog.String("component", adapterComponent)),
		dialer:     dialer,
		clock:      systemClock{},
		retryDelay: jitterReconnect,
		plans:      plans,
	}, nil
}

// validateAdapterConfig enforces the invariants this package requires. Every
// message is a fixed classification and never repeats the topic, the PASSKEY,
// or a broker address.
func validateAdapterConfig(session Session, config Config, dialer mqttDialer) error {
	switch {
	case session == nil:
		return errors.New("ecowitt adapter Session is required")
	case dialer == nil:
		return errors.New("ecowitt MQTT dialer is required")
	case !validMQTTBrokerURL(config.MQTTURL):
		return errors.New("ecowitt MQTT URL must be an mqtt:// or tcp:// broker URL")
	case !validExactMQTTTopic(config.MQTTTopic):
		return errors.New("ecowitt MQTT topic must be one exact two-segment topic without wildcards")
	case strings.TrimSpace(config.MQTTClientID) == "":
		return errors.New("ecowitt MQTT client ID is required")
	case len(config.MQTTClientID) > maximumClientIDBytes:
		return fmt.Errorf("ecowitt MQTT client ID must be at most %d bytes", maximumClientIDBytes)
	case !validDeviceName(config.GatewayName):
		return errors.New("ecowitt gateway Device name must be 1 to 128 characters")
	case !validDeviceName(config.OutdoorArrayName):
		return errors.New("ecowitt outdoor array Device name must be 1 to 128 characters")
	case config.ExpectedPasskey == [16]byte{}:
		return errors.New("ecowitt PASSKEY is required")
	case config.UploadInterval < minimumUploadInterval || config.UploadInterval > maximumUploadInterval:
		return fmt.Errorf(
			"ecowitt upload interval must be between %s and %s",
			minimumUploadInterval, maximumUploadInterval,
		)
	}
	return nil
}

// validMQTTBrokerURL reports whether the value is an absolute plain lowercase
// mqtt:// or tcp:// broker URL with an explicit host and port and no user info,
// path, query, fragment, or TLS scheme. The check is exact because a direct
// package caller bypasses the application's configuration validation. It never
// repeats the URL.
func validMQTTBrokerURL(brokerURL string) bool {
	// url.Parse lowercases parsed.Scheme, so a case-variant scheme like
	// MQTT:// or TCP:// would otherwise pass; the exact lowercase prefix is
	// checked before parsing.
	lowercaseScheme := strings.HasPrefix(brokerURL, "mqtt://") || strings.HasPrefix(brokerURL, "tcp://")
	parsed, err := url.Parse(brokerURL)
	if err != nil || !lowercaseScheme || (parsed.Scheme != "mqtt" && parsed.Scheme != "tcp") ||
		parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || strings.Contains(brokerURL, "#") {
		return false
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	return err == nil && port != 0
}

// validExactMQTTTopic reports whether the topic is one exact two-segment
// subject-safe topic with no MQTT wildcard.
func validExactMQTTTopic(topic string) bool {
	if topic == "" || strings.ContainsAny(topic, "#+") {
		return false
	}
	segments := strings.Split(topic, "/")
	if len(segments) != exactTopicSegmentCount {
		return false
	}
	for _, segment := range segments {
		if !validTopicSegment(segment) {
			return false
		}
	}
	return true
}

// validTopicSegment checks one topic segment against the exact
// [a-z0-9][a-z0-9_-]{0,62} shape.
func validTopicSegment(segment string) bool {
	if segment == "" || len(segment) > maximumTopicSegmentBytes {
		return false
	}
	for index := range len(segment) {
		character := segment[index]
		lower := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if index == 0 && !lower && !digit {
			return false
		}
		if !lower && !digit && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// maximumTopicSegmentBytes is the longest accepted MQTT topic segment.
const maximumTopicSegmentBytes = 63

// exactTopicSegmentCount is the required number of slash-separated topic
// segments.
const exactTopicSegmentCount = 2

// validDeviceName checks one configured Device display name after trimming.
func validDeviceName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	return utf8.RuneCountInString(trimmed) <= maximumDeviceNameRunes
}

// Run owns static registration, the MQTT reconnect loop, report processing,
// health, availability, and Observation publication. Both Device slots are
// registered before any MQTT connection is attempted.
func (ecowitt *Adapter) Run(ctx context.Context) error {
	routes, err := ecowitt.registerDevices(ctx)
	if err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	coordinator := newRuntimeCoordinator(
		runContext, cancel, ecowitt, routes, ecowitt.clock, &waitGroupEffects{},
	)
	results := make(chan error, concurrentRuntimeComponents)
	go func() { results <- coordinator.run() }()
	go func() { results <- ecowitt.runConnections(runContext, coordinator) }()
	first := <-results
	cancel()
	second := <-results
	if ctx.Err() != nil {
		return nil //nolint:nilerr // Parent cancellation is graceful shutdown.
	}
	for _, loopErr := range []error{first, second} {
		if loopErr != nil && !errors.Is(loopErr, context.Canceled) {
			return loopErr
		}
	}
	return nil
}

// registerDevices registers the two static Device slots and joins the Session's
// canonical Entity IDs to the capability catalog. A registration rejection is
// terminal: a static slot with no canonical identity cannot publish evidence.
func (ecowitt *Adapter) registerDevices(ctx context.Context) (routeSnapshot, error) {
	registrations := staticRegistrations(ecowitt.config, ecowitt.plans)
	bindings := make([]adapter.Binding, 0, len(registrations))
	for _, registration := range registrations {
		binding, err := ecowitt.session.Register(ctx, registration)
		if err != nil {
			return routeSnapshot{}, fmt.Errorf(
				"register Ecowitt Device %s: %w", registration.BindingKey, err,
			)
		}
		bindings = append(bindings, binding)
	}
	return buildRouteSnapshot(ecowitt.plans, bindings)
}

// HandleUnexpectedCommand is a defensive lifecycle handler only. An impossible
// Command is rejected as unavailable without publishing MQTT traffic or an
// Observation, because no Entity in this catalog accepts Commands.
func (ecowitt *Adapter) HandleUnexpectedCommand(
	ctx context.Context,
	_ adapter.Command,
	responder adapter.Responder,
) error {
	ecowitt.logger.WarnContext(
		ctx,
		"rejected unexpected Ecowitt command",
		slog.String(eventKey, "adapter.command_rejected"),
		slog.String(errorCodeKey, "no_command_routes"),
	)
	return responder.RejectUnavailable("Ecowitt Entities are read-only")
}
