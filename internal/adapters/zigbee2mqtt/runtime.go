package zigbee2mqtt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type runtimeEvent interface{ runtimeEvent() }

type commandSubmitted struct {
	ctx       context.Context
	command   adapter.Command
	responder adapter.Responder
	result    chan error
}

func (commandSubmitted) runtimeEvent() {}

type routeActivationResult struct {
	revision uint64
	err      error
}

type routesActivated struct {
	generation uint64
	connection mqttConnection
	disconnect context.CancelCauseFunc
	snapshot   routeSnapshot
	result     chan routeActivationResult
}

func (routesActivated) runtimeEvent() {}

type routesInvalidated struct {
	generation uint64
	cause      error
	result     chan error
}

func (routesInvalidated) runtimeEvent() {}

type stateCandidate struct {
	ctx           context.Context
	generation    uint64
	routeRevision uint64
	entityID      string
	state         decodedEntityState
	retained      bool
	receivedAt    time.Time
	result        chan stateDisposition
}

func (stateCandidate) runtimeEvent() {}

type setPublishFinished struct {
	attemptID uint64
	err       error
}

func (setPublishFinished) runtimeEvent() {}

type getPublishFinished struct {
	attemptID uint64
	err       error
}

func (getPublishFinished) runtimeEvent() {}

type linkedPublishFinished struct {
	attemptID uint64
	err       error
}

func (linkedPublishFinished) runtimeEvent() {}

type fallbackPublishFinished struct {
	attemptID uint64
	err       error
}

func (fallbackPublishFinished) runtimeEvent() {}

type ordinaryPublishFinished struct {
	result chan stateDisposition
	err    error
}

func (ordinaryPublishFinished) runtimeEvent() {}

type attemptDeadlineReached struct{ attemptID uint64 }

func (attemptDeadlineReached) runtimeEvent() {}

type stateDisposition uint8

const (
	stateOrdinary stateDisposition = iota
	stateClaimed
)

type runtimeCoordinator struct {
	adapter *Adapter
	ctx     context.Context
	cancel  context.CancelFunc

	generation    uint64
	routeRevision uint64
	connection    mqttConnection
	disconnect    context.CancelCauseFunc
	dispatchable  bool

	routes        map[string]commandRoute
	deviceQueues  map[string]*deviceCommandQueue
	matchers      map[string]*commandAttempt
	attempts      map[uint64]*commandAttempt
	nextAttemptID uint64

	effects sync.WaitGroup
}

type deviceCommandQueue struct {
	active *commandAttempt
	queued []*commandAttempt
}

type commandAttempt struct {
	id            uint64
	generation    uint64
	routeRevision uint64
	command       adapter.Command
	responder     adapter.Responder
	handlerResult chan error
	handlerDone   bool

	route        commandRoute
	payload      []byte
	desired      desiredState
	deadline     time.Time
	dispatchedAt time.Time

	phase         commandPhase
	evidence      adapter.CommandEvidence
	claimed       *matchedState
	deadlineTimer *time.Timer

	mqttContext context.Context
	cancelMQTT  context.CancelFunc
}

type commandPhase uint8

const (
	commandQueued commandPhase = iota
	commandPublishingSet
	commandAwaitingEvidence
	commandPublishingLinked
	commandPublishingFallback
	commandTerminal
)

var errStaleRuntimeGeneration = errors.New("stale Zigbee2MQTT runtime generation")

func newRuntimeCoordinator(ctx context.Context, z2m *Adapter) *runtimeCoordinator {
	runtimeContext, cancel := context.WithCancel(ctx)
	return &runtimeCoordinator{
		adapter:      z2m,
		ctx:          runtimeContext,
		cancel:       cancel,
		routes:       make(map[string]commandRoute),
		deviceQueues: make(map[string]*deviceCommandQueue),
		matchers:     make(map[string]*commandAttempt),
		attempts:     make(map[uint64]*commandAttempt),
	}
}

func (coordinator *runtimeCoordinator) run() error {
	for {
		select {
		case <-coordinator.ctx.Done():
			return coordinator.stop(coordinator.ctx.Err())
		case event := <-coordinator.adapter.runtimeEvents:
			if err := coordinator.handle(event); err != nil {
				return coordinator.stop(err)
			}
		}
	}
}

func (coordinator *runtimeCoordinator) stop(err error) error {
	coordinator.cancel()
	coordinator.shutdown(err)
	close(coordinator.adapter.runtimeDone)
	return err
}

func (coordinator *runtimeCoordinator) handle(event runtimeEvent) error {
	switch event := event.(type) {
	case commandSubmitted:
		coordinator.submitCommand(event)
	case routesActivated:
		coordinator.activateRoutes(event)
	case routesInvalidated:
		coordinator.invalidateRoutes(event)
	case stateCandidate:
		return coordinator.handleState(event)
	case setPublishFinished:
		return coordinator.finishSet(event)
	case getPublishFinished:
		coordinator.finishGet(event)
	case linkedPublishFinished:
		return coordinator.finishLinked(event)
	case fallbackPublishFinished:
		return coordinator.finishFallback(event)
	case ordinaryPublishFinished:
		event.result <- stateOrdinary
		if event.err != nil && !errors.Is(event.err, context.Canceled) {
			return &sessionOperationError{operation: "publish Zigbee2MQTT Observation", err: event.err}
		}
	case attemptDeadlineReached:
		coordinator.reachDeadline(event.attemptID)
	}
	return nil
}

func (coordinator *runtimeCoordinator) submitCommand(event commandSubmitted) {
	if err := event.ctx.Err(); err != nil {
		event.result <- err
		return
	}
	route, exists := coordinator.routes[event.command.EntityID]
	if !coordinator.dispatchable || !exists {
		event.result <- event.responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
		return
	}
	payload, desired, deadline, err := translateCommand(event.ctx, route, event.command, event.responder)
	if err != nil {
		event.result <- err
		return
	}
	coordinator.nextAttemptID++
	mqttContext, cancelMQTT := context.WithDeadline(coordinator.ctx, deadline)
	attempt := &commandAttempt{
		id:            coordinator.nextAttemptID,
		generation:    coordinator.generation,
		routeRevision: coordinator.routeRevision,
		command:       event.command,
		responder:     event.responder,
		handlerResult: event.result,
		route:         route,
		payload:       payload,
		desired:       desired,
		deadline:      deadline,
		phase:         commandQueued,
		mqttContext:   mqttContext,
		cancelMQTT:    cancelMQTT,
	}
	attempt.deadlineTimer = time.AfterFunc(max(time.Until(deadline), 0), func() {
		coordinator.sendCompletion(attemptDeadlineReached{attemptID: attempt.id})
	})
	coordinator.attempts[attempt.id] = attempt
	queue := coordinator.deviceQueues[route.ieeeAddress]
	if queue == nil {
		queue = &deviceCommandQueue{}
		coordinator.deviceQueues[route.ieeeAddress] = queue
	}
	queue.queued = append(queue.queued, attempt)
	coordinator.startNext(route.ieeeAddress)
}

func (coordinator *runtimeCoordinator) startNext(ieeeAddress string) {
	queue := coordinator.deviceQueues[ieeeAddress]
	if queue == nil || queue.active != nil {
		return
	}
	for len(queue.queued) > 0 {
		attempt := queue.queued[0]
		queue.queued = queue.queued[1:]
		if !time.Now().Before(attempt.deadline) || !coordinator.attemptRouteIsCurrent(attempt) {
			if time.Now().Before(attempt.deadline) {
				coordinator.respondUnavailable(attempt)
			}
			coordinator.finishAttempt(attempt, false)
			continue
		}
		queue.active = attempt
		attempt.phase = commandPublishingSet
		attempt.dispatchedAt = time.Now().UTC()
		coordinator.matchers[attempt.command.EntityID] = attempt
		connection := coordinator.connection
		coordinator.startEffect(func() runtimeEvent {
			err := connection.Publish(
				attempt.mqttContext,
				coordinator.adapter.config.BaseTopic+"/"+attempt.route.friendlyName+"/set",
				mqttQoS,
				false,
				attempt.payload,
			)
			return setPublishFinished{attemptID: attempt.id, err: err}
		})
		return
	}
	delete(coordinator.deviceQueues, ieeeAddress)
}

func (coordinator *runtimeCoordinator) finishSet(event setPublishFinished) error {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || attempt.phase != commandPublishingSet {
		return nil
	}
	if !time.Now().Before(attempt.deadline) {
		coordinator.finishHandler(attempt, context.DeadlineExceeded)
		if event.err != nil && !errors.Is(event.err, context.Canceled) &&
			!errors.Is(event.err, context.DeadlineExceeded) {
			disconnect := coordinator.disconnect
			coordinator.dispatchable = false
			coordinator.connection = nil
			coordinator.disconnect = nil
			clear(coordinator.routes)
			if disconnect != nil {
				disconnect(fmt.Errorf("publish Zigbee2MQTT command: %w", event.err))
			}
			coordinator.invalidateAttempts(coordinator.generation, coordinator.routeRevision)
		} else {
			coordinator.finishBeforeLink(attempt)
		}
		return nil
	}
	if event.err != nil {
		if !errors.Is(event.err, context.Canceled) && !errors.Is(event.err, context.DeadlineExceeded) {
			coordinator.respondUnavailable(attempt)
			cause := fmt.Errorf("publish Zigbee2MQTT command: %w", event.err)
			disconnect := coordinator.disconnect
			coordinator.dispatchable = false
			coordinator.connection = nil
			coordinator.disconnect = nil
			clear(coordinator.routes)
			if disconnect != nil {
				disconnect(cause)
			}
			coordinator.invalidateAttempts(coordinator.generation, coordinator.routeRevision)
		} else {
			coordinator.finishHandler(attempt, event.err)
			coordinator.finishBeforeLink(attempt)
		}
		return nil
	}

	evidence, err := attempt.responder.Accept()
	if err != nil {
		coordinator.finishHandler(attempt, err)
		coordinator.finishBeforeLink(attempt)
		return nil
	}
	attempt.evidence = evidence
	attempt.phase = commandAwaitingEvidence
	connection := coordinator.connection
	coordinator.startEffect(func() runtimeEvent {
		getErr := publishGet(
			attempt.mqttContext,
			connection,
			coordinator.adapter.config.BaseTopic,
			attempt.route.friendlyName,
			attempt.route.entity.Property,
		)
		return getPublishFinished{attemptID: attempt.id, err: getErr}
	})
	coordinator.finishHandler(attempt, nil)
	if attempt.claimed != nil {
		coordinator.startLinked(attempt)
	}
	return nil
}

func (coordinator *runtimeCoordinator) finishGet(event getPublishFinished) {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || event.err == nil || errors.Is(event.err, context.Canceled) ||
		errors.Is(event.err, context.DeadlineExceeded) {
		return
	}
	coordinator.adapter.logger.WarnContext(
		coordinator.ctx,
		"Zigbee2MQTT command refresh publication failed",
		"entity_id", attempt.command.EntityID,
		"error", event.err,
	)
}

func (coordinator *runtimeCoordinator) handleState(event stateCandidate) error {
	attempt := coordinator.matchers[event.entityID]
	if attempt == nil || event.retained || event.generation != attempt.generation ||
		event.routeRevision != attempt.routeRevision || attempt.claimed != nil ||
		!time.Now().Before(attempt.deadline) ||
		attempt.phase == commandPublishingLinked || attempt.phase == commandPublishingFallback ||
		attempt.phase == commandTerminal || event.state.Entity.Property != attempt.route.entity.Property ||
		!matcherMatches(attempt.desired, event.state) || !event.receivedAt.After(attempt.dispatchedAt) ||
		event.receivedAt.After(attempt.deadline) {
		coordinator.startOrdinary(event)
		return nil
	}
	attempt.claimed = &matchedState{state: event.state, receivedAt: event.receivedAt}
	event.result <- stateClaimed
	if attempt.evidence != nil {
		coordinator.startLinked(attempt)
	}
	return nil
}

func (coordinator *runtimeCoordinator) startOrdinary(candidate stateCandidate) {
	observation, err := newObservation(candidate.entityID, candidate.state, candidate.receivedAt)
	if err != nil {
		coordinator.sendCompletion(ordinaryPublishFinished{result: candidate.result, err: err})
		return
	}
	coordinator.startEffect(func() runtimeEvent {
		_, publishErr := coordinator.adapter.session.PublishObservation(candidate.ctx, observation)
		return ordinaryPublishFinished{result: candidate.result, err: publishErr}
	})
}

func (coordinator *runtimeCoordinator) startLinked(attempt *commandAttempt) {
	if attempt.phase != commandAwaitingEvidence || attempt.claimed == nil || attempt.evidence == nil {
		return
	}
	observation, err := newObservation(attempt.command.EntityID, attempt.claimed.state, attempt.claimed.receivedAt)
	if err != nil {
		coordinator.finishHandler(attempt, err)
		coordinator.finishBeforeLink(attempt)
		return
	}
	attempt.phase = commandPublishingLinked
	coordinator.startEffect(func() runtimeEvent {
		_, publishErr := attempt.evidence.PublishObservation(coordinator.ctx, observation)
		return linkedPublishFinished{attemptID: attempt.id, err: publishErr}
	})
}

func (coordinator *runtimeCoordinator) finishLinked(event linkedPublishFinished) error {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || attempt.phase != commandPublishingLinked {
		return nil
	}
	if event.err == nil || errors.Is(event.err, context.DeadlineExceeded) || errors.Is(event.err, context.Canceled) {
		coordinator.completeAttempt(attempt)
		return nil
	}
	coordinator.finishAttempt(attempt, false)
	return &sessionOperationError{operation: "publish command-linked Zigbee2MQTT Observation", err: event.err}
}

func (coordinator *runtimeCoordinator) finishFallback(event fallbackPublishFinished) error {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || attempt.phase != commandPublishingFallback {
		return nil
	}
	if event.err == nil || errors.Is(event.err, context.Canceled) {
		coordinator.completeAttempt(attempt)
		return nil
	}
	coordinator.finishAttempt(attempt, false)
	return &sessionOperationError{operation: "publish held Zigbee2MQTT Observation", err: event.err}
}

func (coordinator *runtimeCoordinator) finishBeforeLink(attempt *commandAttempt) {
	if attempt.claimed == nil {
		coordinator.completeAttempt(attempt)
		return
	}
	observation, err := newObservation(attempt.command.EntityID, attempt.claimed.state, attempt.claimed.receivedAt)
	if err != nil {
		coordinator.completeAttempt(attempt)
		return
	}
	attempt.phase = commandPublishingFallback
	coordinator.startEffect(func() runtimeEvent {
		_, publishErr := coordinator.adapter.session.PublishObservation(coordinator.ctx, observation)
		return fallbackPublishFinished{attemptID: attempt.id, err: publishErr}
	})
}

func (coordinator *runtimeCoordinator) reachDeadline(attemptID uint64) {
	attempt := coordinator.attempts[attemptID]
	if attempt == nil || attempt.phase == commandPublishingLinked || attempt.phase == commandPublishingFallback {
		return
	}
	coordinator.finishHandler(attempt, context.DeadlineExceeded)
	coordinator.finishBeforeLink(attempt)
}

func (coordinator *runtimeCoordinator) activateRoutes(event routesActivated) {
	if event.generation < coordinator.generation {
		event.result <- routeActivationResult{err: errStaleRuntimeGeneration}
		return
	}
	coordinator.dispatchable = false
	coordinator.invalidateAttempts(event.generation, 0)
	coordinator.generation = event.generation
	coordinator.routeRevision++
	coordinator.connection = event.connection
	coordinator.disconnect = event.disconnect
	coordinator.routes = make(map[string]commandRoute, len(event.snapshot.routes))
	for entityID, route := range event.snapshot.routes {
		route.connectionGeneration = coordinator.generation
		route.routeGeneration = coordinator.routeRevision
		coordinator.routes[entityID] = route
	}
	coordinator.dispatchable = true
	event.result <- routeActivationResult{revision: coordinator.routeRevision}
}

func (coordinator *runtimeCoordinator) invalidateRoutes(event routesInvalidated) {
	if event.generation < coordinator.generation {
		event.result <- nil
		return
	}
	coordinator.dispatchable = false
	coordinator.invalidateAttempts(event.generation, coordinator.routeRevision)
	coordinator.generation = event.generation
	coordinator.connection = nil
	coordinator.disconnect = nil
	clear(coordinator.routes)
	event.result <- nil
}

func (coordinator *runtimeCoordinator) invalidateAttempts(newGeneration, oldRevision uint64) {
	for _, attempt := range coordinator.attempts {
		if attempt.phase == commandPublishingLinked || attempt.phase == commandPublishingFallback {
			if attempt.cancelMQTT != nil {
				attempt.cancelMQTT()
			}
			continue
		}
		if attempt.generation == newGeneration && oldRevision != 0 && attempt.routeRevision != oldRevision {
			continue
		}
		if attempt.cancelMQTT != nil {
			attempt.cancelMQTT()
		}
		if attempt.evidence == nil && time.Now().Before(attempt.deadline) {
			coordinator.respondUnavailable(attempt)
		}
		coordinator.finishBeforeLink(attempt)
	}
}

func (coordinator *runtimeCoordinator) respondUnavailable(attempt *commandAttempt) {
	if attempt.handlerDone || attempt.evidence != nil {
		return
	}
	coordinator.finishHandler(
		attempt,
		attempt.responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable"),
	)
}

func (coordinator *runtimeCoordinator) finishHandler(attempt *commandAttempt, err error) {
	if attempt.handlerDone {
		return
	}
	attempt.handlerDone = true
	attempt.handlerResult <- err
}

func (coordinator *runtimeCoordinator) completeAttempt(attempt *commandAttempt) {
	coordinator.finishAttempt(attempt, true)
}

//nolint:nestif // Queue removal distinguishes active and queued attempts before optional advancement.
func (coordinator *runtimeCoordinator) finishAttempt(attempt *commandAttempt, advanceQueue bool) {
	if attempt.phase == commandTerminal {
		return
	}
	attempt.phase = commandTerminal
	if attempt.deadlineTimer != nil {
		attempt.deadlineTimer.Stop()
	}
	if attempt.cancelMQTT != nil {
		attempt.cancelMQTT()
	}
	if coordinator.matchers[attempt.command.EntityID] == attempt {
		delete(coordinator.matchers, attempt.command.EntityID)
	}
	delete(coordinator.attempts, attempt.id)
	queue := coordinator.deviceQueues[attempt.route.ieeeAddress]
	if queue != nil {
		if queue.active == attempt {
			queue.active = nil
		} else {
			for index, queued := range queue.queued {
				if queued == attempt {
					queue.queued = append(queue.queued[:index], queue.queued[index+1:]...)
					break
				}
			}
		}
		if advanceQueue {
			coordinator.startNext(attempt.route.ieeeAddress)
		}
	}
}

func (coordinator *runtimeCoordinator) attemptRouteIsCurrent(attempt *commandAttempt) bool {
	if !coordinator.dispatchable || attempt.generation != coordinator.generation ||
		attempt.routeRevision != coordinator.routeRevision {
		return false
	}
	route, exists := coordinator.routes[attempt.command.EntityID]
	return exists && route.ieeeAddress == attempt.route.ieeeAddress &&
		route.entity.Property == attempt.route.entity.Property
}

func (coordinator *runtimeCoordinator) startEffect(effect func() runtimeEvent) {
	coordinator.effects.Go(func() {
		coordinator.sendCompletion(effect())
	})
}

func (coordinator *runtimeCoordinator) sendCompletion(event runtimeEvent) {
	select {
	case coordinator.adapter.runtimeEvents <- event:
	case <-coordinator.ctx.Done():
	case <-coordinator.adapter.runtimeDone:
	}
}

func (coordinator *runtimeCoordinator) shutdown(cause error) {
	coordinator.dispatchable = false
	for _, attempt := range coordinator.attempts {
		if attempt.deadlineTimer != nil {
			attempt.deadlineTimer.Stop()
		}
		if attempt.cancelMQTT != nil {
			attempt.cancelMQTT()
		}
		coordinator.finishHandler(attempt, cause)
	}
	coordinator.effects.Wait()
}

func matcherMatches(desired desiredState, state decodedEntityState) bool {
	switch state.Entity.Kind {
	case entityKindPower:
		return state.Power == desired.power
	case entityKindBrightness:
		return state.Brightness == desired.brightness
	case entityKindColorTemp:
		return state.ColorTemp == desired.colorTemp
	default:
		return false
	}
}
