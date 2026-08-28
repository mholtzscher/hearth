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
	reconnectMinimum = 250 * time.Millisecond
	reconnectMaximum = 5 * time.Second
)

type ObservationPublisher interface {
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Config struct {
	URL              string
	Token            string
	ExternalEntityID string
	EntityID         string
}

type Adapter struct {
	publisher ObservationPublisher
	config    Config
	logger    *slog.Logger
	support   contractpowerv1.Support

	clientMutex sync.RWMutex
	client      *client
}

func New(publisher ObservationPublisher, config Config, logger *slog.Logger) (*Adapter, error) {
	if publisher == nil {
		return nil, errors.New("home assistant observation publisher is required")
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
	return &Adapter{
		publisher: publisher,
		config:    config,
		logger:    logger,
		support:   PowerSupport(),
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
	for {
		err := homeAssistant.runConnection(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if _, ok := errors.AsType[*AuthenticationError](err); ok {
			return err
		}
		wait := jitter(delay)
		homeAssistant.logger.WarnContext(
			ctx,
			"Home Assistant connection ended; reconnecting",
			"error",
			err,
			"retry_in",
			wait,
		)
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

func (homeAssistant *Adapter) runConnection(ctx context.Context) error {
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
	homeAssistant.setClient(client)
	defer homeAssistant.clearClient(client)
	return homeAssistant.reconcileAndStream(ctx, client)
}

type snapshotResult struct {
	states      []upstreamState
	receivedAt  time.Time
	priorEvents int64
	err         error
}

//nolint:gocognit // Reconciliation is an explicit state machine whose branches mirror upstream events.
func (homeAssistant *Adapter) reconcileAndStream(ctx context.Context, client *client) error {
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
	snapshotUpdatedAt, _ := sourceUpdatedAt(state)
	if err := homeAssistant.publish(ctx, state, snapshot.receivedAt, nil); err != nil {
		if !errors.Is(err, errUnsupportedState) {
			return err
		}
		homeAssistant.logUnsupportedState(ctx, "snapshot", state)
	}
	for _, event := range buffered {
		eventUpdatedAt, err := sourceUpdatedAt(event.State)
		if err != nil || snapshotUpdatedAt == nil || eventUpdatedAt == nil ||
			!eventUpdatedAt.After(*snapshotUpdatedAt) {
			continue
		}
		if publishErr := homeAssistant.publish(ctx, event.State, event.ReceivedAt, nil); publishErr != nil {
			if errors.Is(publishErr, errUnsupportedState) {
				homeAssistant.logUnsupportedState(ctx, "event", event.State)
				continue
			}
			return publishErr
		}
	}

	for {
		select {
		case event := <-client.Events():
			if err := homeAssistant.publish(ctx, event.State, event.ReceivedAt, nil); err != nil {
				if errors.Is(err, errUnsupportedState) {
					homeAssistant.logger.WarnContext(
						ctx,
						"Home Assistant event State is not publishable",
						"entity_id",
						event.State.EntityID,
						"state",
						event.State.State,
					)
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

func (homeAssistant *Adapter) logUnsupportedState(ctx context.Context, source string, state upstreamState) {
	homeAssistant.logger.WarnContext(
		ctx,
		"Home Assistant "+source+" State is not publishable",
		"entity_id", state.EntityID,
		"state", state.State,
	)
}

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
	desiredState := "off"
	if command.Parameters.Value {
		service = "turn_on"
		desiredState = "on"
	}
	if err := client.CallLightService(ctx, service, homeAssistant.config.ExternalEntityID); err != nil {
		homeAssistant.logger.WarnContext(
			ctx,
			"Home Assistant service call failed",
			"entity_id",
			homeAssistant.config.ExternalEntityID,
			"error",
			err,
		)
		return responder.Reject("Home Assistant rejected the light command")
	}
	if err := responder.Accept(); err != nil {
		return err
	}
	commandID := command.ID
	state, receivedAt, err := homeAssistant.getState(ctx, client)
	if err != nil {
		return err
	}
	if state.State == desiredState {
		return homeAssistant.publish(ctx, state, receivedAt, &commandID)
	}
	if state.State == "on" || state.State == "off" {
		if publishErr := homeAssistant.publish(ctx, state, receivedAt, &commandID); publishErr != nil {
			return publishErr
		}
	}
	matching, err := client.WaitForStateAfter(ctx, eventSequence, desiredState)
	if err != nil {
		return err
	}
	return homeAssistant.publish(ctx, matching.State, matching.ReceivedAt, &commandID)
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

func (homeAssistant *Adapter) publish(
	ctx context.Context,
	state upstreamState,
	receivedAt time.Time,
	refreshForCommand *string,
) error {
	var value contractpowerv1.State
	switch state.State {
	case "on":
		value = true
	case "off":
		value = false
	default:
		return fmt.Errorf("%w %q", errUnsupportedState, state.State)
	}
	updatedAt, err := sourceUpdatedAt(state)
	if err != nil {
		homeAssistant.logger.WarnContext(
			ctx,
			"Home Assistant State has invalid last_updated",
			"entity_id",
			state.EntityID,
			"error",
			err,
		)
		updatedAt = nil
	}
	observation, err := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
		EntityID:          homeAssistant.config.EntityID,
		Support:           homeAssistant.support,
		State:             value,
		AdapterReceivedAt: receivedAt,
		SourceUpdatedAt:   updatedAt,
		RefreshForCommand: refreshForCommand,
	})
	if err != nil {
		return err
	}
	_, err = homeAssistant.publisher.PublishObservation(ctx, observation)
	return err
}

func sourceUpdatedAt(state upstreamState) (*time.Time, error) {
	if state.LastUpdated == "" {
		return nil, nil
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
	half := delay / 2
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}
