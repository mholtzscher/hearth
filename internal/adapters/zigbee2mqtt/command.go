package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

type commandRoute struct {
	entityID             string
	ieeeAddress          string
	friendlyName         string
	entity               discoveredEntity
	connectionGeneration uint64
	routeGeneration      uint64
}

type desiredState struct {
	power      bool
	brightness int64
}

type commandMatcher struct {
	entityID             string
	property             string
	connectionGeneration uint64
	desired              desiredState
	dispatchedAt         time.Time
	claimed              bool
	matched              chan matchedState
	canceled             chan struct{}
}

type matchedState struct {
	state      decodedEntityState
	receivedAt time.Time
}

type contextLock struct {
	semaphore chan struct{}
}

func newContextLock() *contextLock { return &contextLock{semaphore: make(chan struct{}, 1)} }

func (lock *contextLock) acquire(ctx context.Context) error {
	select {
	case lock.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (lock *contextLock) release() { <-lock.semaphore }

func (z2m *Adapter) HandleCommand(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
	z2m.mutex.Lock()
	route, exists := z2m.routes[command.EntityID]
	healthy := z2m.healthy
	z2m.mutex.Unlock()
	if !exists || !healthy {
		return responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
	}

	switch route.entity.Kind {
	case entityKindPower:
		handler, err := sdkpowerv1.NewCommandHandler(route.entityID, powerSupport(), sdkpowerv1.Handlers{
			Set: func(ctx context.Context, typedCommand typed.Command[contractpowerv1.SetParameters], responder adapter.Responder) error {
				value, valueErr := powerCommandValue(route.entity, typedCommand.Parameters.Value)
				if valueErr != nil {
					return valueErr
				}
				return z2m.dispatchCommand(
					ctx,
					route,
					typedCommand.ID,
					value,
					desiredState{power: typedCommand.Parameters.Value},
					responder,
				)
			},
		})
		if err != nil {
			return err
		}
		return handler(ctx, command, responder)
	case entityKindBrightness:
		handler, err := sdkbrightnessv1.NewCommandHandler(route.entityID, brightnessSupport(), sdkbrightnessv1.Handlers{
			Set: func(ctx context.Context, typedCommand typed.Command[contractbrightnessv1.SetParameters], responder adapter.Responder) error {
				value, valueErr := brightnessCommandValue(route.entity, typedCommand.Parameters.Value)
				if valueErr != nil {
					return valueErr
				}
				return z2m.dispatchCommand(
					ctx,
					route,
					typedCommand.ID,
					value,
					desiredState{brightness: typedCommand.Parameters.Value},
					responder,
				)
			},
		})
		if err != nil {
			return err
		}
		return handler(ctx, command, responder)
	default:
		return responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
	}
}

//nolint:funlen // Dispatch keeps matcher installation, MQTT acknowledgement, and linked evidence in one lifecycle.
func (z2m *Adapter) dispatchCommand(
	ctx context.Context,
	initial commandRoute,
	commandID string,
	value json.RawMessage,
	desired desiredState,
	responder adapter.Responder,
) error {
	lock := z2m.lockForDevice(initial.ieeeAddress)
	if err := lock.acquire(ctx); err != nil {
		return err
	}
	defer lock.release()

	z2m.routeLifecycle.RLock()
	z2m.mutex.Lock()
	route, exists := z2m.routes[initial.entityID]
	if !exists || !z2m.healthy || route.routeGeneration != initial.routeGeneration ||
		route.ieeeAddress != initial.ieeeAddress {
		z2m.mutex.Unlock()
		z2m.routeLifecycle.RUnlock()
		return responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
	}
	connection := z2m.connection
	if connection == nil {
		z2m.mutex.Unlock()
		z2m.routeLifecycle.RUnlock()
		return responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
	}
	payload, err := json.Marshal(map[string]json.RawMessage{route.entity.Property: value})
	if err != nil {
		z2m.mutex.Unlock()
		z2m.routeLifecycle.RUnlock()
		return err
	}
	matcher := &commandMatcher{
		entityID: route.entityID, property: route.entity.Property,
		connectionGeneration: route.connectionGeneration, desired: desired,
		matched: make(chan matchedState, 1), canceled: make(chan struct{}),
	}
	if _, duplicate := z2m.matchers[route.entityID]; duplicate {
		z2m.mutex.Unlock()
		z2m.routeLifecycle.RUnlock()
		return errors.New("Zigbee2MQTT command matcher already exists")
	}
	z2m.matchers[route.entityID] = matcher
	matcher.dispatchedAt = time.Now().UTC()
	z2m.mutex.Unlock()

	err = connection.Publish(
		ctx,
		z2m.config.BaseTopic+"/"+route.friendlyName+"/set",
		mqttQoS,
		false,
		payload,
	)
	z2m.routeLifecycle.RUnlock()
	if err != nil {
		z2m.releaseClaimedMatch(ctx, matcher)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			z2m.failConnection(route.connectionGeneration, err)
		}
		return responder.RejectUnavailable("Zigbee2MQTT broker is unavailable")
	}
	if err = responder.Accept(); err != nil {
		z2m.releaseClaimedMatch(ctx, matcher)
		return err
	}
	if getErr := publishGet(
		ctx,
		connection,
		z2m.config.BaseTopic,
		route.friendlyName,
		route.entity.Property,
	); getErr != nil {
		z2m.logger.WarnContext(
			ctx,
			"Zigbee2MQTT command refresh publication failed",
			"entity_id",
			route.entityID,
			"error",
			getErr,
		)
	}

	select {
	case match := <-matcher.matched:
		z2m.removeMatcher(matcher)
		select {
		case <-matcher.canceled:
			z2m.publishOrdinaryMatch(ctx, matcher.entityID, match)
			return nil
		default:
		}
		observation, observationErr := newObservation(route.entityID, match.state, match.receivedAt, &commandID)
		if observationErr != nil {
			return observationErr
		}
		if _, observationErr = z2m.session.PublishObservation(ctx, observation); observationErr != nil {
			return &sessionOperationError{
				operation: "publish command-linked Zigbee2MQTT Observation",
				err:       observationErr,
			}
		}
		return nil
	case <-matcher.canceled:
		z2m.releaseClaimedMatch(ctx, matcher)
		return nil
	case <-ctx.Done():
		z2m.releaseClaimedMatch(ctx, matcher)
		return ctx.Err()
	}
}

func (z2m *Adapter) lockForDevice(ieeeAddress string) *contextLock {
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	lock := z2m.deviceLocks[ieeeAddress]
	if lock == nil {
		lock = newContextLock()
		z2m.deviceLocks[ieeeAddress] = lock
	}
	return lock
}

func (z2m *Adapter) removeMatcher(expected *commandMatcher) {
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	if z2m.matchers[expected.entityID] == expected {
		delete(z2m.matchers, expected.entityID)
	}
}

func (z2m *Adapter) claimMatcher(
	generation uint64,
	entityID string,
	state decodedEntityState,
	retained bool,
	receivedAt time.Time,
) bool {
	if retained {
		return false
	}
	z2m.mutex.Lock()
	defer z2m.mutex.Unlock()
	matcher := z2m.matchers[entityID]
	if matcher == nil || matcher.claimed || matcher.dispatchedAt.IsZero() ||
		matcher.connectionGeneration != generation || matcher.property != state.Entity.Property ||
		!matcherMatches(matcher, state) || !receivedAt.After(matcher.dispatchedAt) {
		return false
	}
	matcher.claimed = true
	matcher.matched <- matchedState{state: state, receivedAt: receivedAt}
	return true
}

func (z2m *Adapter) releaseClaimedMatch(ctx context.Context, matcher *commandMatcher) {
	z2m.removeMatcher(matcher)
	select {
	case match := <-matcher.matched:
		z2m.publishOrdinaryMatch(ctx, matcher.entityID, match)
	default:
	}
}

func (z2m *Adapter) publishOrdinaryMatch(ctx context.Context, entityID string, match matchedState) {
	observation, err := newObservation(entityID, match.state, match.receivedAt, nil)
	if err == nil {
		_, err = z2m.session.PublishObservation(ctx, observation)
	}
	if err != nil {
		z2m.logger.WarnContext(ctx, "failed to publish held Zigbee2MQTT State", "error", err)
	}
}

func matcherMatches(matcher *commandMatcher, state decodedEntityState) bool {
	switch state.Entity.Kind {
	case entityKindPower:
		return state.Power == matcher.desired.power
	case entityKindBrightness:
		return state.Brightness == matcher.desired.brightness
	default:
		return false
	}
}

func (z2m *Adapter) failConnection(generation uint64, err error) {
	z2m.routeLifecycle.Lock()
	defer z2m.routeLifecycle.Unlock()
	z2m.mutex.Lock()
	if z2m.generation != generation {
		z2m.mutex.Unlock()
		return
	}
	cause := fmt.Errorf("publish Zigbee2MQTT command: %w", err)
	connection := z2m.connection
	cancel := z2m.connectionCancel
	z2m.healthy = false
	z2m.routes = make(map[string]commandRoute)
	z2m.devices = make(map[string]runtimeDevice)
	z2m.cancelMatchersLocked()
	z2m.mutex.Unlock()
	if cancel != nil {
		cancel(cause)
	}
	if connection != nil {
		connection.Close()
	}
}
