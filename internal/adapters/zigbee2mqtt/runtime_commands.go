package zigbee2mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

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
	// Read-only Entities have no translator: reject without queuing. Falling
	// through would queue an empty plan whose zero deadline never completes
	// the handler.
	if route.entity.plan.TranslateCommand == nil {
		event.result <- event.responder.RejectUnavailable("Zigbee2MQTT Entity is unavailable")
		return
	}
	payload, planned, err := translateCommand(event.ctx, route, event.command, event.responder)
	if err != nil {
		event.result <- err
		return
	}
	coordinator.nextAttemptID++
	mqttContext, cancelMQTT := context.WithDeadline(coordinator.ctx, planned.Deadline)
	attempt := &commandAttempt{
		id:            coordinator.nextAttemptID,
		generation:    coordinator.generation,
		routeRevision: coordinator.routeRevision,
		command:       event.command,
		responder:     event.responder,
		handlerResult: event.result,
		route:         route,
		payload:       payload,
		matches:       planned.Matches,
		refresh:       planned.GetProperties,
		deadline:      planned.Deadline,
		phase:         commandQueued,
		mqttContext:   mqttContext,
		cancelMQTT:    cancelMQTT,
	}
	attempt.deadlineTimer = time.AfterFunc(max(time.Until(planned.Deadline), 0), func() {
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
			attempt.refresh,
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
		slog.String(eventKey, "adapter.command_refresh_failed"),
		slog.String("entity_id", attempt.command.EntityID),
		slog.String("error_code", "refresh_publish_failed"),
	)
}

func (coordinator *runtimeCoordinator) reachDeadline(attemptID uint64) {
	attempt := coordinator.attempts[attemptID]
	if attempt == nil || attempt.phase == commandPublishingLinked || attempt.phase == commandPublishingFallback {
		return
	}
	coordinator.finishHandler(attempt, context.DeadlineExceeded)
	coordinator.finishBeforeLink(attempt)
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
