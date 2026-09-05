package homeassistant

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	reconnectMinimum                = 250 * time.Millisecond
	reconnectMaximum                = 5 * time.Second
	stateOff                        = "off"
	stateUnavailable                = "unavailable"
	stateUnknown                    = "unknown"
	jitterDivisor                   = 2
	authenticationFailedReason      = "hearth.authentication_failed"
	externalSystemUnavailableReason = "hearth.external_system_unavailable"
	entityUnavailableReason         = "adapter.hearth-adapter-homeassistant.entity_unavailable"
)

type Session interface {
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
}

type Config struct {
	URL              string
	Token            string
	ExternalEntityID string
	EntityID         string
}

type Adapter struct {
	session Session
	config  Config
	logger  *slog.Logger
	support contractpowerv1.Support

	clientMutex sync.RWMutex
	client      *client
}

func New(session Session, config Config, logger *slog.Logger) (*Adapter, error) {
	if session == nil {
		return nil, errors.New("home assistant adapter Session is required")
	}
	if _, err := websocketAddress(config.URL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("home assistant token is required")
	}
	if config.ExternalEntityID == "" {
		return nil, errors.New("home assistant Entity ID is required")
	}
	if config.EntityID == "" {
		return nil, errors.New("hearth Entity ID is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", adapterComponent)
	return &Adapter{
		session: session,
		config:  config,
		logger:  logger,
		support: PowerSupport(),
	}, nil
}

func PowerSupport() contractpowerv1.Support {
	return contractpowerv1.Support{
		State: contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{
			Set: contractpowerv1.SetSupport{},
		},
	}
}

func (homeAssistant *Adapter) Support() contractpowerv1.Support { return homeAssistant.support }

func (homeAssistant *Adapter) CommandHandler() (adapter.CommandHandler, error) {
	return sdkpowerv1.NewCommandHandler(homeAssistant.config.EntityID, homeAssistant.support, sdkpowerv1.Handlers{
		Set: homeAssistant.set,
	})
}

func (homeAssistant *Adapter) Run(ctx context.Context) error {
	delay := reconnectMinimum
	var episode retryEpisode
	for {
		err := homeAssistant.runConnection(ctx, &episode)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // Context cancellation is a graceful shutdown.
		}
		if _, ok := errors.AsType[*sessionOperationError](err); ok {
			return err
		}
		if healthErr := homeAssistant.reportUnhealthy(ctx, err); healthErr != nil {
			return healthErr
		}
		if _, ok := errors.AsType[*AuthenticationError](err); ok {
			return err
		}
		wait := jitter(delay)
		homeAssistant.logConnectionRetry(ctx, &episode, err, wait)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay *= 2
		if delay > reconnectMaximum {
			delay = reconnectMaximum
		}
	}
}

func (homeAssistant *Adapter) runConnection(ctx context.Context, episode *retryEpisode) error {
	client, err := dialClient(
		ctx,
		homeAssistant.config.URL,
		homeAssistant.config.Token,
		homeAssistant.config.ExternalEntityID,
	)
	if err != nil {
		return err
	}
	defer client.Close()
	if subscribeErr := client.SubscribeStateChanges(ctx); subscribeErr != nil {
		return subscribeErr
	}
	return homeAssistant.reconcileAndStream(ctx, client, episode)
}

type snapshotResult struct {
	states      []upstreamState
	receivedAt  time.Time
	priorEvents int64
	err         error
}

//nolint:gocognit // Reconciliation is an explicit state machine whose branches mirror upstream events.
func (homeAssistant *Adapter) reconcileAndStream(
	ctx context.Context,
	client *client,
	episode *retryEpisode,
) error {
	resultChannel := make(chan snapshotResult, 1)
	go func() {
		states, receivedAt, priorEvents, err := client.GetStates(ctx)
		resultChannel <- snapshotResult{states: states, receivedAt: receivedAt, priorEvents: priorEvents, err: err}
	}()

	var buffered []stateChange
	var snapshot snapshotResult
	for {
		select {
		case event := <-client.Events():
			buffered = append(buffered, event)
		case snapshot = <-resultChannel:
			if snapshot.err != nil {
				return snapshot.err
			}
			goto snapshotReady
		case <-client.Done():
			return client.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

snapshotReady:
	for int64(len(buffered)) < snapshot.priorEvents {
		select {
		case event := <-client.Events():
			buffered = append(buffered, event)
		case <-client.Done():
			return client.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state, found := findState(snapshot.states, homeAssistant.config.ExternalEntityID)
	if !found {
		return fmt.Errorf(
			"configured Home Assistant Entity %q was absent from get_states",
			homeAssistant.config.ExternalEntityID,
		)
	}
	reconciled := []stateChange{{State: state, ReceivedAt: snapshot.receivedAt}}
	snapshotUpdatedAt, _ := sourceUpdatedAt(state)
	for _, event := range buffered {
		eventUpdatedAt, err := sourceUpdatedAt(event.State)
		if err != nil || snapshotUpdatedAt == nil || eventUpdatedAt == nil ||
			!eventUpdatedAt.After(*snapshotUpdatedAt) {
			continue
		}
		reconciled = append(reconciled, event)
	}
	homeAssistant.setClient(client)
	defer homeAssistant.clearClient(client)
	if err := homeAssistant.setHealthy(ctx); err != nil {
		return err
	}
	for index, item := range reconciled {
		source := "event"
		if index == 0 {
			source = "snapshot"
		}
		if err := homeAssistant.processState(ctx, item.State, item.ReceivedAt); err != nil {
			if errors.Is(err, errUnsupportedState) {
				homeAssistant.logUnsupportedState(ctx, source)
				continue
			}
			return err
		}
	}
	homeAssistant.logUpstreamReady(ctx, episode)

	for {
		select {
		case event := <-client.Events():
			if err := homeAssistant.processState(ctx, event.State, event.ReceivedAt); err != nil {
				if errors.Is(err, errUnsupportedState) {
					homeAssistant.logUnsupportedState(ctx, "event")
					continue
				}
				return err
			}
		case <-client.Done():
			return client.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (homeAssistant *Adapter) logUnsupportedState(ctx context.Context, source string) {
	homeAssistant.logger.WarnContext(
		ctx,
		"Home Assistant "+source+" State is not publishable",
		eventKey, "adapter.unsupported_state",
		"entity_id", homeAssistant.config.EntityID,
		"error_code", "unsupported_state",
		"source", source,
	)
}

//nolint:gocognit // Command refresh distinguishes resource, external-system, and protocol failures before responding.
func (homeAssistant *Adapter) set(
	ctx context.Context,
	command typed.Command[contractpowerv1.SetParameters],
	responder adapter.Responder,
) error {
	client := homeAssistant.currentClient()
	if client == nil {
		return responder.Reject("Home Assistant is unavailable")
	}
	eventSequence := client.EventSequence()
	service := "turn_off"
	desiredState := stateOff
	if command.Parameters.Value {
		service = "turn_on"
		desiredState = "on"
	}
	serviceErr := client.CallLightService(ctx, service, homeAssistant.config.ExternalEntityID)
	if serviceErr != nil {
		homeAssistant.logger.WarnContext(
			ctx,
			"Home Assistant service call failed",
			eventKey, "adapter.service_call_failed",
			"entity_id", homeAssistant.config.EntityID,
			"error_code", homeAssistantErrorCode(serviceErr),
		)
	}
	state, receivedAt, stateErr := homeAssistant.getState(ctx, client)
	if stateErr != nil {
		client.Close()
		if healthErr := homeAssistant.reportClientUnhealthy(ctx, client, stateErr); healthErr != nil {
			return healthErr
		}
		return responder.Reject("Home Assistant is unavailable")
	}
	available, availabilityErr := homeAssistant.reportStateAvailability(ctx, state, receivedAt)
	if availabilityErr != nil && !errors.Is(availabilityErr, errUnsupportedState) {
		return availabilityErr
	}
	if !available && availabilityErr == nil {
		return responder.RejectUnavailable("Home Assistant reported the Entity unavailable")
	}
	if serviceErr != nil {
		if _, ok := errors.AsType[*requestRejectedError](serviceErr); !ok {
			client.Close()
			if healthErr := homeAssistant.reportClientUnhealthy(ctx, client, serviceErr); healthErr != nil {
				return healthErr
			}
		}
		return responder.Reject("Home Assistant rejected the light command")
	}
	evidence, err := responder.Accept()
	if err != nil {
		return err
	}
	if state.State == desiredState {
		return homeAssistant.publishEvidence(ctx, evidence, state, receivedAt)
	}
	if available {
		if publishErr := homeAssistant.publishEvidence(ctx, evidence, state, receivedAt); publishErr != nil {
			return publishErr
		}
	}
	matching, err := client.WaitForStateAfter(ctx, eventSequence, desiredState)
	if err != nil {
		return err
	}
	return homeAssistant.publishEvidence(ctx, evidence, matching.State, matching.ReceivedAt)
}

func (homeAssistant *Adapter) getState(ctx context.Context, client *client) (upstreamState, time.Time, error) {
	states, receivedAt, _, err := client.GetStates(ctx)
	if err != nil {
		return upstreamState{}, time.Time{}, err
	}
	state, found := findState(states, homeAssistant.config.ExternalEntityID)
	if !found {
		return upstreamState{}, time.Time{}, fmt.Errorf(
			"configured Home Assistant Entity %q was absent from command refresh",
			homeAssistant.config.ExternalEntityID,
		)
	}
	return state, receivedAt, nil
}

var errUnsupportedState = errors.New("unsupported Home Assistant State")

type sessionOperationError struct {
	operation string
	err       error
}

func (operationError *sessionOperationError) Error() string {
	return operationError.operation + ": " + operationError.err.Error()
}

func (operationError *sessionOperationError) Unwrap() error { return operationError.err }

func (homeAssistant *Adapter) setHealthy(ctx context.Context) error {
	err := homeAssistant.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	})
	if err != nil {
		return &sessionOperationError{operation: "report healthy Home Assistant", err: err}
	}
	return nil
}

func (homeAssistant *Adapter) reportUnhealthy(ctx context.Context, cause error) error {
	reason := externalSystemUnavailableReason
	if _, ok := errors.AsType[*AuthenticationError](cause); ok {
		reason = authenticationFailedReason
	}
	err := homeAssistant.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthUnhealthy, SourceObservedAt: time.Now().UTC(), ReasonCode: reason,
	})
	if err != nil {
		return &sessionOperationError{operation: "report unhealthy Home Assistant", err: err}
	}
	return nil
}

func (homeAssistant *Adapter) reportClientUnhealthy(ctx context.Context, expected *client, cause error) error {
	homeAssistant.clientMutex.Lock()
	defer homeAssistant.clientMutex.Unlock()
	if homeAssistant.client != expected {
		return nil
	}
	return homeAssistant.reportUnhealthy(ctx, cause)
}

func (homeAssistant *Adapter) processState(
	ctx context.Context,
	state upstreamState,
	receivedAt time.Time,
) error {
	available, err := homeAssistant.reportStateAvailability(ctx, state, receivedAt)
	if err != nil || !available {
		return err
	}
	return homeAssistant.publish(ctx, state, receivedAt)
}

func (homeAssistant *Adapter) reportStateAvailability(
	ctx context.Context,
	state upstreamState,
	receivedAt time.Time,
) (bool, error) {
	report := adapter.EntityAvailabilityReport{
		EntityID: homeAssistant.config.EntityID, SourceObservedAt: receivedAt.UTC(),
	}
	if report.SourceObservedAt.IsZero() {
		report.SourceObservedAt = time.Now().UTC()
	}
	switch state.State {
	case "on", stateOff:
		report.Status = adapter.AvailabilityAvailable
	case stateUnavailable, stateUnknown:
		report.Status = adapter.AvailabilityUnavailable
		report.ReasonCode = entityUnavailableReason
	default:
		return false, fmt.Errorf("%w %q", errUnsupportedState, state.State)
	}
	if err := homeAssistant.session.ReportEntityAvailability(
		ctx,
		[]adapter.EntityAvailabilityReport{report},
	); err != nil {
		return false, &sessionOperationError{
			operation: "report Home Assistant Entity availability",
			err:       err,
		}
	}
	return report.Status == adapter.AvailabilityAvailable, nil
}

func (homeAssistant *Adapter) publish(ctx context.Context, state upstreamState, receivedAt time.Time) error {
	observation, err := homeAssistant.newObservation(ctx, state, receivedAt)
	if err != nil {
		return err
	}
	if _, err = homeAssistant.session.PublishObservation(ctx, observation); err != nil {
		return &sessionOperationError{operation: "publish Home Assistant Observation", err: err}
	}
	return nil
}

func (homeAssistant *Adapter) publishEvidence(
	ctx context.Context,
	evidence adapter.CommandEvidence,
	state upstreamState,
	receivedAt time.Time,
) error {
	observation, err := homeAssistant.newObservation(ctx, state, receivedAt)
	if err != nil {
		return err
	}
	if _, err = evidence.PublishObservation(ctx, observation); err != nil {
		return &sessionOperationError{operation: "publish Home Assistant Command evidence", err: err}
	}
	return nil
}

func (homeAssistant *Adapter) newObservation(
	ctx context.Context,
	state upstreamState,
	receivedAt time.Time,
) (adapter.Observation, error) {
	var value contractpowerv1.State
	switch state.State {
	case "on":
		value = true
	case stateOff:
		value = false
	default:
		return adapter.Observation{}, fmt.Errorf("%w %q", errUnsupportedState, state.State)
	}
	updatedAt, err := sourceUpdatedAt(state)
	if err != nil {
		homeAssistant.logger.WarnContext(
			ctx,
			"Home Assistant State has invalid last_updated",
			eventKey, "adapter.invalid_source_timestamp",
			"entity_id", homeAssistant.config.EntityID,
			"error_code", "invalid_timestamp",
		)
		updatedAt = nil
	}
	return sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
		EntityID:          homeAssistant.config.EntityID,
		Support:           homeAssistant.support,
		State:             value,
		AdapterReceivedAt: receivedAt,
		SourceUpdatedAt:   updatedAt,
	})
}

func sourceUpdatedAt(state upstreamState) (*time.Time, error) {
	if state.LastUpdated == "" {
		return nil, nil //nolint:nilnil // Missing upstream timestamps are represented by nil.
	}
	value, err := time.Parse(time.RFC3339Nano, state.LastUpdated)
	if err != nil {
		return nil, fmt.Errorf("parse Home Assistant last_updated: %w", err)
	}
	value = value.UTC()
	return &value, nil
}

func findState(states []upstreamState, entityID string) (upstreamState, bool) {
	for _, state := range states {
		if state.EntityID == entityID {
			return state, true
		}
	}
	return upstreamState{}, false
}

func (homeAssistant *Adapter) setClient(client *client) {
	homeAssistant.clientMutex.Lock()
	homeAssistant.client = client
	homeAssistant.clientMutex.Unlock()
}

func (homeAssistant *Adapter) clearClient(expected *client) {
	homeAssistant.clientMutex.Lock()
	if homeAssistant.client == expected {
		homeAssistant.client = nil
	}
	homeAssistant.clientMutex.Unlock()
}

func (homeAssistant *Adapter) currentClient() *client {
	homeAssistant.clientMutex.RLock()
	defer homeAssistant.clientMutex.RUnlock()
	return homeAssistant.client
}

func jitter(delay time.Duration) time.Duration {
	half := delay / jitterDivisor
	if half <= 0 {
		return delay
	}
	//nolint:gosec // Backoff jitter does not require cryptographic randomness.
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}
