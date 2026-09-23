// runtime.go owns the private serial runtime coordinator: connection
// generations, immutable route revisions, per-node FIFO queues, active
// attempts, protocol request completions, buffered Events, and deadline timers.
//
// The coordinator is the only owner of that state, so one goroutine decides
// every route change, Event disposition, and attempt transition. Blocking
// Session and WebSocket calls run in tracked goroutines and report back as
// events, so the coordinator loop never blocks on the network.

package zwavejs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// Schema-29 node Event names v1 consumes. Controller node added and node
// removed are declared in client.go because the reader normalizes their node ID.
const (
	eventReady              = "ready"
	eventInterviewCompleted = "interview completed"
	eventValueAdded         = "value added"
	eventValueUpdated       = "value updated"
	eventValueRemoved       = "value removed"
	eventMetadataUpdated    = "metadata updated"
	eventWakeUp             = "wake up"
	eventSleep              = "sleep"
	eventAlive              = "alive"
	eventDead               = "dead"
)

// defaultPollHintInterval is the minimum interval between two correlated polls
// of one accepted Command. A later value update is only a wake hint, so it can
// never drive polls faster than this.
const defaultPollHintInterval = 250 * time.Millisecond

// Fixed attempt-abort classifications. None of them reaches a Hearth reason
// code; each is a local transition cause.
var (
	errStaleGeneration  = errors.New("stale Z-Wave runtime generation")
	errStaleRoute       = errors.New("stale Z-Wave route revision")
	errGenerationClosed = errors.New("Z-Wave connection generation is closed")
)

// eventBufferOverflowError reports that Events arrived faster than one
// reconciliation could activate routes. The generation ends so the next
// generation resynchronizes instead of dropping an Event.
type eventBufferOverflowError struct{ Limit int }

func (err *eventBufferOverflowError) Error() string {
	return "zwavejs: buffered Event limit of " + strconv.Itoa(err.Limit) + " reached"
}

// runtimeEvent is one message the serial coordinator accepts.
type runtimeEvent interface{ runtimeEvent() }

// upstreamEvent is one validated upstream Event awaiting the coordinator. It
// carries the identity of the connection generation that produced it, including
// a generation that has not been activated yet, so an overflow can terminate
// exactly that generation instead of guessing through the active generation's
// disconnect.
type upstreamEvent struct {
	generation uint64
	// connection is the connection the Event arrived on. It is closed when an
	// incoming generation's pre-activation Events overflow, so the stale
	// snapshot can never be activated.
	connection zwaveConnection
	// disconnect ends the connection generation this Event came from. It is
	// carried with every pre-activation Event because the coordinator has not
	// installed the incoming generation's disconnect yet when its buffered
	// Events overflow.
	disconnect context.CancelCauseFunc
	event      receivedEvent
}

func (upstreamEvent) runtimeEvent() {}

// reconciledNode is one node of one snapshot together with the routes the
// registration activated. A node the planner rejects keeps its facts and an
// empty route set, so availability can name the exact reason without a
// zero-Entity registration.
type reconciledNode struct {
	nodeID   int
	state    nodeState
	deviceID string
	routes   []entityRoute
}

// reconciliationSubmitted installs one generation's routes.
type reconciliationSubmitted struct {
	generation uint64
	connection zwaveConnection
	disconnect context.CancelCauseFunc
	homeID     uint32
	mappings   []adapter.OwnedMapping
	nodes      []reconciledNode
	observedAt time.Time
	result     chan error
}

func (reconciliationSubmitted) runtimeEvent() {}

// reconciliationCompleted reports that the ordered startup effect finished:
// health acknowledged, fresh availability reported, and snapshot Observations
// published. Only then are live Events and Commands activated.
type reconciliationCompleted struct {
	scope      *generationScope
	generation uint64
	err        error
	result     chan error
}

func (reconciliationCompleted) runtimeEvent() {}

// generationInvalidated ends one connection generation.
type generationInvalidated struct {
	generation uint64
	cause      error
	result     chan error
}

func (generationInvalidated) runtimeEvent() {}

// taskCompleted reports one tracked background effect. Scope and Generation
// identify the generation that started it, so a completion that outlived its
// generation is ignored rather than ending the runtime. A non-nil error that is
// not a context cancellation is a terminal Session failure.
type taskCompleted struct {
	scope      *generationScope
	generation uint64
	err        error
}

func (taskCompleted) runtimeEvent() {}

// mappingID identifies one owned mapping.
type mappingID struct {
	binding string
	entity  string
}

// nodeRecord is the coordinator's state for one Z-Wave node of the active
// generation.
type nodeRecord struct {
	nodeID int
	// present records that the current generation observed this node. A node
	// that was previously known and is now absent reports node_missing.
	present bool
	// assessed records that this node was already known before the active
	// reconciliation. A new node that is still Unknown stays unknown.
	assessed bool
	state    nodeState
	routes   []entityRoute
	// revision is the route revision this node's routes were installed under.
	// Reconciliation assigns it once per connection generation.
	revision uint64
}

// generationScope is the lifetime of one connection generation. Generation-
// scoped effects capture it and run under its context, so ending a generation
// cancels them. The coordinator awaits them before it reports the failure that
// ended the generation, so a stale healthy report, availability batch,
// Observation, or topology update can never follow the unhealthy report.
type generationScope struct {
	ctx     context.Context
	cancel  context.CancelFunc
	effects sync.WaitGroup
}

// newGenerationScope derives one generation's lifetime from the runtime.
func newGenerationScope(parent context.Context) *generationScope {
	ctx, cancel := context.WithCancel(parent)
	return &generationScope{ctx: ctx, cancel: cancel}
}

// ended reports whether the generation has been torn down.
func (scope *generationScope) ended() bool { return scope.ctx.Err() != nil }

// end cancels every generation-scoped effect and waits for it to stop, so the
// caller's next report is the last thing this generation emits.
func (scope *generationScope) end() {
	scope.cancel()
	scope.effects.Wait()
}

// runtimeCoordinator is the serial owner of the command runtime.
type runtimeCoordinator struct {
	adapter *Adapter
	ctx     context.Context
	cancel  context.CancelFunc

	generation   uint64
	dispatchable bool
	reconciling  bool
	connection   zwaveConnection
	disconnect   context.CancelCauseFunc
	homeID       uint32
	home         string

	// scope is the active generation's lifetime. It is nil before the first
	// activation and after the active generation ended.
	scope *generationScope

	// mappings and order are the Adapter-owned directory in owned-mapping
	// order. It survives one generation so availability can report a removed
	// capability or a missing node.
	mappings map[mappingID]adapter.OwnedMapping
	order    []mappingID

	nodes    map[int]*nodeRecord
	snapshot routeSnapshot

	routeRevision uint64

	queues          map[int]*nodeCommandQueue
	attempts        map[uint64]*commandAttempt
	attemptsByValue map[attemptValueKey]*commandAttempt
	nextAttemptID   uint64

	// publishTail is the tail of the shared ordinary and linked Observation
	// publication chain. Each frame's Observations stay together, and later
	// publications wait for earlier ones so Core sees State in receive order.
	publishTail chan struct{}

	buffered []upstreamEvent

	// terminatedGenerations records generations whose pre-activation Events
	// overflowed the runtime. Such a generation is already ended, so a late
	// reconciliation must be refused instead of installing its stale snapshot.
	terminatedGenerations map[uint64]struct{}

	effects sync.WaitGroup
}

// newRuntimeCoordinator builds the coordinator for one Adapter run.
func newRuntimeCoordinator(ctx context.Context, zwave *Adapter) *runtimeCoordinator {
	runtimeContext, cancel := context.WithCancel(ctx)
	return &runtimeCoordinator{
		adapter:               zwave,
		ctx:                   runtimeContext,
		cancel:                cancel,
		mappings:              make(map[mappingID]adapter.OwnedMapping),
		nodes:                 make(map[int]*nodeRecord),
		queues:                make(map[int]*nodeCommandQueue),
		attempts:              make(map[uint64]*commandAttempt),
		attemptsByValue:       make(map[attemptValueKey]*commandAttempt),
		terminatedGenerations: make(map[uint64]struct{}),
	}
}

// run is the coordinator loop. It ends when the runtime context ends or when
// one event fails terminally.
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

// stop cancels the runtime, drains tracked effects, and publishes the terminal
// signal every Adapter call waits on.
func (coordinator *runtimeCoordinator) stop(err error) error {
	coordinator.cancel()
	coordinator.shutdown(err)
	close(coordinator.adapter.runtimeDone)
	return err
}

// shutdown ends every route and every attempt without waiting on the network.
func (coordinator *runtimeCoordinator) shutdown(cause error) {
	coordinator.dispatchable = false
	coordinator.reconciling = false
	coordinator.clearRoutes()
	coordinator.connection = nil
	coordinator.disconnect = nil
	coordinator.endGenerationScope()
	if cause == nil {
		cause = errGenerationClosed
	}
	coordinator.invalidateAllAttempts(cause)
	coordinator.effects.Wait()
}

// endGenerationScope tears down the active generation's effects and forgets the
// scope, so a result they already queued is dropped by its scope comparison. The
// ordinary-publication chain also starts over: a frame whose task was skipped
// because the generation ended may never close its link, and a later generation
// must never wait on it.
func (coordinator *runtimeCoordinator) endGenerationScope() {
	scope := coordinator.scope
	coordinator.scope = nil
	coordinator.publishTail = nil
	if scope != nil {
		scope.end()
	}
}

// effectContext is the context a generation-scoped effect runs under: the
// generation's lifetime when it has one, otherwise the runtime's.
func (coordinator *runtimeCoordinator) effectContext(scope *generationScope) context.Context {
	if scope == nil {
		return coordinator.ctx
	}
	return scope.ctx
}

// startGenerationEffect runs one tracked effect bound to a generation's
// lifetime. Its completion is delivered only while the generation is still
// active: a generation that ended drops the result instead of reporting stale
// health, availability, Observations, or topology.
func (coordinator *runtimeCoordinator) startGenerationEffect(
	scope *generationScope,
	effect func() runtimeEvent,
) {
	if scope == nil {
		coordinator.startEffect(effect)
		return
	}
	scope.effects.Add(1)
	coordinator.effects.Go(func() {
		defer scope.effects.Done()
		if scope.ended() {
			return
		}
		coordinator.sendScopedCompletion(scope, effect())
	})
}

// startGenerationTask runs one tracked generation-scoped task that needs no
// coordinator transition.
func (coordinator *runtimeCoordinator) startGenerationTask(
	scope *generationScope,
	task func(),
) {
	if scope == nil {
		coordinator.startTask(task)
		return
	}
	scope.effects.Add(1)
	coordinator.effects.Go(func() {
		defer scope.effects.Done()
		if scope.ended() {
			return
		}
		task()
	})
}

// sendScopedCompletion delivers one generation-scoped completion, unless the
// generation ended first. Giving up on a canceled scope is what keeps
// endGenerationScope from deadlocking on a full event queue.
func (coordinator *runtimeCoordinator) sendScopedCompletion(
	scope *generationScope,
	event runtimeEvent,
) {
	select {
	case coordinator.adapter.runtimeEvents <- event:
	case <-scope.ctx.Done():
	case <-coordinator.ctx.Done():
	case <-coordinator.adapter.runtimeDone:
	}
}

// startEffect runs one tracked background effect and delivers its completion to
// the coordinator loop.
func (coordinator *runtimeCoordinator) startEffect(effect func() runtimeEvent) {
	coordinator.effects.Go(func() {
		coordinator.sendCompletion(effect())
	})
}

// startTask runs one tracked background effect that needs no coordinator
// transition. It is used for ordinary Observation publication, where a failure
// is a diagnostic rather than protocol state.
func (coordinator *runtimeCoordinator) startTask(task func()) {
	coordinator.effects.Go(task)
}

// sendCompletion delivers one effect completion unless the runtime already
// stopped.
func (coordinator *runtimeCoordinator) sendCompletion(event runtimeEvent) {
	select {
	case coordinator.adapter.runtimeEvents <- event:
	case <-coordinator.ctx.Done():
	case <-coordinator.adapter.runtimeDone:
	}
}

// handle dispatches one runtime event.
func (coordinator *runtimeCoordinator) handle(event runtimeEvent) error {
	switch event := event.(type) {
	case upstreamEvent:
		return coordinator.acceptUpstreamEvent(event)
	case reconciliationSubmitted:
		coordinator.activateReconciliation(event)
	case reconciliationCompleted:
		return coordinator.finishReconciliation(event)
	case generationInvalidated:
		coordinator.invalidateGeneration(event)
	case commandSubmitted:
		coordinator.submitCommand(event)
	case setValueCompleted:
		coordinator.finishSetValue(event)
	case pollValueCompleted:
		coordinator.finishPollValue(event)
	case linkedPublishCompleted:
		coordinator.finishLinkedPublish(event)
	case attemptDeadlineReached:
		coordinator.reachDeadline(event.attemptID)
	case pollTimerFired:
		coordinator.firePollTimer(event.attemptID)
	case taskCompleted:
		// A completion that outlived its generation, or that only reports a
		// canceled operation, is not this generation's failure. A canceled
		// availability report is what tearing a generation down produces, and it
		// must never stop the runtime.
		if event.scope != coordinator.scope || event.generation != coordinator.generation {
			return nil
		}
		if event.err != nil && !isContextCancellation(event.err) {
			return event.err
		}
	}
	return nil
}

// finishReconciliation acknowledges recovery only after reports and buffered
// Events have left the generation dispatchable.
func (coordinator *runtimeCoordinator) finishReconciliation(event reconciliationCompleted) error {
	if event.scope != coordinator.scope || event.generation != coordinator.generation {
		return nil
	}
	coordinator.reconciling = false
	if event.err != nil {
		if event.result != nil {
			event.result <- event.err
		}
		return event.err
	}
	coordinator.dispatchable = true
	coordinator.replayBuffered()
	if !coordinator.dispatchable {
		// A buffered topology Event invalidated the snapshot before recovery.
		if event.result != nil {
			event.result <- errStaleGeneration
		}
		return nil
	}
	coordinator.logReconcileCompleted()
	if event.result != nil {
		event.result <- nil
	}
	return nil
}

// acceptUpstreamEvent buffers or handles one upstream Event. An Event from an
// older generation is dropped; an Event that arrives before routes are active
// is buffered so it is replayed, never silently lost.
func (coordinator *runtimeCoordinator) acceptUpstreamEvent(event upstreamEvent) error {
	if event.generation < coordinator.generation {
		return nil
	}
	if !coordinator.dispatchable || event.generation > coordinator.generation {
		if len(coordinator.buffered) >= bufferedEventLimit {
			coordinator.adapter.logger.WarnContext(
				coordinator.ctx,
				"Z-Wave JS Event buffer overflowed",
				slog.String(eventKey, "adapter.event_buffer_overflowed"),
				slog.Int("event_limit", bufferedEventLimit),
			)
			coordinator.buffered = nil
			coordinator.overflowBufferedEvents(event)
			return nil
		}
		coordinator.buffered = append(coordinator.buffered, event)
		return nil
	}
	coordinator.handleUpstreamEvent(event.event)
	return nil
}

// overflowBufferedEvents ends the generation whose pre-activation Events
// overflowed the runtime. The active generation is dropped through its installed
// disconnect, and an incoming generation that has not been activated yet is
// terminated through the cancellation and connection identity its Events
// carried, because dropGeneration can only reach the installed disconnect. The
// terminated generation is remembered so a late reconciliation can never
// install the stale snapshot its dropped Events described.
func (coordinator *runtimeCoordinator) overflowBufferedEvents(event upstreamEvent) {
	cause := &eventBufferOverflowError{Limit: bufferedEventLimit}
	if event.generation > coordinator.generation {
		coordinator.terminatedGenerations[event.generation] = struct{}{}
		if event.disconnect != nil {
			event.disconnect(cause)
		}
		if event.connection != nil {
			event.connection.Close()
		}
	}
	coordinator.dropGeneration(cause)
}

// replayBuffered handles every Event that arrived while reconciliation was in
// progress, in arrival order, and drops anything from an older generation.
func (coordinator *runtimeCoordinator) replayBuffered() {
	buffered := coordinator.buffered
	coordinator.buffered = nil
	for _, event := range buffered {
		if event.generation != coordinator.generation {
			continue
		}
		coordinator.handleUpstreamEvent(event.event)
	}
}

// dropBufferedEvents forgets the buffered Events of one generation.
func (coordinator *runtimeCoordinator) dropBufferedEvents(generation uint64) {
	kept := coordinator.buffered[:0]
	for _, event := range coordinator.buffered {
		if event.generation != generation {
			kept = append(kept, event)
		}
	}
	coordinator.buffered = kept
}

// activateReconciliation installs one generation's immutable routes and starts
// the ordered startup effect: healthy, then fresh availability, then snapshot
// Observations.
func (coordinator *runtimeCoordinator) activateReconciliation(event reconciliationSubmitted) {
	if event.generation < coordinator.generation {
		event.result <- errStaleGeneration
		return
	}
	// A generation whose pre-activation Events overflowed the runtime is already
	// ended. Its snapshot is stale by definition, so it must never install routes
	// or publish State from the events that were dropped.
	if _, terminated := coordinator.terminatedGenerations[event.generation]; terminated {
		delete(coordinator.terminatedGenerations, event.generation)
		event.result <- &eventBufferOverflowError{Limit: bufferedEventLimit}
		return
	}
	coordinator.terminatedGenerations = make(map[uint64]struct{})
	coordinator.endGenerationScope()
	coordinator.scope = newGenerationScope(coordinator.ctx)
	coordinator.dispatchable = false
	coordinator.reconciling = true
	coordinator.generation = event.generation
	coordinator.connection = event.connection
	coordinator.disconnect = event.disconnect
	coordinator.homeID = event.homeID
	coordinator.home = normalizedHomeID(event.homeID)
	coordinator.invalidateAllAttempts(errStaleGeneration)
	coordinator.routeRevision = 0
	coordinator.snapshot = routeSnapshot{}
	coordinator.queues = make(map[int]*nodeCommandQueue)

	coordinator.rememberMappings(event.mappings)
	coordinator.nodes = make(map[int]*nodeRecord, len(event.nodes))
	for _, node := range event.nodes {
		coordinator.nodes[node.nodeID] = &nodeRecord{
			nodeID:   node.nodeID,
			present:  true,
			assessed: coordinator.nodeHasMappings(node.nodeID),
			state:    node.state,
		}
	}
	for _, node := range event.nodes {
		// Record only the filtered routable routes, so an ineligible node keeps
		// its facts and mappings but installs no route and publishes no snapshot
		// State.
		routes := routableRoutes(node.state, node.routes)
		coordinator.nodes[node.nodeID].routes = routes
		if len(routes) == 0 {
			continue
		}
		coordinator.routeRevision++
		coordinator.nodes[node.nodeID].revision = coordinator.routeRevision
	}
	if err := coordinator.rebuildSnapshot(); err != nil {
		coordinator.reconciling = false
		event.result <- err
		return
	}
	for _, node := range event.nodes {
		coordinator.rememberNodeRoutes(node)
	}
	observations := coordinator.snapshotObservations(event.nodes, event.observedAt)
	reports := coordinator.availabilityReports(event.observedAt)
	coordinator.startReconciliationEffect(
		coordinator.scope,
		event.generation,
		reports,
		observations,
		event.result,
	)
}

// startReconciliationEffect performs one generation's startup Session calls in
// order. Routes stay undispatched until every call succeeded, so health is
// always acknowledged before any availability or State report. The effect runs
// under the generation's scope, so ending the generation cancels it before the
// failure report that follows.
func (coordinator *runtimeCoordinator) startReconciliationEffect(
	scope *generationScope,
	generation uint64,
	reports []adapter.EntityAvailabilityReport,
	observations []entityObservation,
	result chan error,
) {
	coordinator.startGenerationEffect(scope, func() runtimeEvent {
		ctx := coordinator.effectContext(scope)
		if err := coordinator.adapter.session.SetHealth(ctx, adapter.HealthReport{
			Status:           adapter.HealthHealthy,
			SourceObservedAt: time.Now().UTC(),
		}); err != nil {
			return reconciliationCompleted{
				scope: scope, generation: generation, result: result,
				err: &sessionOperationError{
					operation: "report healthy Z-Wave JS connection",
					err:       err,
				},
			}
		}
		if scope.ended() {
			return reconciliationCompleted{scope: scope, generation: generation, result: result}
		}
		if err := coordinator.adapter.reportAvailability(ctx, reports); err != nil {
			return reconciliationCompleted{scope: scope, generation: generation, result: result, err: err}
		}
		for _, observation := range observations {
			if scope.ended() {
				return reconciliationCompleted{scope: scope, generation: generation, result: result}
			}
			if _, err := coordinator.adapter.session.PublishObservation(
				ctx,
				observation.Observation,
			); err != nil {
				return reconciliationCompleted{
					scope: scope, generation: generation, result: result,
					err: &sessionOperationError{
						operation: "publish Z-Wave Observation",
						err:       err,
					},
				}
			}
		}
		return reconciliationCompleted{scope: scope, generation: generation, result: result}
	})
}

// invalidateGeneration ends one generation: routes first, then every attempt
// that was never accepted.
func (coordinator *runtimeCoordinator) invalidateGeneration(event generationInvalidated) {
	if event.generation < coordinator.generation {
		event.result <- nil
		return
	}
	coordinator.endGenerationScope()
	coordinator.dispatchable = false
	coordinator.reconciling = false
	coordinator.clearRoutes()
	coordinator.connection = nil
	coordinator.disconnect = nil
	coordinator.generation = event.generation
	coordinator.dropBufferedEvents(event.generation)
	cause := event.cause
	if cause == nil {
		cause = errGenerationClosed
	}
	coordinator.invalidateAllAttempts(cause)
	event.result <- nil
}

// dropGeneration ends the active generation from inside the coordinator, for
// example after a write or read failure.
func (coordinator *runtimeCoordinator) dropGeneration(cause error) {
	coordinator.dispatchable = false
	coordinator.connection = nil
	disconnect := coordinator.disconnect
	coordinator.disconnect = nil
	coordinator.clearRoutes()
	coordinator.endGenerationScope()
	if disconnect != nil {
		disconnect(cause)
	}
	coordinator.invalidateAllAttempts(cause)
}

// clearRoutes removes every installed route and invalidates the snapshot.
func (coordinator *runtimeCoordinator) clearRoutes() {
	for _, record := range coordinator.nodes {
		record.routes = nil
		record.revision = 0
	}
	coordinator.snapshot = routeSnapshot{}
}

// invalidateAllAttempts aborts every in-flight attempt. An unaccepted attempt
// still inside its deadline is rejected as unavailable; an accepted attempt
// receives no second response and simply ends.
func (coordinator *runtimeCoordinator) invalidateAllAttempts(cause error) {
	for _, attempt := range slices.Collect(maps.Values(coordinator.attempts)) {
		coordinator.abortAttempt(attempt, cause)
	}
}

// abortAttempt ends one attempt, responding unavailable only while it has not
// been accepted and its deadline is still open.
func (coordinator *runtimeCoordinator) abortAttempt(attempt *commandAttempt, cause error) {
	coordinator.abortAttemptInternal(attempt, cause, true)
}

// abortAttemptInternal ends one attempt and optionally advances its node FIFO.
// Queue draining disables nested advancement so a long run of expired attempts
// cannot recurse once per entry.
func (coordinator *runtimeCoordinator) abortAttemptInternal(
	attempt *commandAttempt,
	cause error,
	advance bool,
) {
	if !attempt.accepted && !attempt.handlerDone && time.Now().Before(attempt.deadline) {
		if err := attempt.responder.RejectUnavailable("Z-Wave Entity is unavailable"); err != nil {
			coordinator.finishAttemptInternal(attempt, err, advance)
			return
		}
	}
	coordinator.finishAttemptInternal(attempt, cause, advance)
}

// attemptRouteIsCurrent reports whether one attempt may still dispatch through
// its recorded generation and route revision.
func (coordinator *runtimeCoordinator) attemptRouteIsCurrent(attempt *commandAttempt) bool {
	if !coordinator.dispatchable || attempt.generation != coordinator.generation {
		return false
	}
	record := coordinator.nodes[attempt.nodeID]
	if record == nil || record.revision != attempt.routeRevision {
		return false
	}
	current, exists := coordinator.snapshot.ByEntityID[attempt.entityID]
	if !exists {
		return false
	}
	return current.Plan.TargetValueID.valueKey() == attempt.route.Plan.TargetValueID.valueKey()
}

// routableRoutes applies v1 route eligibility. Only Awake and Alive nodes
// receive Command routes; every other status keeps its mappings for availability
// reporting but cannot accept Commands through a stale or non-live route.
func routableRoutes(state nodeState, routes []entityRoute) []entityRoute {
	if state.Status != nodeStatusAwake && state.Status != nodeStatusAlive {
		return nil
	}
	return routes
}

// rebuildSnapshot reindexes the immutable route table from the node records.
func (coordinator *runtimeCoordinator) rebuildSnapshot() error {
	routes := make([]entityRoute, 0, len(coordinator.snapshot.ByEntityID))
	for _, nodeID := range slices.Sorted(maps.Keys(coordinator.nodes)) {
		routes = append(routes, coordinator.nodes[nodeID].routes...)
	}
	snapshot, err := newRouteSnapshot(coordinator.generation, coordinator.routeRevision, routes)
	if err != nil {
		return err
	}
	coordinator.snapshot = snapshot
	return nil
}

// snapshotObservations translates every planned node's snapshot Values into
// typed Observations in registration order, power before brightness per
// endpoint. It reads each node's active routes from the coordinator, so only the
// filtered routable route set produces State: an ineligible node that retains
// facts and mappings but installs no route never publishes a snapshot
// Observation.
func (coordinator *runtimeCoordinator) snapshotObservations(
	nodes []reconciledNode,
	observedAt time.Time,
) []entityObservation {
	observations := make([]entityObservation, 0)
	for _, node := range nodes {
		record := coordinator.nodes[node.nodeID]
		if record == nil || len(record.routes) == 0 {
			continue
		}
		published, issues := translateNodeValues(record.routes, observedAt, record.state.Values)
		observations = append(observations, published...)
		for _, issue := range issues {
			coordinator.logStateUnrepresentable(issue)
		}
	}
	return observations
}

// rememberMappings adds owned mappings to the directory in first-seen order.
func (coordinator *runtimeCoordinator) rememberMappings(mappings []adapter.OwnedMapping) {
	for _, mapping := range mappings {
		coordinator.rememberMapping(mapping)
	}
}

// rememberMapping adds one owned mapping, preserving owned-mapping order.
func (coordinator *runtimeCoordinator) rememberMapping(mapping adapter.OwnedMapping) {
	key := mappingID{binding: mapping.BindingKey, entity: mapping.EntityKey}
	if _, exists := coordinator.mappings[key]; !exists {
		coordinator.order = append(coordinator.order, key)
	}
	coordinator.mappings[key] = mapping
}

// rememberNodeRoutes records the canonical Entity IDs of one node's successful
// registration so a later removal can report them missing.
func (coordinator *runtimeCoordinator) rememberNodeRoutes(node reconciledNode) {
	binding := nodeBindingKey(coordinator.home, node.nodeID)
	for _, route := range node.routes {
		coordinator.rememberMapping(adapter.OwnedMapping{
			BindingKey: binding,
			DeviceID:   node.deviceID,
			EntityKey:  route.Plan.Key,
			EntityID:   route.EntityID,
		})
	}
}

// nodeHasMappings reports whether the directory already holds a mapping for one
// node, which is what makes a node previously assessed.
func (coordinator *runtimeCoordinator) nodeHasMappings(nodeID int) bool {
	for _, key := range coordinator.order {
		if mappingNodeID, ok := bindingKeyNodeID(key.binding); ok && mappingNodeID == nodeID {
			return true
		}
	}
	return false
}

// handleUpstreamEvent keeps ordinary value updates live and recycles the
// connection for every known topology or node-state Event. A fresh
// start_listening snapshot is the only authority for routes and availability.
func (coordinator *runtimeCoordinator) handleUpstreamEvent(received receivedEvent) {
	event := received.Event
	if event.Event.Source == eventSourceNode && event.Event.Event == eventValueUpdated {
		coordinator.handleValueUpdated(received)
		return
	}
	if event.Event.Source == eventSourceNode && event.Event.Event == eventValueAdded {
		args, ok := decodeValueEventArgs(event.Event.Args)
		if ok && valueAddedChangesSupportedPlan(args.valueID) && event.Event.NodeID > 0 {
			coordinator.recycleGeneration(event)
			return
		}
		coordinator.logIgnoredEvent(event)
		return
	}
	switch event.Event.Source {
	case eventSourceController:
		if event.Event.Event == eventNodeAdded || event.Event.Event == eventNodeRemoved {
			coordinator.recycleGeneration(event)
			return
		}
	case eventSourceNode:
		if event.Event.NodeID > 0 {
			switch event.Event.Event {
			case eventReady, eventInterviewCompleted, eventValueRemoved,
				eventMetadataUpdated, eventWakeUp, eventSleep, eventAlive, eventDead:
				coordinator.recycleGeneration(event)
				return
			}
		}
	}
	coordinator.logIgnoredEvent(event)
}

// valueAddedChangesSupportedPlan reports whether one value added Event can add
// a slot to a supported Binary Switch or Multilevel Switch plan. Either slot
// can matter on its own because a later Event may complete the pair. Numeric,
// keyed, malformed, and negative-endpoint Value IDs cannot be planned.
func valueAddedChangesSupportedPlan(id valueID) bool {
	if id.Endpoint < 0 {
		return false
	}
	if _, ok := plannedValueProperty(id); !ok {
		return false
	}
	switch id.CommandClass {
	case commandClassBinarySwitch, commandClassMultilevelSwitch:
		return true
	default:
		return false
	}
}

// recycleGeneration closes the active connection and invalidates its routes
// before queued Commands can be dispatched against a topology that changed.
func (coordinator *runtimeCoordinator) recycleGeneration(event serverEvent) {
	coordinator.adapter.logger.DebugContext(
		coordinator.ctx,
		"recycling Z-Wave JS generation after topology Event",
		slog.String(eventKey, "adapter.topology_changed"),
		slog.String("event_source", event.Event.Source),
	)
	coordinator.dropGeneration(errors.New("Z-Wave JS topology changed"))
}

// handleValueUpdated publishes ordinary State and uses matching updates only as
// coalesced poll hints for accepted Commands. The Event is never linked evidence.
func (coordinator *runtimeCoordinator) handleValueUpdated(received receivedEvent) {
	args, ok := decodeValueEventArgs(received.Event.Event.Args)
	if !ok {
		coordinator.logIgnoredEvent(received.Event)
		return
	}
	nodeID := received.Event.Event.NodeID
	if len(args.NewValue) > 0 {
		coordinator.publishValueObservationFor(received, args)
	}
	attempt := coordinator.attemptsByValue[attemptValueKey{nodeID: nodeID, value: args.valueID.valueKey()}]
	if attempt == nil || attempt.phase != phaseAccepted {
		return
	}
	attempt.hintPending = true
	coordinator.scheduleHintPoll(attempt)
}

// publishValueObservationFor publishes one already-decoded value frame against
// the active route snapshot, in plan order and through one tracked effect so a
// frame that maps to both power and brightness always publishes power first.
// Nothing is published for a Value the active routes do not plan.
func (coordinator *runtimeCoordinator) publishValueObservationFor(
	received receivedEvent,
	args valueEventArgs,
) {
	nodeID := received.Event.Event.NodeID
	observations := make([]adapter.Observation, 0, maximumEndpointCapabilities)
	for _, route := range coordinator.snapshot.routesForValue(nodeID, args.valueID) {
		observation, err := route.Plan.observe(route.EntityID, received.ReceivedAt, args.NewValue)
		if err != nil {
			coordinator.logStateUnrepresentable(entityStateIssue{
				Key:      route.Plan.Key,
				EntityID: route.EntityID,
				Err:      err,
			})
			continue
		}
		observations = append(observations, observation)
	}
	if len(observations) == 0 {
		return
	}
	coordinator.publishOrdinary(observations)
}

// publishOrdinary publishes one frame's Observations in order. One frame shares
// one tracked effect, so power still precedes brightness, and every later frame
// waits for the previous frame's publication, so Core observes live State in
// receive order even when the first publication is slow.
func (coordinator *runtimeCoordinator) publishOrdinary(observations []adapter.Observation) {
	scope := coordinator.scope
	ctx := coordinator.effectContext(scope)
	previous, next := coordinator.reserveObservationPublication()
	coordinator.startGenerationTask(scope, func() {
		defer close(next)
		if !waitForObservationPublication(ctx, previous) {
			return
		}
		for _, observation := range observations {
			if scope != nil && scope.ended() {
				return
			}
			if _, err := coordinator.adapter.session.PublishObservation(
				ctx,
				observation,
			); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				coordinator.adapter.logger.WarnContext(
					coordinator.ctx,
					"Z-Wave Observation publication failed",
					slog.String(eventKey, "adapter.observation_failed"),
					slog.String("entity_id", observation.EntityID),
					slog.String("error_code", codeSessionOperationFailed),
				)
			}
		}
	})
}

// reserveObservationPublication adds one ordinary or command-linked
// Observation effect to the active generation's shared FIFO publication chain.
func (coordinator *runtimeCoordinator) reserveObservationPublication() (<-chan struct{}, chan struct{}) {
	previous := coordinator.publishTail
	next := make(chan struct{})
	coordinator.publishTail = next
	return previous, next
}

// waitForObservationPublication waits for the previous serialized publication,
// or reports that this effect's lifetime ended before it could publish.
func waitForObservationPublication(ctx context.Context, previous <-chan struct{}) bool {
	if previous == nil {
		return ctx.Err() == nil
	}
	select {
	case <-previous:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

// logReconcileCompleted summarizes one activated connection generation. Counts
// come from the activated route snapshot, and a supported Entity count of zero
// is explicit, so an empty network is distinguishable from a failed reconcile.
func (coordinator *runtimeCoordinator) logReconcileCompleted() {
	isolated := 0
	for _, record := range coordinator.nodes {
		if len(record.routes) == 0 {
			isolated++
		}
	}
	coordinator.adapter.logReconcileCompleted(
		coordinator.ctx,
		len(coordinator.snapshot.ByEntityID),
		isolated,
	)
}

// logIgnoredEvent records one unconsumed Event with a bounded diagnostic. No
// Event arguments, Value IDs, or node names are logged.
func (coordinator *runtimeCoordinator) logIgnoredEvent(event serverEvent) {
	coordinator.adapter.logger.DebugContext(
		coordinator.ctx,
		"ignored Z-Wave JS Event",
		slog.String(eventKey, "adapter.event_ignored"),
		slog.String("event_source", event.Event.Source),
	)
}

// logStateUnrepresentable records one Entity whose upstream Value is not a
// representable State. Valid siblings still publish.
func (coordinator *runtimeCoordinator) logStateUnrepresentable(issue entityStateIssue) {
	coordinator.adapter.logger.DebugContext(
		coordinator.ctx,
		"ignored unrepresentable Z-Wave State",
		slog.String(eventKey, "adapter.state_ignored"),
		slog.String("entity_id", issue.EntityID),
		slog.String("error_code", codeStateUnrepresentable),
	)
}

// valueEventArgs is the arguments payload of a value add or value update Event.
type valueEventArgs struct {
	valueID

	NewValue json.RawMessage `json:"newValue"`
}

// decodeValueEventArgs decodes the Value ID and new value of one value Event.
// An argument payload that is absent or not an object is not a State report. A
// keyed Value ID is rejected: planning never accepts a propertyKey, so a keyed
// Event must never alias the unkeyed route or Command hint that shares its
// Command Class, endpoint, and property. An explicit JSON null key counts as
// absent.
func decodeValueEventArgs(raw json.RawMessage) (valueEventArgs, bool) {
	if len(raw) == 0 {
		return valueEventArgs{}, false
	}
	var args valueEventArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return valueEventArgs{}, false
	}
	if args.CommandClass <= 0 || args.Property.Numeric || args.Property.Name == "" ||
		valueIDHasPropertyKey(args.valueID) {
		return valueEventArgs{}, false
	}
	return args, true
}
