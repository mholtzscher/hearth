package zigbee2mqtt

import (
	"context"
	"errors"
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
	report        stateReport
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

type eventCandidate struct {
	ctx    context.Context
	event  adapter.EntityEvent
	result chan struct{}
}

func (eventCandidate) runtimeEvent() {}

type ordinaryEventPublishFinished struct {
	result chan struct{}
	err    error
}

func (ordinaryEventPublishFinished) runtimeEvent() {}

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
	matches      func(stateReport) bool
	refresh      []string
	deadline     time.Time
	dispatchedAt time.Time

	phase         commandPhase
	evidence      adapter.CommandEvidence
	claimed       *matchedState
	deadlineTimer *time.Timer

	mqttContext context.Context
	cancelMQTT  context.CancelFunc
}

type matchedState struct {
	report     stateReport
	receivedAt time.Time
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
	case eventCandidate:
		coordinator.startOrdinaryEvent(event)
	case ordinaryEventPublishFinished:
		event.result <- struct{}{}
		if event.err != nil && !errors.Is(event.err, context.Canceled) {
			return &sessionOperationError{operation: "publish Zigbee2MQTT Entity Event", err: event.err}
		}
	case attemptDeadlineReached:
		coordinator.reachDeadline(event.attemptID)
	}
	return nil
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
