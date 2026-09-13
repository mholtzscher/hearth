package ecowitt

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This file owns the coordinator's ordered work pipeline: admission and bounds
// for copied work, the single active chain, its stage dispatch, and the effect
// completions that advance it. A chain always keeps its already-acquired
// Observations, even after its MQTT generation ends or a later silence revokes
// its evidence; only health recovery and availability are fenced, because Core
// clears availability on an unhealthy transition and only fresh, currently
// eligible evidence may restore it.

// enqueueWork admits one unit of ordered work behind the single active chain.
// A full pending-report queue ends the generation visibly; a second stale
// availability item coalesces into the one already waiting.
func (c *runtimeCoordinator) enqueueWork(item workItem) error {
	if c.activeWork == nil {
		return c.startWork(item)
	}
	if item.kind == workStaleAvailability {
		if c.hasPendingStale() {
			return nil
		}
		c.pendingWork = append(c.pendingWork, item)
		return nil
	}
	if c.pendingReportCount() >= pendingReportLimit {
		return c.overflowPendingWork()
	}
	c.pendingWork = append(c.pendingWork, item)
	return nil
}

// pendingReportCount counts copied reports waiting behind the active chain.
func (c *runtimeCoordinator) pendingReportCount() int {
	count := 0
	for _, item := range c.pendingWork {
		if item.kind == workReport {
			count++
		}
	}
	return count
}

// hasPendingStale reports whether a stale availability recomputation already
// waits behind the active chain.
func (c *runtimeCoordinator) hasPendingStale() bool {
	for _, item := range c.pendingWork {
		if item.kind == workStaleAvailability {
			return true
		}
	}
	return false
}

// overflowPendingWork ends the generation with the same visible behavior as a
// callback relay overflow: one fixed diagnostic, an unhealthy transition, and
// a reconnect instead of silently evicting a chosen report.
func (c *runtimeCoordinator) overflowPendingWork() error {
	c.adapter.logger.WarnContext(
		c.ctx,
		"Ecowitt pending report limit reached",
		slog.String(eventKey, "adapter.pending_report_limit_reached"),
		slog.String(errorCodeKey, pendingWorkErrorCode),
		slog.Int("pending_report_limit", pendingReportLimit),
	)
	c.dropPendingWork(pendingWorkErrorCode)
	c.transitionUnhealthy(externalSystemUnavailableReason)
	c.endGeneration(errPendingReportOverflow)
	return nil
}

// dropPendingWork releases copied work that can no longer be published. The
// count is logged: queued reports are never discarded silently. This is used
// only for the deliberate relay-like resync of a pending-report overflow; an
// ordinary disconnect preserves accepted report evidence.
func (c *runtimeCoordinator) dropPendingWork(code string) {
	if len(c.pendingWork) == 0 {
		return
	}
	dropped := len(c.pendingWork)
	c.pendingWork = nil
	c.adapter.logger.WarnContext(
		c.ctx,
		"dropped pending Ecowitt work",
		slog.String(eventKey, "adapter.pending_work_dropped"),
		slog.String(errorCodeKey, code),
		slog.Int("dropped_items", dropped),
	)
}

// startWork begins one unit of ordered work. Work that would produce no effect
// completes immediately so the queue keeps draining.
func (c *runtimeCoordinator) startWork(item workItem) error {
	c.nextSequence++
	chain := &workChain{
		sequence:         c.nextSequence,
		generation:       item.generation,
		kind:             item.kind,
		report:           item.report,
		receivedAt:       item.receivedAt,
		evidenceRevision: item.evidenceRevision,
		stage:            stageObservations,
	}
	switch item.kind {
	case workReport:
		c.planReportWork(chain)
	case workStaleAvailability:
		if c.health != adapter.HealthHealthy {
			return c.startNextWork()
		}
		c.planStaleWork(chain)
	}
	if chain.stage == stageObservations && len(chain.observations) == 0 {
		return c.startNextWork()
	}
	c.activeWork = chain
	return c.advance()
}

// planReportWork projects one accepted report into its ordered chain: local
// measurement evidence first, then health recovery when needed, then the
// availability transition Core must observe before any Observation. A report
// whose MQTT generation ended, or whose evidence a later unhealthy transition
// revoked, keeps only its Observations: health recovery and availability
// belong to fenced evidence and stay suppressed until a report is accepted after
// the fence.
func (c *runtimeCoordinator) planReportWork(chain *workChain) {
	projected, invalid := projectReport(c.routes, chain.report, chain.receivedAt)
	if invalid > 0 {
		c.adapter.logger.DebugContext(
			c.ctx,
			"isolated invalid Ecowitt measurements",
			slog.String(eventKey, "adapter.measurements_isolated"),
			slog.String(errorCodeKey, invalidMeasurementErrorCode),
			slog.Int("invalid_measurement_count", invalid),
		)
	}
	chain.observations = projected
	for _, measurement := range projected {
		c.freshness.observe(measurement.Route.Index, chain.receivedAt)
	}
	if !c.chainEligible(chain) {
		chain.stage = stageObservations
		c.refreshMeasurementDeadline()
		return
	}
	if c.health != adapter.HealthHealthy {
		chain.stage = stageHealth
		c.refreshMeasurementDeadline()
		return
	}
	c.planChainAvailability(chain)
}

// planChainAvailability builds the availability batch a chain must send before
// its Observations. It runs after any health acknowledgement, because Core
// clears availability on an unhealthy adapter and only a healthy, committed
// transition makes a fresh availability report meaningful.
func (c *runtimeCoordinator) planChainAvailability(chain *workChain) {
	observed := make([]int, 0, len(chain.observations))
	for _, measurement := range chain.observations {
		observed = append(observed, measurement.Route.Index)
	}
	chain.availableIndices = c.freshness.planAvailable(observed)
	chain.availabilityBatch = 0
	chain.availabilityBatches = c.freshness.availabilityReports(
		c.routes, chain.availableIndices, adapter.AvailabilityAvailable, "", chain.receivedAt,
	)
	chain.stage = stageObservations
	if len(chain.availabilityBatches) > 0 {
		chain.stage = stageAvailability
	}
	c.refreshMeasurementDeadline()
}

// planStaleWork recomputes the Entities whose measurement stale interval
// elapsed and builds their explicit unavailable batch.
func (c *runtimeCoordinator) planStaleWork(chain *workChain) {
	now := c.clock.Now()
	stale := c.freshness.staleTransitions(now, c.adapter.config.UploadInterval)
	if len(stale) == 0 {
		return
	}
	chain.availabilityBatches = c.freshness.availabilityReports(
		c.routes, stale, adapter.AvailabilityUnavailable, measurementStaleReason, now,
	)
	chain.stage = stageAvailability
}

// advance dispatches the next effect of the active chain. Health recovery and
// availability are skipped for an ineligible chain, while its already-acquired
// Observations continue to drain in catalog order.
func (c *runtimeCoordinator) advance() error {
	chain := c.activeWork
	if chain == nil {
		return c.startNextWork()
	}
	if chain.pendingStage != stageIdle {
		return nil
	}
	switch chain.stage {
	case stageIdle:
		return nil
	case stageHealth:
		if !c.chainEligible(chain) {
			c.demoteToObservations(chain)
			return c.advance()
		}
		if chain.healthQueued {
			return nil
		}
		return c.dispatchHealthRecovery(chain)
	case stageAvailability:
		if !c.chainEligible(chain) {
			c.demoteToObservations(chain)
			return c.advance()
		}
		if chain.availabilityBatch >= len(chain.availabilityBatches) {
			chain.stage = stageObservations
			return c.advance()
		}
		return c.dispatchAvailability(chain)
	case stageObservations:
		if chain.observationIndex >= len(chain.observations) {
			return c.finishWork()
		}
		return c.dispatchObservation(chain)
	}
	return nil
}

// demoteToObservations keeps a chain's already-acquired Observations while
// dropping the health recovery and availability that fenced evidence may no
// longer claim. Optimistically planned availability returns to unknown, so
// recovery re-sends it after a new eligible report commits healthy.
func (c *runtimeCoordinator) demoteToObservations(chain *workChain) {
	if len(chain.availableIndices) > 0 {
		c.freshness.revertAvailable(chain.availableIndices)
		chain.availableIndices = nil
	}
	chain.availabilityBatches = nil
	chain.availabilityBatch = 0
	chain.healthQueued = false
	chain.stage = stageObservations
	c.refreshMeasurementDeadline()
}

// dispatchHealthRecovery queues the healthy acknowledgement a chain waits for
// before availability and Observations. The ordered health pipeline guarantees
// it is submitted after any earlier health decision, its completion commits
// only while the chain stays eligible, and the coordinator never blocks on the
// acknowledgement.
func (c *runtimeCoordinator) dispatchHealthRecovery(chain *workChain) error {
	chain.healthQueued = true
	c.enqueueHealth(chain.sequence, adapter.HealthReport{
		Status:           adapter.HealthHealthy,
		SourceObservedAt: c.clock.Now().UTC(),
	})
	return nil
}

// dispatchAvailability sends one explicit availability batch.
func (c *runtimeCoordinator) dispatchAvailability(chain *workChain) error {
	batch := chain.availabilityBatches[chain.availabilityBatch]
	sequence := chain.sequence
	chain.pendingStage = stageAvailability
	c.spawnEffect(sequence, stageAvailability, func(ctx context.Context) error {
		return c.adapter.session.ReportEntityAvailability(ctx, batch)
	})
	return nil
}

// dispatchObservation publishes exactly one typed Observation, so a chain's
// Observations stay in catalog order.
func (c *runtimeCoordinator) dispatchObservation(chain *workChain) error {
	observation := chain.observations[chain.observationIndex].Observation
	sequence := chain.sequence
	chain.pendingStage = stageObservations
	c.spawnEffect(sequence, stageObservations, func(ctx context.Context) error {
		_, err := c.adapter.session.PublishObservation(ctx, observation)
		return err
	})
	return nil
}

// spawnEffect runs one blocking SDK call in a tracked goroutine and reports its
// completion with the chain that started it. The coordinator never waits for
// the effect inline.
func (c *runtimeCoordinator) spawnEffect(sequence uint64, stage chainStage, effect func(context.Context) error) {
	c.effects.Go(func() {
		err := effect(c.ctx)
		_ = c.submit(c.ctx, effectFinished{sequence: sequence, stage: stage, err: err})
	})
}

// handleEffect applies one tracked availability or Observation completion. A
// completion for a superseded sequence or stage is ignored. An availability
// completion for an ineligible chain is demoted so the chain still publishes
// its already-acquired Observations.
func (c *runtimeCoordinator) handleEffect(event effectFinished) error {
	chain := c.activeWork
	if chain == nil || chain.sequence != event.sequence || chain.pendingStage != event.stage {
		c.logSupersededWork()
		return nil
	}
	chain.pendingStage = stageIdle
	if event.stage == stageAvailability && !c.chainEligible(chain) {
		c.logSupersededWork()
		c.demoteToObservations(chain)
		return c.advance()
	}
	if err := c.applyEffect(chain, event); err != nil {
		return err
	}
	return c.advance()
}

// applyEffect advances the active chain by one completed stage.
func (c *runtimeCoordinator) applyEffect(chain *workChain, event effectFinished) error {
	switch chain.stage {
	case stageAvailability:
		return c.applyAvailabilityEffect(chain, event.err)
	case stageObservations:
		if err := c.terminalError(event.err); err != nil {
			return err
		}
		chain.observationIndex++
	case stageIdle, stageHealth:
		return nil
	}
	return nil
}

// applyAvailabilityEffect handles one availability completion. A rejection
// caused by a committed unhealthy transition is a superseded generation race:
// the optimistic availability is reverted and fresh availability follows
// recovery instead of the old report being retried. Any other rejection is a
// permanent configuration failure and stays terminal.
func (c *runtimeCoordinator) applyAvailabilityEffect(chain *workChain, effectErr error) error {
	if effectErr != nil {
		var rejection *adapter.EntityAvailabilityRejectedError
		if errors.As(effectErr, &rejection) && rejection.Code == adapter.EntityAvailabilityAdapterUnhealthy {
			c.freshness.revertAvailable(chain.availableIndices)
			c.refreshMeasurementDeadline()
			c.adapter.logger.DebugContext(
				c.ctx,
				"superseded Ecowitt Entity availability",
				slog.String(eventKey, "adapter.availability_superseded"),
				slog.String(errorCodeKey, supersededHealthErrorCode),
			)
			chain.stage = stageObservations
			return nil
		}
		return c.terminalError(effectErr)
	}
	chain.availabilityBatch++
	if chain.availabilityBatch < len(chain.availabilityBatches) {
		return nil
	}
	chain.stage = stageObservations
	return nil
}

// finishWork completes the active chain and starts the next queued item.
func (c *runtimeCoordinator) finishWork() error {
	c.activeWork = nil
	c.refreshMeasurementDeadline()
	return c.startNextWork()
}

// startNextWork starts the first queued item that still applies.
func (c *runtimeCoordinator) startNextWork() error {
	for len(c.pendingWork) > 0 {
		item := c.pendingWork[0]
		c.pendingWork[0] = workItem{}
		c.pendingWork = c.pendingWork[1:]
		if len(c.pendingWork) == 0 {
			c.pendingWork = nil
		}
		if !c.workCurrent(item) {
			c.logSupersededWork()
			continue
		}
		return c.startWork(item)
	}
	return nil
}

// workCurrent reports whether queued work still applies. Accepted report
// evidence always applies, even after its MQTT generation ended or a later
// silence revoked its health eligibility, because it was already received and
// validated and must be published in original order. A stale availability
// recomputation belongs to one generation and is dropped when that generation
// changes.
func (c *runtimeCoordinator) workCurrent(item workItem) bool {
	if item.kind == workStaleAvailability {
		return item.generation == c.generation
	}
	return true
}
