package zigbee2mqtt

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type refreshSubmitted struct {
	ctx      context.Context
	deadline time.Time
	entityID string
	result   chan adapter.FunctionResult
}

func (refreshSubmitted) runtimeEvent() {}

type refreshAttempt struct {
	generation       uint64
	revision         uint64
	entityID         string
	ctx              context.Context
	cancel           context.CancelFunc
	stopCancellation func() bool
	result           chan adapter.FunctionResult
	// Synchronize cancellation with the last pre-publication check. Once began is
	// true, cancellation cannot prove that MQTT did not see the request.
	mu    sync.Mutex
	began bool
}
type refreshFinished struct {
	attempt *refreshAttempt
	err     error
	at      time.Time
}

func (refreshFinished) runtimeEvent() {}

type refreshCanceled struct{ attempt *refreshAttempt }

func (refreshCanceled) runtimeEvent() {}

func (coordinator *runtimeCoordinator) submitRefresh(event refreshSubmitted) {
	if !coordinator.dispatchable || coordinator.connection == nil || event.ctx.Err() != nil ||
		!time.Now().Before(event.deadline) {
		event.result <- adapter.FunctionNotExecuted{ReasonCode: refreshUnavailableReason}
		return
	}
	route, exists := coordinator.refreshRoutes[event.entityID]
	if !exists {
		event.result <- adapter.FunctionNotExecuted{ReasonCode: "entity_unavailable"}
		return
	}
	if len(route.entity.plan.GetProperties) == 0 {
		event.result <- adapter.FunctionNotExecuted{ReasonCode: "refresh_unsupported"}
		return
	}
	ctx, cancel := context.WithDeadline(event.ctx, event.deadline)
	attempt := &refreshAttempt{generation: coordinator.generation, revision: coordinator.routeRevision,
		entityID: event.entityID, ctx: ctx, cancel: cancel, result: event.result}
	coordinator.refreshes[attempt] = struct{}{}
	attempt.stopCancellation = context.AfterFunc(
		ctx,
		func() { coordinator.sendCompletion(refreshCanceled{attempt: attempt}) },
	)
	connection := coordinator.connection
	properties := append([]string(nil), route.entity.plan.GetProperties...)
	friendly, base := route.friendlyName, coordinator.adapter.config.BaseTopic
	coordinator.startEffect(func() runtimeEvent {
		attempt.mu.Lock()
		err := ctx.Err()
		if err == nil {
			attempt.began = true
		}
		attempt.mu.Unlock()
		if err == nil {
			err = publishGet(ctx, connection, base, friendly, properties)
		}
		return refreshFinished{attempt: attempt, err: err, at: time.Now()}
	})
}

func (coordinator *runtimeCoordinator) finishRefresh(event refreshFinished) {
	attempt := event.attempt
	if _, exists := coordinator.refreshes[attempt]; !exists {
		return
	}
	deadline, _ := attempt.ctx.Deadline()
	if event.err != nil || attempt.ctx.Err() != nil || !event.at.Before(deadline) ||
		!coordinator.dispatchable || attempt.generation != coordinator.generation || attempt.revision != coordinator.routeRevision {
		coordinator.cancelRefresh(attempt)
		return
	}
	result, _ := json.Marshal(struct {
		EntityID      string `json:"entity_id"`
		RequestStatus string `json:"request_status"`
	}{attempt.entityID, "published"})
	coordinator.completeRefresh(attempt, adapter.FunctionSucceeded{Result: result})
}

func (coordinator *runtimeCoordinator) cancelRefresh(attempt *refreshAttempt) {
	if _, exists := coordinator.refreshes[attempt]; !exists {
		return
	}
	attempt.mu.Lock()
	attempt.cancel()
	began := attempt.began
	attempt.mu.Unlock()
	var result adapter.FunctionResult = adapter.FunctionNotExecuted{ReasonCode: refreshUnavailableReason}
	if began {
		result = adapter.FunctionUnknown{ReasonCode: "refresh_outcome_unknown"}
	}
	coordinator.completeRefresh(attempt, result)
}

func (coordinator *runtimeCoordinator) completeRefresh(attempt *refreshAttempt, result adapter.FunctionResult) {
	delete(coordinator.refreshes, attempt)
	attempt.stopCancellation()
	attempt.cancel()
	attempt.result <- result
}

func (coordinator *runtimeCoordinator) invalidateRefreshes() {
	for attempt := range coordinator.refreshes {
		coordinator.cancelRefresh(attempt)
	}
}
