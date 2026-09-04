package zigbee2mqtt

import (
	"context"
	"errors"
	"time"
)

func (coordinator *runtimeCoordinator) handleState(event stateCandidate) error {
	attempt := coordinator.matchers[event.entityID]
	// The outcome matcher runs only after every cheap gate passes so stale or
	// late evidence never invokes the closure.
	if attempt == nil || event.retained || event.generation != attempt.generation ||
		event.routeRevision != attempt.routeRevision || attempt.claimed != nil ||
		!time.Now().Before(attempt.deadline) ||
		attempt.phase == commandPublishingLinked || attempt.phase == commandPublishingFallback ||
		attempt.phase == commandTerminal || !event.receivedAt.After(attempt.dispatchedAt) ||
		event.receivedAt.After(attempt.deadline) || !attempt.matches(event.report) {
		coordinator.startOrdinary(event)
		return nil
	}
	attempt.claimed = &matchedState{report: event.report, receivedAt: event.receivedAt}
	event.result <- stateClaimed
	if attempt.evidence != nil {
		coordinator.startLinked(attempt)
	}
	return nil
}

func (coordinator *runtimeCoordinator) startOrdinary(candidate stateCandidate) {
	coordinator.startEffect(func() runtimeEvent {
		_, publishErr := coordinator.adapter.session.PublishObservation(candidate.ctx, candidate.report.Observation)
		return ordinaryPublishFinished{result: candidate.result, err: publishErr}
	})
}

func (coordinator *runtimeCoordinator) startLinked(attempt *commandAttempt) {
	if attempt.phase != commandAwaitingEvidence || attempt.claimed == nil || attempt.evidence == nil {
		return
	}
	observation := attempt.claimed.report.Observation
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
	observation := attempt.claimed.report.Observation
	attempt.phase = commandPublishingFallback
	coordinator.startEffect(func() runtimeEvent {
		_, publishErr := coordinator.adapter.session.PublishObservation(coordinator.ctx, observation)
		return fallbackPublishFinished{attemptID: attempt.id, err: publishErr}
	})
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
