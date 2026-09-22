// command.go owns Command dispatch: per-node FIFO queues, correlated
// node.set_value publication, acceptance only after a documented successful
// status, and fresh correlated poll evidence through the accepted Command's
// evidence capability.
//
// A poll result is the only linked evidence. A value update Event is an ordinary
// Observation and, at most, a coalesced wake hint. A target value, a supervision
// success, or an emitted post-set update is never proof of the physical result.

package zwavejs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// attemptPhase is the lifecycle stage of one Command attempt.
type attemptPhase uint8

const (
	// phaseQueued is an admitted attempt waiting for its node's FIFO slot.
	phaseQueued attemptPhase = iota
	// phaseSetting is an attempt whose node.set_value is in flight.
	phaseSetting
	// phaseAccepted is an attempt whose Command was accepted and which now
	// awaits linked poll evidence.
	phaseAccepted
	// phaseTerminal is an attempt that will never change again.
	phaseTerminal
)

// commandSubmitted is one Command admitted to the serial coordinator.
type commandSubmitted struct {
	ctx       context.Context
	command   adapter.Command
	responder adapter.Responder
	result    chan error
}

func (commandSubmitted) runtimeEvent() {}

// setValueCompleted reports the outcome of one correlated node.set_value.
type setValueCompleted struct {
	attemptID uint64
	err       error
}

func (setValueCompleted) runtimeEvent() {}

// pollValueCompleted reports one correlated node.poll_value result.
type pollValueCompleted struct {
	attemptID  uint64
	value      json.RawMessage
	receivedAt time.Time
	err        error
}

func (pollValueCompleted) runtimeEvent() {}

// linkedPublishCompleted reports one command-linked Observation publication.
type linkedPublishCompleted struct {
	attemptID uint64
	matched   bool
	err       error
}

func (linkedPublishCompleted) runtimeEvent() {}

// attemptDeadlineReached reports that one attempt's absolute deadline passed.
type attemptDeadlineReached struct{ attemptID uint64 }

func (attemptDeadlineReached) runtimeEvent() {}

// pollTimerFired reports that one coalesced poll hint may now issue a poll.
type pollTimerFired struct{ attemptID uint64 }

func (pollTimerFired) runtimeEvent() {}

// errSetValuePastDeadline reports a node.set_value that was still unresolved
// when its absolute deadline passed. The write may already have reached the
// radio, so the attempt is ambiguous and its generation is closed rather than
// reused for a Command whose outcome can never be confirmed.
var errSetValuePastDeadline = errors.New("Z-Wave set value was unresolved at its deadline")

// attemptValueKey identifies the Value one attempt polls, including its node,
// because one Value key can exist on several nodes.
type attemptValueKey struct {
	nodeID int
	value  upstreamValueKey
}

// commandAttempt is one in-flight or queued Command.
type commandAttempt struct {
	id         uint64
	generation uint64
	// routeRevision is the route revision this attempt dispatches through. It is
	// rechecked before every dispatch and poll.
	routeRevision uint64
	entityID      string
	nodeID        int

	command    adapter.Command
	parameters json.RawMessage
	responder  adapter.Responder

	handlerResult chan error
	handlerDone   bool

	route    entityRoute
	setValue json.RawMessage
	matches  func(parameters json.RawMessage, state json.RawMessage) bool
	deadline time.Time
	// deadlineElapsed records that the absolute deadline passed while
	// node.set_value was still unresolved. Such an attempt keeps its FIFO slot
	// and is decided by its own completion, because a write already on the wire
	// can neither be cancelled nor advanced past.
	deadlineElapsed bool
	phase           attemptPhase
	accepted        bool
	evidence        adapter.CommandEvidence
	valueKey        attemptValueKey

	attemptContext context.Context
	cancelAttempt  context.CancelFunc
	deadlineTimer  *time.Timer
	// cancelLink cancels the in-flight command-linked publication of this
	// attempt, so a route change, removal, or sleep cannot leave stale linked
	// evidence behind.
	cancelLink context.CancelFunc

	pollInFlight bool
	hintPending  bool
	lastPollAt   time.Time
	pollTimer    *time.Timer
}

// nodeCommandQueue is one node's FIFO of Command attempts.
type nodeCommandQueue struct {
	active *commandAttempt
	queued []*commandAttempt
}

// HandleCommand submits one Command to the serial coordinator and waits for its
// single response.
func (zwave *Adapter) HandleCommand(
	ctx context.Context,
	command adapter.Command,
	responder adapter.Responder,
) error {
	result := make(chan error, 1)
	event := commandSubmitted{ctx: ctx, command: command, responder: responder, result: result}
	select {
	case zwave.runtimeEvents <- event:
	case <-ctx.Done():
		return ctx.Err()
	case <-zwave.runtimeDone:
		return errors.New("Z-Wave JS runtime stopped")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-zwave.runtimeDone:
		return errors.New("Z-Wave JS runtime stopped")
	}
}

// submitCommand resolves one Command against the active generation and route
// revision, decodes its parameters, and queues it behind its node's FIFO.
func (coordinator *runtimeCoordinator) submitCommand(event commandSubmitted) {
	if err := event.ctx.Err(); err != nil {
		event.result <- err
		return
	}
	if !coordinator.dispatchable {
		event.result <- event.responder.RejectUnavailable("Z-Wave Entity is unavailable")
		return
	}
	route, exists := coordinator.snapshot.ByEntityID[event.command.EntityID]
	if !exists {
		event.result <- event.responder.RejectUnavailable("Z-Wave Entity is unavailable")
		return
	}
	if event.command.OperationName != operationSet {
		event.result <- event.responder.Reject("Z-Wave Entity supports only the set operation")
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, event.command.Deadline)
	if err != nil {
		event.result <- err
		return
	}
	if !time.Now().Before(deadline) {
		event.result <- context.DeadlineExceeded
		return
	}
	setValue, err := route.Plan.EncodeSet(event.command)
	if err != nil {
		event.result <- event.responder.Reject("Z-Wave set parameters are invalid")
		return
	}
	record := coordinator.nodes[route.Plan.NodeID]
	if record == nil || record.revision == 0 {
		event.result <- event.responder.RejectUnavailable("Z-Wave Entity is unavailable")
		return
	}

	coordinator.nextAttemptID++
	attemptContext, cancelAttempt := context.WithDeadline(coordinator.ctx, deadline)
	attempt := &commandAttempt{
		id:             coordinator.nextAttemptID,
		generation:     coordinator.generation,
		routeRevision:  record.revision,
		entityID:       route.EntityID,
		nodeID:         route.Plan.NodeID,
		command:        event.command,
		parameters:     event.command.Parameters,
		responder:      event.responder,
		handlerResult:  event.result,
		route:          route,
		setValue:       setValue,
		matches:        route.Plan.Matches,
		deadline:       deadline,
		phase:          phaseQueued,
		valueKey:       attemptValueKey{nodeID: route.Plan.NodeID, value: route.Plan.CurrentValueID.valueKey()},
		attemptContext: attemptContext,
		cancelAttempt:  cancelAttempt,
	}
	attempt.deadlineTimer = time.AfterFunc(max(time.Until(deadline), 0), func() {
		coordinator.sendCompletion(attemptDeadlineReached{attemptID: attempt.id})
	})
	coordinator.attempts[attempt.id] = attempt
	queue := coordinator.queues[attempt.nodeID]
	if queue == nil {
		queue = &nodeCommandQueue{}
		coordinator.queues[attempt.nodeID] = queue
	}
	queue.queued = append(queue.queued, attempt)
	coordinator.startNext(attempt.nodeID)
}

// startNext dispatches the oldest admitted attempt of one node whose deadline
// is still open and whose route is still current. Attempts whose deadline
// elapsed while queued never dispatch: queue time consumes the absolute
// deadline.
func (coordinator *runtimeCoordinator) startNext(nodeID int) {
	queue := coordinator.queues[nodeID]
	if queue == nil || queue.active != nil {
		return
	}
	for len(queue.queued) > 0 {
		attempt := queue.queued[0]
		queue.queued = queue.queued[1:]
		if !time.Now().Before(attempt.deadline) {
			coordinator.finishAttempt(attempt, context.DeadlineExceeded)
			continue
		}
		if !coordinator.attemptRouteIsCurrent(attempt) {
			coordinator.abortAttempt(attempt, errStaleRoute)
			continue
		}

		coordinator.dispatchSetValue(queue, attempt)
		return
	}
	delete(coordinator.queues, nodeID)
}

// dispatchSetValue starts one correlated node.set_value outside the coordinator
// loop.
func (coordinator *runtimeCoordinator) dispatchSetValue(queue *nodeCommandQueue, attempt *commandAttempt) {
	queue.active = attempt
	attempt.phase = phaseSetting
	connection := coordinator.connection
	if connection == nil {
		coordinator.abortAttempt(attempt, errGenerationClosed)
		return
	}
	target := attempt.route.Plan.TargetValueID
	coordinator.startEffect(func() runtimeEvent {
		_, err := connection.SetValue(attempt.attemptContext, attempt.nodeID, target, attempt.setValue)
		return setValueCompleted{attemptID: attempt.id, err: err}
	})
}

// finishSetValue applies one node.set_value outcome. Acceptance happens only
// after a documented successful status; every other outcome either rejects the
// Command or invalidates the generation. A completion that arrives after the
// attempt's absolute deadline is ambiguous: it is diagnosed and closes the
// generation, and it is never accepted. The absolute deadline is checked against
// the wall clock as well as against the deadline timer's event, because the two
// events race in the coordinator's queue: a completion that is processed at or
// after the deadline is late even when its event beat the timer event.
func (coordinator *runtimeCoordinator) finishSetValue(event setValueCompleted) {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || attempt.phase != phaseSetting {
		return
	}
	if attempt.deadlineElapsed {
		coordinator.failAmbiguousSetValue(attempt, errSetValuePastDeadline)
		return
	}
	if event.err != nil {
		coordinator.failSetValue(attempt, event.err)
		return
	}
	// A successful status that the coordinator processes at or after the
	// absolute deadline is a late success: Acceptance may not follow the deadline
	// the Command already ended with, so it takes the same ambiguous diagnosis as
	// an unresolved write and drops the generation before the node's FIFO
	// advances.
	if !time.Now().Before(attempt.deadline) {
		coordinator.failAmbiguousSetValue(attempt, errSetValuePastDeadline)
		return
	}
	// A write that raced a route change must never be accepted through the stale
	// generation or revision it was dispatched under. The value may already have
	// reached the radio, so the Command is rejected as unavailable rather than
	// claimed.
	if !coordinator.attemptRouteIsCurrent(attempt) {
		coordinator.abortAttempt(attempt, errStaleRoute)
		return
	}
	evidence, err := attempt.responder.Accept()
	if err != nil {
		coordinator.finishAttempt(attempt, err)
		return
	}
	attempt.evidence = evidence
	attempt.accepted = true
	attempt.phase = phaseAccepted
	coordinator.attemptsByValue[attempt.valueKey] = attempt
	coordinator.finishHandler(attempt, nil)
	coordinator.pollNow(attempt)
}

// failAmbiguousSetValue ends one node.set_value attempt whose physical outcome
// can never be confirmed, together with the generation it ran in. The generation
// ends first: dropping it ends this attempt and rejects every unaccepted follower
// with the same cause, so no later write can be dispatched behind an unresolved
// one. Ending the attempt first would release its FIFO slot for exactly that.
func (coordinator *runtimeCoordinator) failAmbiguousSetValue(attempt *commandAttempt, cause error) {
	coordinator.logAmbiguousSetValue(attempt)
	coordinator.dropGeneration(cause)
}

// logAmbiguousSetValue records one node.set_value whose physical outcome can
// never be confirmed. The write may already have reached the radio.
func (coordinator *runtimeCoordinator) logAmbiguousSetValue(attempt *commandAttempt) {
	coordinator.adapter.logger.WarnContext(
		coordinator.ctx,
		"Z-Wave set value did not resolve within its deadline",
		slog.String(eventKey, "adapter.command_ambiguous"),
		slog.String("entity_id", attempt.entityID),
		slog.String("command_id", attempt.command.ID),
		slog.String("error_code", codeAmbiguousSetValue),
	)
}

// failSetValue classifies one failed node.set_value. A deterministic upstream
// value rejection, whether a refused status or a refused result envelope, is a
// Command rejection; a timed-out or deadline-canceled request is diagnosed as an
// ambiguous write and closes its generation; every other failure invalidates the
// generation and rejects the unaccepted Command.
func (coordinator *runtimeCoordinator) failSetValue(attempt *commandAttempt, cause error) {
	switch {
	case isSetValueRefused(cause):
		coordinator.rejectAttempt(attempt, "Z-Wave JS refused the value")
	case isUpstreamRejection(cause):
		// A failed result envelope is an ordinary upstream rejection, like the
		// client reports for any other refused request. It is not a transport
		// failure, so the generation stays healthy and only this Command is
		// rejected.
		coordinator.rejectAttempt(attempt, "Z-Wave JS rejected the request")
	case isRequestTimeout(cause):
		coordinator.failAmbiguousSetValue(attempt, cause)
	case errors.Is(cause, context.DeadlineExceeded), errors.Is(cause, context.Canceled):
		// The request context ended while the write was unresolved. Only the
		// attempt's own deadline can do that, because tearing an attempt down
		// makes it terminal first, so this is the same ambiguous write as a
		// server-side request timeout.
		coordinator.failAmbiguousSetValue(attempt, errSetValuePastDeadline)
	default:
		// A transport failure ends the generation before the node's FIFO can
		// advance: dropping the generation aborts this attempt together with
		// every queued follower, so no follower is ever written through a
		// generation that is about to be torn down. Aborting this attempt first
		// would release its FIFO slot for exactly that.
		coordinator.dropGeneration(cause)
	}
}

// pollNow issues one correlated node.poll_value for an accepted attempt if its
// deadline is still open and its route is still current.
func (coordinator *runtimeCoordinator) pollNow(attempt *commandAttempt) {
	if attempt.phase != phaseAccepted || attempt.pollInFlight {
		return
	}
	if !time.Now().Before(attempt.deadline) {
		coordinator.finishAttempt(attempt, context.DeadlineExceeded)
		return
	}
	if !coordinator.attemptRouteIsCurrent(attempt) {
		coordinator.abortAttempt(attempt, errStaleRoute)
		return
	}
	connection := coordinator.connection
	if connection == nil {
		coordinator.abortAttempt(attempt, errGenerationClosed)
		return
	}
	attempt.pollInFlight = true
	attempt.hintPending = false
	current := attempt.route.Plan.CurrentValueID
	coordinator.startEffect(func() runtimeEvent {
		// lastPollAt is stamped at the request itself, so the coalescing floor is
		// measured from the moment the poll was issued rather than from the
		// moment the coordinator decided to issue it.
		attempt.lastPollAt = time.Now()
		// The poll runs under the attempt's own lifetime, not the coordinator's:
		// the absolute deadline cancels an unanswered poll, which releases its
		// waiter and its goroutine without ending a healthy generation. A poll
		// result that arrives first is still correlated normally.
		value, receivedAt, err := connection.PollValue(attempt.attemptContext, attempt.nodeID, current)
		return pollValueCompleted{
			attemptID:  attempt.id,
			value:      value,
			receivedAt: receivedAt.UTC(),
			err:        err,
		}
	})
}

// finishPollValue publishes one successful poll result through the accepted
// Command's evidence capability, whether or not it matches. A matching linked
// Observation satisfies the Command in Core.
func (coordinator *runtimeCoordinator) finishPollValue(event pollValueCompleted) {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || attempt.phase != phaseAccepted {
		return
	}
	attempt.pollInFlight = false
	if event.err != nil {
		switch {
		case isRequestTimeout(event.err),
			errors.Is(event.err, context.Canceled),
			errors.Is(event.err, context.DeadlineExceeded):
			// The generation already ended, or the attempt's own lifetime did.
		case isUpstreamRejection(event.err):
			// A deterministic poll rejection ends linked evidence for this one
			// accepted attempt. It is not a transport failure, so the generation
			// stays healthy and keeps serving its other Entities.
		default:
			coordinator.dropGeneration(event.err)
		}
		coordinator.abortAttempt(attempt, event.err)
		return
	}
	observation, err := attempt.route.Plan.observe(attempt.route.EntityID, event.receivedAt, event.value)
	if err != nil {
		coordinator.logStateUnrepresentable(entityStateIssue{
			Key:      attempt.route.Plan.Key,
			EntityID: attempt.route.EntityID,
			Err:      err,
		})
		coordinator.scheduleHintPoll(attempt)
		return
	}
	// A poll that resolved after its route changed must never become linked
	// evidence for the route it was dispatched under.
	if !coordinator.attemptRouteIsCurrent(attempt) {
		coordinator.finishAttempt(attempt, errStaleRoute)
		return
	}
	matched := attempt.matches(attempt.parameters, observation.Value)
	evidence := attempt.evidence
	scope := coordinator.scope
	// Linked publication runs under its own cancelable lifetime so a route
	// change, removal, or sleep stops it before it can report stale evidence.
	// Only the newest linked publication of one attempt may be in flight.
	if attempt.cancelLink != nil {
		attempt.cancelLink()
	}
	linkContext, cancelLink := context.WithCancel(coordinator.effectContext(scope))
	attempt.cancelLink = cancelLink
	coordinator.startGenerationEffect(scope, func() runtimeEvent {
		defer cancelLink()
		_, publishErr := evidence.PublishObservation(linkContext, observation)
		return linkedPublishCompleted{attemptID: attempt.id, matched: matched, err: publishErr}
	})
}

// finishLinkedPublish ends a satisfied attempt, or waits for the next coalesced
// hint after a mismatching result. A failed linked publication is a diagnostic:
// the accepted Command simply receives no linked evidence and times out.
func (coordinator *runtimeCoordinator) finishLinkedPublish(event linkedPublishCompleted) {
	attempt := coordinator.attempts[event.attemptID]
	if attempt == nil || attempt.phase != phaseAccepted {
		return
	}
	if event.err != nil {
		if !errors.Is(event.err, context.Canceled) && !errors.Is(event.err, context.DeadlineExceeded) {
			coordinator.adapter.logger.WarnContext(
				coordinator.ctx,
				"command-linked Z-Wave Observation publication failed",
				slog.String(eventKey, "adapter.linked_observation_failed"),
				slog.String("entity_id", attempt.entityID),
				slog.String("error_code", codeLinkedPublishFailed),
			)
		}
		coordinator.finishAttempt(attempt, nil)
		return
	}
	if event.matched {
		coordinator.finishAttempt(attempt, nil)
		return
	}
	coordinator.scheduleHintPoll(attempt)
}

// scheduleHintPoll issues one coalesced poll no faster than the hint interval
// after the previous poll started.
func (coordinator *runtimeCoordinator) scheduleHintPoll(attempt *commandAttempt) {
	if attempt.phase != phaseAccepted || !attempt.hintPending || attempt.pollInFlight {
		return
	}
	wait := coordinator.adapter.pollHintInterval - time.Since(attempt.lastPollAt)
	if wait <= 0 {
		coordinator.pollNow(attempt)
		return
	}
	if attempt.pollTimer != nil {
		attempt.pollTimer.Stop()
	}
	attempt.pollTimer = time.AfterFunc(wait, func() {
		coordinator.sendCompletion(pollTimerFired{attemptID: attempt.id})
	})
}

// firePollTimer issues the poll one coalesced hint scheduled.
func (coordinator *runtimeCoordinator) firePollTimer(attemptID uint64) {
	attempt := coordinator.attempts[attemptID]
	if attempt == nil || attempt.phase != phaseAccepted {
		return
	}
	if !attempt.hintPending {
		return
	}
	coordinator.pollNow(attempt)
}

// reachDeadline ends one attempt whose absolute deadline passed. An unaccepted
// attempt receives no response because Core's own deadline already fired. An
// attempt whose node.set_value is still unresolved keeps its FIFO slot and is
// decided by its own completion: advancing the queue there would dispatch a
// follower while an unconfirmed write is still outstanding, and ending the
// attempt there would lose the ambiguous diagnosis.
func (coordinator *runtimeCoordinator) reachDeadline(attemptID uint64) {
	attempt := coordinator.attempts[attemptID]
	if attempt == nil || attempt.phase == phaseTerminal {
		return
	}
	if attempt.phase == phaseSetting {
		attempt.deadlineElapsed = true
		return
	}
	coordinator.finishAttempt(attempt, context.DeadlineExceeded)
}

// rejectAttempt rejects one unaccepted Command with a fixed message.
func (coordinator *runtimeCoordinator) rejectAttempt(attempt *commandAttempt, message string) {
	if attempt.handlerDone {
		coordinator.finishAttempt(attempt, nil)
		return
	}
	coordinator.finishAttempt(attempt, attempt.responder.Reject(message))
}

// finishAttempt ends one attempt exactly once, releases its FIFO slot, and
// advances its node's queue. A response is delivered only to an attempt that
// never accepted.
func (coordinator *runtimeCoordinator) finishAttempt(attempt *commandAttempt, handlerErr error) {
	if attempt.phase == phaseTerminal {
		return
	}
	attempt.phase = phaseTerminal
	if attempt.deadlineTimer != nil {
		attempt.deadlineTimer.Stop()
	}
	if attempt.pollTimer != nil {
		attempt.pollTimer.Stop()
	}
	if attempt.cancelAttempt != nil {
		attempt.cancelAttempt()
	}
	if attempt.cancelLink != nil {
		attempt.cancelLink()
	}
	if current, exists := coordinator.attemptsByValue[attempt.valueKey]; exists && current == attempt {
		delete(coordinator.attemptsByValue, attempt.valueKey)
	}
	delete(coordinator.attempts, attempt.id)
	queue := coordinator.queues[attempt.nodeID]
	if queue != nil {
		if queue.active == attempt {
			queue.active = nil
		} else {
			for index, queued := range queue.queued {
				if queued == attempt {
					queue.queued = slices.Delete(queue.queued, index, index+1)
					break
				}
			}
		}
	}
	coordinator.finishHandler(attempt, handlerErr)
	if queue != nil {
		coordinator.startNext(attempt.nodeID)
	}
}

// finishHandler delivers the attempt's one handler result.
func (coordinator *runtimeCoordinator) finishHandler(attempt *commandAttempt, err error) {
	if attempt.handlerDone {
		return
	}
	attempt.handlerDone = true
	attempt.handlerResult <- err
}
