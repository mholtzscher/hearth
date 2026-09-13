package ecowitt

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// runtimeEventBuffer bounds the coordinator's event queue. It is the same
	// order of magnitude as the callback relay so backpressure is reached
	// through the relay rather than by blocking a Paho callback.
	runtimeEventBuffer = 64
	// pendingReportLimit bounds copied reports that wait behind the one active
	// ordered report chain. Exceeding it ends the MQTT generation visibly.
	pendingReportLimit = 64
	// reportTimeoutIntervals is the report timeout as a multiple of the
	// configured upload interval.
	reportTimeoutIntervals = 3
)

// errRuntimeStopped reports that the coordinator has stopped and can accept no
// further upstream events. It carries no vendor identity.
var errRuntimeStopped = errors.New("ecowitt runtime stopped")

// errPendingReportOverflow ends the current MQTT generation because more than
// pendingReportLimit copied reports are waiting behind the active ordered
// chain. Like the callback relay, it ends the generation rather than evicting
// one arbitrarily chosen report.
var errPendingReportOverflow = errors.New("ecowitt pending report limit reached; reconnecting to resynchronize")

// errUpstreamAlreadyEnded is the connection-teardown cause the coordinator uses
// when it has already observed an upstream failure and the connection is
// already ending. A generation the coordinator ends itself passes its own
// specific cause.
var errUpstreamAlreadyEnded = errors.New("ecowitt MQTT generation already ended")

// runtimeEvent is one input to the serial runtime coordinator. MQTT callbacks
// and deadline timers only submit these bounded, already-copied events.
type runtimeEvent interface{ runtimeEvent() }

// generationStarting announces one MQTT connection attempt before it dials, so
// a connect or subscribe failure can be attributed to the current generation.
type generationStarting struct {
	generation uint64
	result     chan error
}

func (generationStarting) runtimeEvent() {}

// generationEstablished announces a successful SUBACK. It arms the initial
// report deadline and hands the coordinator the generation's disconnect hook.
type generationEstablished struct {
	generation   uint64
	disconnect   context.CancelCauseFunc
	subscribedAt time.Time
}

func (generationEstablished) runtimeEvent() {}

// upstreamUnavailable reports a connect failure, disconnect, subscription
// failure, or relay overflow for one MQTT generation.
type upstreamUnavailable struct {
	generation uint64
}

func (upstreamUnavailable) runtimeEvent() {}

// reportReceived carries one copied MQTT delivery into the coordinator.
type reportReceived struct {
	generation uint64
	message    mqttMessage
}

func (reportReceived) runtimeEvent() {}

// effectFinished reports the completion of one tracked SDK effect together
// with the work sequence that started it, so a stale completion cannot mutate
// current coordinator state.
type effectFinished struct {
	sequence uint64
	stage    chainStage
	err      error
}

func (effectFinished) runtimeEvent() {}

// healthFinished reports the completion of one serialized SetHealth call with
// the decision epoch that authorized it. A completion whose epoch was
// superseded by a newer health decision cannot commit, so a stale healthy
// acknowledgement never restores health after a silence or disconnect.
type healthFinished struct {
	epoch    uint64
	sequence uint64
	status   adapter.HealthStatus
	err      error
}

func (healthFinished) runtimeEvent() {}

// reportDeadlineReached reports that the initial or steady-state report
// deadline elapsed for one MQTT generation.
type reportDeadlineReached struct {
	generation uint64
}

func (reportDeadlineReached) runtimeEvent() {}

// measurementDeadlineReached reports that a measurement stale deadline
// elapsed.
type measurementDeadlineReached struct{}

func (measurementDeadlineReached) runtimeEvent() {}

// chainStage is one ordered step of a report chain.
type chainStage uint8

const (
	// stageIdle means no availability or Observation effect is in flight for
	// the chain.
	stageIdle chainStage = iota
	// stageHealth is the health-recovery acknowledgement a report chain waits
	// for before availability and Observations.
	stageHealth
	// stageAvailability is the explicit availability report for one chain.
	stageAvailability
	// stageObservations publishes the chain's typed Observations one at a time
	// in catalog order.
	stageObservations
)

// workKind is one unit of ordered coordinator work.
type workKind uint8

const (
	// workReport is one accepted station report.
	workReport workKind = iota
	// workStaleAvailability recomputes and reports Entities whose measurement
	// stale interval elapsed.
	workStaleAvailability
)

// workItem is one copied unit of ordered work.
type workItem struct {
	kind       workKind
	generation uint64
	report     stationReport
	receivedAt time.Time
	// evidenceRevision is the report-evidence revision captured when the
	// coordinator accepted this work, so a later silence or upstream
	// invalidation revokes the eligibility of evidence that already waited.
	evidenceRevision uint64
}

// workChain is the single active ordered work chain. Health recovery and
// availability are gated by the chain's MQTT generation and its captured
// report-evidence revision; already-acquired Observations drain even after
// either fence is raised.
type workChain struct {
	sequence         uint64
	generation       uint64
	kind             workKind
	report           stationReport
	receivedAt       time.Time
	evidenceRevision uint64
	stage            chainStage

	healthQueued bool
	// pendingStage is the availability or Observation stage whose effect is in
	// flight, or stageIdle. It prevents a second effect for the same chain from
	// being dispatched while one is already in flight.
	pendingStage chainStage

	availabilityBatches [][]adapter.EntityAvailabilityReport
	availabilityBatch   int
	availableIndices    []int

	observations     []projectedMeasurement
	observationIndex int
}

// effectGroup runs tracked SDK effects so the coordinator never blocks its
// event loop on a NATS acknowledgement, and joins them on shutdown. Runtime
// tests substitute an inline group so effect completion is deterministic.
type effectGroup interface {
	Go(func())
	Wait()
}

// waitGroupEffects is the production tracked-effect group.
type waitGroupEffects struct {
	group sync.WaitGroup
}

// Go starts one tracked effect.
func (effects *waitGroupEffects) Go(effect func()) { effects.group.Go(effect) }

// Wait joins every tracked effect.
func (effects *waitGroupEffects) Wait() { effects.group.Wait() }

// runtimeCoordinator owns MQTT generation state, report and measurement
// deadlines, duplicate suppression, availability state, and ordered report
// work. Every blocking SDK call runs in a tracked effect goroutine; the
// coordinator never blocks on an acknowledgement.
type runtimeCoordinator struct {
	adapter *Adapter
	routes  routeSnapshot

	ctx     context.Context
	cancel  context.CancelFunc
	events  chan runtimeEvent
	done    chan struct{}
	clock   clock
	effects effectGroup

	generation      uint64
	generationEnded bool
	disconnect      context.CancelCauseFunc

	health       adapter.HealthStatus
	healthReason string

	// healthEpoch revisions every health decision. A health completion whose
	// epoch is no longer current cannot commit, so concurrent healthy and
	// unhealthy decisions cannot reorder or restore stale health.
	healthEpoch    uint64
	healthInFlight bool
	healthQueue    []healthDecision

	// evidenceRevision revisions the eligibility of report evidence for health
	// recovery and availability. It advances on every unhealthy transition, so
	// evidence accepted before a station silence or an upstream invalidation
	// still publishes its already-acquired Observations but can never restore
	// health or availability: only a report accepted after the transition can.
	evidenceRevision uint64

	lastSignature  *reportSignature
	lastAcceptedAt time.Time

	reportDeadlineAt time.Time
	reportTimer      timer

	freshness             *availabilityTracker
	measurementDeadlineAt time.Time
	measurementTimer      timer

	activeWork   *workChain
	pendingWork  []workItem
	nextSequence uint64
}

// healthDecision is one ordered SetHealth submission. A standalone unhealthy
// transition carries sequence 0; a report chain carries its sequence so its
// completion can advance that chain.
type healthDecision struct {
	epoch    uint64
	sequence uint64
	report   adapter.HealthReport
}

// newRuntimeCoordinator builds the serial coordinator for one route snapshot.
func newRuntimeCoordinator(
	ctx context.Context,
	cancel context.CancelFunc,
	ecowitt *Adapter,
	routes routeSnapshot,
	clock clock,
	effects effectGroup,
) *runtimeCoordinator {
	return &runtimeCoordinator{
		adapter:   ecowitt,
		routes:    routes,
		ctx:       ctx,
		cancel:    cancel,
		events:    make(chan runtimeEvent, runtimeEventBuffer),
		done:      make(chan struct{}),
		clock:     clock,
		effects:   effects,
		health:    adapter.HealthUnknown,
		freshness: newAvailabilityTracker(len(routes.entities)),
	}
}

// run drains events until the run context ends or a terminal effect failure
// stops the Adapter, then joins every tracked effect.
func (c *runtimeCoordinator) run() error {
	var runErr error
	for runErr == nil {
		select {
		case <-c.ctx.Done():
			runErr = c.ctx.Err()
		case event := <-c.events:
			runErr = c.handle(event)
		}
	}
	c.shutdown()
	return runErr
}

// shutdown cancels in-flight effects, stops every deadline, joins the tracked
// effects, and releases event submitters.
func (c *runtimeCoordinator) shutdown() {
	c.cancel()
	c.stopReportTimer()
	c.stopMeasurementTimer()
	c.effects.Wait()
	close(c.done)
}

// handle applies exactly one event.
func (c *runtimeCoordinator) handle(event runtimeEvent) error {
	switch event := event.(type) {
	case generationStarting:
		return c.handleGenerationStarting(event)
	case generationEstablished:
		return c.handleGenerationEstablished(event)
	case upstreamUnavailable:
		return c.handleUpstreamUnavailable(event)
	case reportReceived:
		return c.handleReport(event)
	case effectFinished:
		return c.handleEffect(event)
	case healthFinished:
		return c.handleHealthFinished(event)
	case reportDeadlineReached:
		return c.handleReportDeadline(event)
	case measurementDeadlineReached:
		return c.handleMeasurementDeadline()
	}
	return nil
}

// submit enqueues one event without ever dropping it silently: it waits for
// room, for the caller's context, or for the runtime to stop.
func (c *runtimeCoordinator) submit(ctx context.Context, event runtimeEvent) error {
	select {
	case c.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errRuntimeStopped
	}
}

// submitSync enqueues one event and waits for its acknowledgement.
func (c *runtimeCoordinator) submitSync(ctx context.Context, event runtimeEvent, result chan error) error {
	if err := c.submit(ctx, event); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errRuntimeStopped
	}
}

func (c *runtimeCoordinator) handleGenerationStarting(event generationStarting) error {
	if event.generation <= c.generation {
		acknowledgeGeneration(event.result)
		return nil
	}
	c.generation = event.generation
	c.generationEnded = false
	c.disconnect = nil
	c.stopReportTimer()
	c.reportDeadlineAt = time.Time{}
	// Duplicate suppression is connection-local: a new MQTT connection accepts
	// the same report signature as fresh evidence, because it is a new live
	// delivery rather than a broker redelivery on the old connection.
	c.lastSignature = nil
	// Accepted report evidence from the previous generation is deliberately not
	// discarded: it stays queued and still publishes its Observations, while
	// its now-dead generation cannot contribute health or availability.
	acknowledgeGeneration(event.result)
	return nil
}

// acknowledgeGeneration releases one generation-start waiter without ever
// blocking the coordinator's event loop. Callers pass a buffered channel.
func acknowledgeGeneration(result chan error) {
	if result == nil {
		return
	}
	select {
	case result <- nil:
	default:
	}
}

func (c *runtimeCoordinator) handleGenerationEstablished(event generationEstablished) error {
	if event.generation != c.generation {
		return nil
	}
	c.disconnect = event.disconnect
	base := event.subscribedAt
	if c.lastAcceptedAt.After(base) {
		base = c.lastAcceptedAt
	}
	c.reportDeadlineAt = base.Add(c.reportTimeout())
	c.scheduleReportTimer()
	c.refreshMeasurementDeadline()
	return nil
}

// endGeneration invalidates the current MQTT generation in coordinator memory
// and asks the connection supervisor to tear the connection down. An in-flight
// publication is deliberately not cancelled: it already acquired its evidence
// and keeps its SDK envelope and Observation ID through retry.
func (c *runtimeCoordinator) endGeneration(cause error) {
	c.generationEnded = true
	disconnect := c.disconnect
	c.disconnect = nil
	if disconnect != nil {
		disconnect(cause)
	}
}

func (c *runtimeCoordinator) handleUpstreamUnavailable(event upstreamUnavailable) error {
	if event.generation != c.generation {
		return nil
	}
	c.endGeneration(errUpstreamAlreadyEnded)
	c.stopReportTimer()
	c.reportDeadlineAt = time.Time{}
	c.transitionUnhealthy(externalSystemUnavailableReason)
	return nil
}

// handleReport validates one copied delivery. Retained, wrong-topic,
// wrong-PASSKEY, wrong-station-type, malformed, oversized, over-field-limit,
// duplicate-key, and exact-duplicate reports produce no evidence and refresh
// no freshness deadline.
func (c *runtimeCoordinator) handleReport(event reportReceived) error {
	if event.generation != c.generation || c.generationEnded {
		c.adapter.logIgnoredReport(c.ctx, endedGenerationIgnoredCode, len(event.message.Payload))
		return nil
	}
	message := event.message
	if message.Retained {
		c.adapter.logIgnoredReport(c.ctx, retainedReportIgnoredCode, len(message.Payload))
		return nil
	}
	if message.Topic != c.adapter.config.MQTTTopic {
		c.adapter.logIgnoredReport(c.ctx, unexpectedTopicIgnoredCode, len(message.Payload))
		return nil
	}
	report, err := parseStationReport(message.Payload, message.ReceivedAt, c.adapter.config.ExpectedPasskey)
	if err != nil {
		c.adapter.logRejectedReport(c.ctx, err, len(message.Payload))
		return nil
	}
	signature := newReportSignature(report, message.Payload)
	if c.lastSignature != nil && *c.lastSignature == signature {
		c.adapter.logIgnoredReport(c.ctx, duplicateReportIgnoredCode, len(message.Payload))
		return nil
	}
	c.lastSignature = &signature
	c.lastAcceptedAt = message.ReceivedAt
	c.freshness.noteReport(message.ReceivedAt)
	c.reportDeadlineAt = c.clock.Now().Add(c.reportTimeout())
	c.scheduleReportTimer()
	return c.enqueueWork(workItem{
		kind:             workReport,
		generation:       event.generation,
		report:           report,
		receivedAt:       message.ReceivedAt,
		evidenceRevision: c.evidenceRevision,
	})
}

func (c *runtimeCoordinator) handleReportDeadline(event reportDeadlineReached) error {
	if event.generation != c.generation || c.reportDeadlineAt.IsZero() {
		return nil
	}
	if c.clock.Now().Before(c.reportDeadlineAt) {
		return nil
	}
	c.reportDeadlineAt = time.Time{}
	c.reportTimer = nil
	c.transitionUnhealthy(stationSilentReason)
	return nil
}

func (c *runtimeCoordinator) handleMeasurementDeadline() error {
	if c.health != adapter.HealthHealthy {
		c.refreshMeasurementDeadline()
		return nil
	}
	c.measurementDeadlineAt = time.Time{}
	c.measurementTimer = nil
	if err := c.enqueueWork(workItem{
		kind:             workStaleAvailability,
		generation:       c.generation,
		evidenceRevision: c.evidenceRevision,
	}); err != nil {
		return err
	}
	c.refreshMeasurementDeadline()
	return nil
}

// transitionUnhealthy commits one unhealthy adapter transition, models Core
// clearing every Entity's availability, and reports the change without
// blocking the event loop. Every transition revokes the eligibility of report
// evidence accepted before it, including a re-affirmed silence or disconnect:
// evidence older than the transition may publish its Observations but may not
// restore health or availability.
func (c *runtimeCoordinator) transitionUnhealthy(reason string) {
	c.evidenceRevision++
	if c.health == adapter.HealthUnhealthy && c.healthReason == reason {
		// Core already knows this unhealthy state. If a health submission is
		// still pending, re-affirm unhealthy after it: a healthy decision that
		// was already sent can otherwise leave Core believing the adapter
		// recovered from a silence it never recovered from.
		if c.healthSubmissionsPending() {
			c.enqueueHealth(0, c.unhealthyReport(reason))
		}
		return
	}
	c.health = adapter.HealthUnhealthy
	c.healthReason = reason
	c.adapter.logger.WarnContext(
		c.ctx,
		"Ecowitt adapter unhealthy",
		slog.String(eventKey, "adapter.unhealthy"),
		slog.String(errorCodeKey, reason),
	)
	c.freshness.markUnhealthy()
	c.refreshMeasurementDeadline()
	c.enqueueHealth(0, c.unhealthyReport(reason))
}

// unhealthyReport builds one unhealthy health report with the local monotonic
// observation clock converted to UTC for the SDK wire field.
func (c *runtimeCoordinator) unhealthyReport(reason string) adapter.HealthReport {
	return adapter.HealthReport{
		Status:           adapter.HealthUnhealthy,
		SourceObservedAt: c.clock.Now().UTC(),
		ReasonCode:       reason,
	}
}

// healthSubmissionsPending reports whether a SetHealth call is in flight or
// queued, so an unhealthy transition knows whether it must re-affirm after it.
func (c *runtimeCoordinator) healthSubmissionsPending() bool {
	return c.healthInFlight || len(c.healthQueue) > 0
}

// enqueueHealth revisions one health decision and submits it to the single
// ordered health pipeline. The coordinator never waits for the SDK
// acknowledgement: only one SetHealth call is in flight at a time and the next
// is dispatched from its completion event, so concurrent healthy and unhealthy
// effects cannot reorder. Local health state already reflects the decision, so
// unhealthy intent is prompt even while an earlier call is completing. A
// decision's eligibility is not carried here: it belongs to the report chain
// that queued a recovery, which owns the generation and evidence-revision
// fence.
func (c *runtimeCoordinator) enqueueHealth(sequence uint64, report adapter.HealthReport) {
	c.healthEpoch++
	c.healthQueue = append(c.healthQueue, healthDecision{
		epoch:    c.healthEpoch,
		sequence: sequence,
		report:   report,
	})
	c.pumpHealth()
}

// pumpHealth dispatches the oldest queued health decision when none is in
// flight. It runs on the coordinator goroutine and never blocks.
func (c *runtimeCoordinator) pumpHealth() {
	if c.healthInFlight || len(c.healthQueue) == 0 {
		return
	}
	decision := c.healthQueue[0]
	c.healthQueue[0] = healthDecision{}
	c.healthQueue = c.healthQueue[1:]
	if len(c.healthQueue) == 0 {
		c.healthQueue = nil
	}
	c.healthInFlight = true
	c.effects.Go(func() {
		err := c.adapter.session.SetHealth(c.ctx, decision.report)
		_ = c.submit(c.ctx, healthFinished{
			epoch:    decision.epoch,
			sequence: decision.sequence,
			status:   decision.report.Status,
			err:      err,
		})
	})
}

// handleHealthFinished applies one serialized SetHealth completion. Only the
// newest decision may commit, and a healthy decision for a chain that lost its
// generation or evidence-revision fence never commits: stale evidence cannot
// restore health before a new live report.
func (c *runtimeCoordinator) handleHealthFinished(event healthFinished) error {
	c.healthInFlight = false
	if err := c.terminalError(event.err); err != nil {
		return err
	}
	chain := c.activeWork
	chainMatches := chain != nil && chain.sequence == event.sequence && chain.stage == stageHealth
	committed := false
	// A healthy acknowledgement commits only for the chain that queued it,
	// while that chain is still eligible: the epoch fences ordering, the chain's
	// generation and evidence revision fence which evidence may still claim a
	// recovery. A chain that lost either fence keeps only its Observations.
	if event.err == nil && event.epoch == c.healthEpoch &&
		event.status == adapter.HealthHealthy && chainMatches && c.chainEligible(chain) {
		c.health = adapter.HealthHealthy
		c.healthReason = ""
		committed = true
	}
	if chainMatches {
		chain.healthQueued = false
		if committed {
			c.planChainAvailability(chain)
		} else {
			c.demoteToObservations(chain)
		}
	}
	c.pumpHealth()
	return c.advance()
}

// generationLive reports whether one MQTT generation is the current live
// generation.
func (c *runtimeCoordinator) generationLive(generation uint64) bool {
	return generation == c.generation && !c.generationEnded
}

// chainGenerationLive reports whether a work chain still belongs to the live
// MQTT generation. A dead chain keeps only its already-acquired Observations.
func (c *runtimeCoordinator) chainGenerationLive(chain *workChain) bool {
	return c.generationLive(chain.generation)
}

// chainEvidenceCurrent reports whether a work chain's report evidence was
// accepted after the newest unhealthy transition, so it may still contribute
// health recovery and availability.
func (c *runtimeCoordinator) chainEvidenceCurrent(chain *workChain) bool {
	return chain.evidenceRevision == c.evidenceRevision
}

// chainEligible reports whether a work chain may still contribute health
// recovery and availability. It requires both a live MQTT generation and report
// evidence accepted after the newest unhealthy transition; an ineligible chain
// keeps only its already-acquired Observations, which still drain in order.
func (c *runtimeCoordinator) chainEligible(chain *workChain) bool {
	return c.chainGenerationLive(chain) && c.chainEvidenceCurrent(chain)
}

// terminalError classifies one SDK effect failure. Context cancellation and
// shutdown are not failures; every other non-nil failure is terminal because
// the SDK owns publication retry and never needs a compensating Observation.
func (c *runtimeCoordinator) terminalError(err error) error {
	if err == nil {
		return nil
	}
	if c.ctx.Err() != nil {
		return nil //nolint:nilerr // Shutdown cancels every in-flight effect by design.
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// reportTimeout is the report timeout: exactly three configured upload
// intervals.
func (c *runtimeCoordinator) reportTimeout() time.Duration {
	return reportTimeoutIntervals * c.adapter.config.UploadInterval
}

// scheduleReportTimer arms the report deadline from the monotonic clock.
func (c *runtimeCoordinator) scheduleReportTimer() {
	c.stopReportTimer()
	if c.reportDeadlineAt.IsZero() {
		return
	}
	delay := max(c.reportDeadlineAt.Sub(c.clock.Now()), 0)
	generation := c.generation
	c.reportTimer = c.clock.AfterFunc(delay, func() {
		_ = c.submit(c.ctx, reportDeadlineReached{generation: generation})
	})
}

// stopReportTimer cancels the armed report deadline.
func (c *runtimeCoordinator) stopReportTimer() {
	if c.reportTimer != nil {
		c.reportTimer.Stop()
		c.reportTimer = nil
	}
}

// refreshMeasurementDeadline arms the earliest per-Entity stale deadline. It
// is only armed while the Adapter is healthy, because Core clears availability
// on an unhealthy adapter and a stale report would be rejected. It is not armed
// while a stale recomputation already waits behind the active chain: that work
// will move the deadline forward when it runs, and re-arming an already due
// deadline would spin the event loop instead of draining ordered work.
func (c *runtimeCoordinator) refreshMeasurementDeadline() {
	c.stopMeasurementTimer()
	c.measurementDeadlineAt = time.Time{}
	if c.health != adapter.HealthHealthy || c.hasPendingStale() {
		return
	}
	deadline, ok := c.freshness.deadline(c.adapter.config.UploadInterval)
	if !ok {
		return
	}
	c.measurementDeadlineAt = deadline
	delay := max(deadline.Sub(c.clock.Now()), 0)
	c.measurementTimer = c.clock.AfterFunc(delay, func() {
		_ = c.submit(c.ctx, measurementDeadlineReached{})
	})
}

// stopMeasurementTimer cancels the armed measurement deadline.
func (c *runtimeCoordinator) stopMeasurementTimer() {
	if c.measurementTimer != nil {
		c.measurementTimer.Stop()
		c.measurementTimer = nil
	}
}

// logSupersededWork records that a stale completion or queued item could not
// mutate current coordinator state.
func (c *runtimeCoordinator) logSupersededWork() {
	c.adapter.logger.DebugContext(
		c.ctx,
		"superseded Ecowitt work",
		slog.String(eventKey, "adapter.work_superseded"),
		slog.String(errorCodeKey, supersededWorkErrorCode),
	)
}
