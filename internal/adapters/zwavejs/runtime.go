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
	"sync/atomic"
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

// defaultNodeRefreshTimeout bounds one correlated node.get_state inventory read.
//
// A refresh serializes one node's routes: while it is unanswered, the node keeps
// whatever routes it already had, so a server that never answers would preserve
// stale routes for the whole lifetime of the generation. The bound is
// deliberately generous for a loopback WebSocket inventory read, because a
// timeout ends the generation instead of leaving the node in doubt. Only the
// inventory read is bounded by this elapsed timeout: an SDK Register call has no
// similar per-request deadline, because a first registration may legitimately
// outlive one node read and its own lifetime is the generation.
const defaultNodeRefreshTimeout = 10 * time.Second

// Fixed attempt-abort classifications. None of them reaches a Hearth reason
// code; each is a local transition cause.
var (
	errStaleGeneration  = errors.New("stale Z-Wave runtime generation")
	errStaleRoute       = errors.New("stale Z-Wave route revision")
	errNodeUnroutable   = errors.New("Z-Wave node is not routable")
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
}

func (reconciliationCompleted) runtimeEvent() {}

// generationInvalidated ends one connection generation.
type generationInvalidated struct {
	generation uint64
	cause      error
	result     chan error
}

func (generationInvalidated) runtimeEvent() {}

// nodeRefreshCompleted reports one correlated node.get_state refresh and, when
// the refreshed node is still eligible, its successful re-registration.
// Revision is the node's refresh revision when the refresh was dispatched: a
// refresh whose revision a later Event or invalidation superseded describes
// inventory that is no longer authoritative and must not install routes.
type nodeRefreshCompleted struct {
	scope      *generationScope
	generation uint64
	nodeID     int
	revision   uint64
	observedAt time.Time
	state      nodeState
	deviceID   string
	routes     []entityRoute
	rejection  *nodeRejection
	err        error
}

func (nodeRefreshCompleted) runtimeEvent() {}

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
	// It changes only when the route set actually changes, so an identical
	// refresh never invalidates a queued attempt.
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

	refreshInFlight map[int]bool
	refreshPending  map[int]bool

	// nodeRefreshRevision holds one atomic refresh revision per node. Every Event
	// or invalidation that supersedes an in-flight refresh advances it, so a
	// completion can be recognized as stale even inside one generation: a
	// node.get_state issued before a sleep, removal, or topology Event must not
	// reinstall routes afterwards. The counter is atomic because a refresh effect
	// reads it, before it registers and reports its outcome, while the
	// coordinator may advance it.
	nodeRefreshRevision map[int]*atomic.Uint64

	// availabilityChain serializes one node's availability reports: each
	// request waits for the previous one to finish, so a later report always
	// lands after an earlier one.
	availabilityChain map[int]chan struct{}

	// publishTail is the tail of the ordinary Observation publication chain. One
	// frame's Observations are published together and the next frame waits for
	// the previous one, so Core sees live State in receive order while power
	// still precedes brightness within one frame.
	publishTail chan struct{}

	buffered []upstreamEvent

	// terminatedGenerations records generations whose pre-activation Events
	// overflowed the runtime. Such a generation is already ended, so a late
	// reconciliation must be refused instead of installing its stale snapshot.
	terminatedGenerations map[uint64]struct{}

	// pendingValueAdded holds, per node, the newest value added or value updated
	// report of every Value that arrived before any active route could project it.
	// A frame that creates a new plan is replayed only after a successful refresh
	// installs the route it needs, so its State is not lost and its Adapter
	// receive time is preserved. A newer report of the same exact unkeyed Value
	// supersedes the queued one, so replay never publishes stale State.
	pendingValueAdded map[int][]receivedEvent

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
		refreshInFlight:       make(map[int]bool),
		refreshPending:        make(map[int]bool),
		nodeRefreshRevision:   make(map[int]*atomic.Uint64),
		availabilityChain:     make(map[int]chan struct{}),
		terminatedGenerations: make(map[uint64]struct{}),
		pendingValueAdded:     make(map[int][]receivedEvent),
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
	clear(coordinator.pendingValueAdded)
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
		if event.scope != coordinator.scope || event.generation != coordinator.generation {
			return nil
		}
		coordinator.reconciling = false
		if event.err != nil {
			return event.err
		}
		coordinator.dispatchable = true
		coordinator.replayBuffered()
		// The reconciliation is only complete once health, availability, and
		// snapshot State are acknowledged and live Events and Commands are
		// activated.
		coordinator.logReconcileCompleted()
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
	case nodeRefreshCompleted:
		return coordinator.applyNodeRefresh(event)
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
	coordinator.refreshInFlight = make(map[int]bool)
	coordinator.refreshPending = make(map[int]bool)
	coordinator.nodeRefreshRevision = make(map[int]*atomic.Uint64)
	coordinator.availabilityChain = make(map[int]chan struct{})
	coordinator.pendingValueAdded = make(map[int][]receivedEvent)

	coordinator.rememberMappings(event.mappings)
	coordinator.nodes = make(map[int]*nodeRecord, len(event.nodes))
	for _, node := range event.nodes {
		coordinator.nodes[node.nodeID] = &nodeRecord{
			nodeID:   node.nodeID,
			present:  true,
			assessed: coordinator.nodeHasMappings(node.nodeID),
			state:    node.state,
			routes:   node.routes,
		}
	}
	for _, node := range event.nodes {
		routes := routableRoutes(node.state, node.routes)
		if len(routes) == 0 {
			continue
		}
		coordinator.routeRevision++
		coordinator.nodes[node.nodeID].revision = coordinator.routeRevision
		coordinator.nodes[node.nodeID].routes = routes
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
	event.result <- nil
	coordinator.startReconciliationEffect(coordinator.scope, event.generation, reports, observations)
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
) {
	coordinator.startGenerationEffect(scope, func() runtimeEvent {
		ctx := coordinator.effectContext(scope)
		if err := coordinator.adapter.session.SetHealth(ctx, adapter.HealthReport{
			Status:           adapter.HealthHealthy,
			SourceObservedAt: time.Now().UTC(),
		}); err != nil {
			return reconciliationCompleted{scope: scope, generation: generation, err: &sessionOperationError{
				operation: "report healthy Z-Wave JS connection",
				err:       err,
			}}
		}
		if scope.ended() {
			return reconciliationCompleted{scope: scope, generation: generation}
		}
		if err := coordinator.adapter.reportAvailability(ctx, reports); err != nil {
			return reconciliationCompleted{scope: scope, generation: generation, err: err}
		}
		for _, observation := range observations {
			if scope.ended() {
				return reconciliationCompleted{scope: scope, generation: generation}
			}
			if _, err := coordinator.adapter.session.PublishObservation(
				ctx,
				observation.Observation,
			); err != nil {
				return reconciliationCompleted{scope: scope, generation: generation, err: &sessionOperationError{
					operation: "publish Z-Wave Observation",
					err:       err,
				}}
			}
		}
		return reconciliationCompleted{scope: scope, generation: generation}
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
	clear(coordinator.pendingValueAdded)
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
	clear(coordinator.pendingValueAdded)
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

// invalidateNodeAttempts ends every attempt of one node that can no longer rely
// on its route. A queued attempt is rejected as unavailable while its deadline
// is still open. An attempt whose node.set_value is already in flight is decided
// by its own completion, because cancelling a request that is already on the
// wire would end the whole connection generation. An accepted attempt receives
// no second response: it stops publishing linked evidence and any in-flight
// linked publication is cancelled, so a route change, removal, or sleep can
// never leave stale linked evidence behind.
func (coordinator *runtimeCoordinator) invalidateNodeAttempts(nodeID int, cause error) {
	for _, attempt := range slices.Collect(maps.Values(coordinator.attempts)) {
		if attempt.nodeID != nodeID || attempt.phase == phaseTerminal {
			continue
		}
		switch attempt.phase {
		case phaseQueued:
			coordinator.abortAttempt(attempt, cause)
		case phaseSetting:
			// The node.set_value of this attempt is already on the wire, so its
			// own completion decides it.
		case phaseAccepted, phaseTerminal:
			coordinator.finishAttempt(attempt, cause)
		}
	}
	coordinator.revalidateQueuedAttempts(nodeID)
}

// revalidateQueuedAttempts rejects queued attempts of one node whose route is no
// longer current, and advances the node's queue.
func (coordinator *runtimeCoordinator) revalidateQueuedAttempts(nodeID int) {
	for _, attempt := range slices.Collect(maps.Values(coordinator.attempts)) {
		if attempt.nodeID != nodeID || attempt.phase != phaseQueued {
			continue
		}
		if !coordinator.attemptRouteIsCurrent(attempt) {
			coordinator.abortAttempt(attempt, errStaleRoute)
		}
	}
	coordinator.startNext(nodeID)
}

// abortAttempt ends one attempt, responding unavailable only while it has not
// been accepted and its deadline is still open.
func (coordinator *runtimeCoordinator) abortAttempt(attempt *commandAttempt, cause error) {
	if !attempt.accepted && !attempt.handlerDone && time.Now().Before(attempt.deadline) {
		if err := attempt.responder.RejectUnavailable("Z-Wave Entity is unavailable"); err != nil {
			coordinator.finishAttempt(attempt, err)
			return
		}
	}
	coordinator.finishAttempt(attempt, cause)
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

// routableRoutes applies v1 route eligibility. A node that is sleeping never
// receives routes, because the upstream could defer a write past Hearth's
// deadline. The node's Entities are still registered and reported unavailable.
func routableRoutes(state nodeState, routes []entityRoute) []entityRoute {
	if state.Status == nodeStatusAsleep {
		return nil
	}
	return routes
}

// replaceNodeRoutes atomically replaces one node's routes. An identical route
// set keeps its revision, so an unchanged refresh never invalidates an
// unaccepted Command.
func (coordinator *runtimeCoordinator) replaceNodeRoutes(nodeID int, routes []entityRoute) error {
	record := coordinator.nodes[nodeID]
	if record == nil {
		record = &nodeRecord{nodeID: nodeID, present: true}
		coordinator.nodes[nodeID] = record
	}
	if sameRoutes(record.routes, routes) {
		return nil
	}
	coordinator.routeRevision++
	record.routes = routes
	record.revision = coordinator.routeRevision
	if len(routes) > 0 {
		record.assessed = true
	}
	return coordinator.rebuildSnapshot()
}

// dropNodeRoutes invalidates one node's routes without touching its facts. A
// queued value report still waiting for a route from this node is dropped with
// it, because the node is no longer eligible to project it.
func (coordinator *runtimeCoordinator) dropNodeRoutes(nodeID int) {
	delete(coordinator.pendingValueAdded, nodeID)
	coordinator.clearNodeRoutes(nodeID)
}

// clearNodeRoutes invalidates one node's routes without touching its facts or
// its queued value reports. An intermediate refresh that still cannot project a
// queued current Value uses this instead of dropNodeRoutes, so the State that
// report carried survives until a later refresh completes the plan.
func (coordinator *runtimeCoordinator) clearNodeRoutes(nodeID int) {
	record := coordinator.nodes[nodeID]
	if record == nil || len(record.routes) == 0 {
		return
	}
	record.routes = nil
	record.revision = 0
	_ = coordinator.rebuildSnapshot()
}

// markNodeMissing reports one node as absent from the active generation.
func (coordinator *runtimeCoordinator) markNodeMissing(nodeID int) {
	record := coordinator.nodes[nodeID]
	if record == nil {
		record = &nodeRecord{nodeID: nodeID}
		coordinator.nodes[nodeID] = record
	}
	record.present = false
	coordinator.invalidateNodeRefresh(nodeID)
	coordinator.dropNodeRoutes(nodeID)
	coordinator.invalidateNodeAttempts(nodeID, errStaleRoute)
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

// sameRoutes reports whether two route sets are identical in Entity identity,
// plan key, and read and write Value IDs.
func sameRoutes(current, next []entityRoute) bool {
	if len(current) != len(next) {
		return false
	}
	for index := range current {
		if current[index].EntityID != next[index].EntityID ||
			current[index].Plan.Key != next[index].Plan.Key ||
			current[index].Plan.CurrentValueID.valueKey() != next[index].Plan.CurrentValueID.valueKey() ||
			current[index].Plan.TargetValueID.valueKey() != next[index].Plan.TargetValueID.valueKey() {
			return false
		}
	}
	return true
}

// snapshotObservations translates every planned node's snapshot Values into
// typed Observations in registration order, power before brightness per
// endpoint.
func (coordinator *runtimeCoordinator) snapshotObservations(
	nodes []reconciledNode,
	observedAt time.Time,
) []entityObservation {
	observations := make([]entityObservation, 0)
	for _, node := range nodes {
		if len(node.routes) == 0 {
			continue
		}
		published, issues := translateNodeValues(node.routes, observedAt, node.state.Values)
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

// handleUpstreamEvent applies one validated Event to the active generation.
func (coordinator *runtimeCoordinator) handleUpstreamEvent(received receivedEvent) {
	event := received.Event
	switch event.Event.Source {
	case eventSourceController:
		switch event.Event.Event {
		case eventNodeAdded:
			coordinator.requestNodeRefresh(event.Event.NodeID)
		case eventNodeRemoved:
			coordinator.markNodeMissing(event.Event.NodeID)
			coordinator.startNodeAvailabilityEffect(event.Event.NodeID, received.ReceivedAt)
		default:
			coordinator.logIgnoredEvent(event)
		}
	case eventSourceNode:
		coordinator.handleNodeEvent(received)
	case eventSourceDriver, eventSourceZniffer:
		coordinator.logIgnoredEvent(event)
	default:
		coordinator.logIgnoredEvent(event)
	}
}

// handleNodeEvent applies one node-sourced Event.
func (coordinator *runtimeCoordinator) handleNodeEvent(received receivedEvent) {
	event := received.Event
	nodeID := event.Event.NodeID
	if nodeID <= 0 {
		coordinator.logIgnoredEvent(event)
		return
	}
	switch event.Event.Event {
	case eventReady, eventInterviewCompleted, eventValueRemoved, eventMetadataUpdated:
		coordinator.requestNodeRefresh(nodeID)
	case eventValueAdded:
		coordinator.handleValueAdded(received)
	case eventValueUpdated:
		coordinator.handleValueUpdated(received)
	case eventWakeUp:
		coordinator.handleWakeUp(nodeID, received.ReceivedAt)
	case eventSleep:
		coordinator.handleSleep(nodeID, received.ReceivedAt)
	case eventAlive:
		coordinator.handleNodeStatus(nodeID, nodeStatusAlive, received.ReceivedAt)
	case eventDead:
		coordinator.handleNodeStatus(nodeID, nodeStatusDead, received.ReceivedAt)
	default:
		coordinator.logIgnoredEvent(event)
	}
}

// handleValueAdded applies one value added Event. A frame that already resolves
// against an active route is an ordinary Observation now. A state-eligible
// current Value that creates a new plan has no route yet, so it is queued and
// replayed only after a refresh installs the route it needs; publishing it now
// would lose its State. A target Value or an unrelated Value is never State, so
// it is never queued, but the refresh is requested either way because a new
// Value may still complete a plan.
func (coordinator *runtimeCoordinator) handleValueAdded(received receivedEvent) {
	nodeID := received.Event.Event.NodeID
	args, ok := decodeValueEventArgs(received.Event.Event.Args)
	if !ok || len(args.NewValue) == 0 {
		coordinator.requestNodeRefresh(nodeID)
		return
	}
	if coordinator.routeProjectsValue(nodeID, args.valueID) {
		// A route already exists, so the frame is projected now and is never
		// replayed: replaying it after the refresh would publish it twice.
		coordinator.publishValueObservationFor(received, args)
		coordinator.requestNodeRefresh(nodeID)
		return
	}
	if valueAddedCarriesState(args.valueID) {
		if !coordinator.queueValueAddedReplay(nodeID, received) {
			coordinator.dropGeneration(&eventBufferOverflowError{Limit: bufferedEventLimit})
			return
		}
	}
	coordinator.requestNodeRefresh(nodeID)
}

// routeProjectsValue reports whether an active route of one node already
// projects one Value. A Value the active routes do not plan is the only case a
// value added frame must wait for a refresh.
func (coordinator *runtimeCoordinator) routeProjectsValue(nodeID int, id valueID) bool {
	for _, route := range coordinator.snapshot.routesForValue(id) {
		if route.Plan.NodeID == nodeID {
			return true
		}
	}
	return false
}

// queueValueAddedReplay remembers one value added frame until a refresh installs
// the route that can project it. The queue is bounded by the same Event limit
// that bounds pre-activation buffering, so a burst ends the generation instead
// of silently dropping State. It reports false when the node's queue is full.
func (coordinator *runtimeCoordinator) queueValueAddedReplay(nodeID int, received receivedEvent) bool {
	pending := coordinator.pendingValueAdded[nodeID]
	if len(pending) >= bufferedEventLimit {
		return false
	}
	coordinator.pendingValueAdded[nodeID] = append(pending, received)
	return true
}

// replayPendingValueAdded publishes every queued value report of one node
// that its refreshed routes can now project, in receive order and through the
// node's ordinary-publication chain, so the State a report carried is not lost
// and its Adapter receive time is preserved. A report whose Value the refreshed
// routes still cannot project is retained instead of deleted: an intermediate
// refresh may install a plan that does not yet cover it, and only a later
// refresh that completes the plan may publish it. The retained queue stays
// bounded by the same Event limit that bounds pre-activation buffering.
func (coordinator *runtimeCoordinator) replayPendingValueAdded(nodeID int) {
	pending := coordinator.pendingValueAdded[nodeID]
	if len(pending) == 0 {
		return
	}
	retained := make([]receivedEvent, 0, len(pending))
	for _, received := range pending {
		args, ok := decodeValueEventArgs(received.Event.Event.Args)
		if !ok || !coordinator.routeProjectsValue(nodeID, args.valueID) {
			retained = append(retained, received)
			continue
		}
		coordinator.publishValueObservationFor(received, args)
	}
	if len(retained) == 0 {
		delete(coordinator.pendingValueAdded, nodeID)
		return
	}
	coordinator.pendingValueAdded[nodeID] = retained
}

// supersedePendingValueReplay replaces the queued replay report of one exact
// unkeyed Value with a newer report of the same Value, preserving the newer
// report's receive time. It acts only when a report for that Value is already
// waiting for its route: an update with no queued report is never queued, because
// no known plan needs it and the refresh that installs its route reads the
// current value anyway. Later duplicates of the same Value collapse into the one
// superseding report, so replay publishes that Value exactly once.
func (coordinator *runtimeCoordinator) supersedePendingValueReplay(
	nodeID int,
	received receivedEvent,
	args valueEventArgs,
) {
	pending := coordinator.pendingValueAdded[nodeID]
	if len(pending) == 0 {
		return
	}
	key := args.valueID.valueKey()
	replaced := false
	retained := pending[:0]
	for _, queued := range pending {
		queuedArgs, ok := decodeValueEventArgs(queued.Event.Event.Args)
		if ok && queuedArgs.valueID.valueKey() == key {
			if replaced {
				continue
			}
			retained = append(retained, received)
			replaced = true
			continue
		}
		retained = append(retained, queued)
	}
	if !replaced {
		return
	}
	coordinator.pendingValueAdded[nodeID] = retained
}

// handleValueUpdated publishes one ordinary Observation per planned Entity that
// projects the Value, then treats the frame as a coalesced poll hint for an
// accepted Command on the same Value. The Event itself is never linked evidence.
// A report whose Value no route can project yet supersedes any queued report of
// the same Value instead of being discarded, so a replay publishes the newest
// State rather than the stale value added frame it replaced.
func (coordinator *runtimeCoordinator) handleValueUpdated(received receivedEvent) {
	args, ok := decodeValueEventArgs(received.Event.Event.Args)
	if !ok {
		coordinator.logIgnoredEvent(received.Event)
		return
	}
	nodeID := received.Event.Event.NodeID
	switch {
	case len(args.NewValue) == 0:
		// A report without a new value is not State. It never publishes and never
		// supersedes a queued report.
	case coordinator.routeProjectsValue(nodeID, args.valueID):
		// A route already projects this Value, so the frame publishes now and is
		// never queued.
		coordinator.publishValueObservationFor(received, args)
	default:
		coordinator.supersedePendingValueReplay(nodeID, received, args)
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
	for _, route := range coordinator.snapshot.routesForValue(args.valueID) {
		if route.Plan.NodeID != nodeID {
			continue
		}
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
	previous := coordinator.publishTail
	next := make(chan struct{})
	coordinator.publishTail = next
	coordinator.startGenerationTask(scope, func() {
		defer close(next)
		if previous != nil {
			select {
			case <-previous:
			case <-ctx.Done():
				return
			}
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

// handleNodeStatus updates one node's status and reports fresh availability.
// The status Event is authoritative for the node's status, and it supersedes an
// in-flight inventory read so that read cannot restore the status it replaced.
func (coordinator *runtimeCoordinator) handleNodeStatus(nodeID, status int, observedAt time.Time) {
	record := coordinator.nodes[nodeID]
	if record == nil {
		return
	}
	record.present = true
	record.state.Status = status
	// A status Event does not itself read the node's inventory, so a
	// Registration that is already in flight must be followed by a fresh read:
	// its committed mappings exist in Core but only a current refresh can install
	// their routes. Marking the replacement pending keeps convergence even when
	// the stale Register commits after this Event.
	coordinator.supersedeNodeRefreshWithReplacement(nodeID)
	coordinator.startNodeAvailabilityEffect(nodeID, observedAt)
}

// handleSleep invalidates one node's Command routes immediately, then reports
// every owned Entity of that node unavailable. v1 never queues a write for a
// sleeping node, so recovery needs a wake up Event and a fresh node state.
func (coordinator *runtimeCoordinator) handleSleep(nodeID int, observedAt time.Time) {
	record := coordinator.nodeRecordFor(nodeID)
	record.state.Status = nodeStatusAsleep
	record.state.IsListening = false
	coordinator.invalidateNodeRefresh(nodeID)
	coordinator.dropNodeRoutes(nodeID)
	coordinator.invalidateNodeAttempts(nodeID, errNodeUnroutable)
	coordinator.startNodeAvailabilityEffect(nodeID, observedAt)
}

// handleWakeUp marks one node awake and refreshes its state. Routes return only
// after the refreshed state proves the node eligible again.
func (coordinator *runtimeCoordinator) handleWakeUp(nodeID int, observedAt time.Time) {
	record := coordinator.nodeRecordFor(nodeID)
	record.state.Status = nodeStatusAwake
	record.state.IsListening = false
	coordinator.invalidateNodeRefresh(nodeID)
	coordinator.dropNodeRoutes(nodeID)
	coordinator.invalidateNodeAttempts(nodeID, errNodeUnroutable)
	coordinator.startNodeAvailabilityEffect(nodeID, observedAt)
	coordinator.requestNodeRefresh(nodeID)
}

// invalidateNodeRefresh advances one node's refresh revision, so every refresh
// completion already in flight for that node is recognized as superseded: it
// installs nothing and does not re-register the node. The node's coalesced
// pending refresh is dropped with it, because this Event already carries a
// fresher fact than the abandoned read; convergence comes from the Event that
// asks for a new read, which requests its own refresh.
func (coordinator *runtimeCoordinator) invalidateNodeRefresh(nodeID int) {
	coordinator.refreshRevisionFor(nodeID).Add(1)
	delete(coordinator.refreshPending, nodeID)
}

// supersedeNodeRefreshWithReplacement advances one node's refresh revision like
// invalidateNodeRefresh, but preserves the node's coalesced pending replacement
// instead of clearing it. A status Event (alive or dead) carries the node's
// status authority without reading its inventory, so a Registration that commits
// after it would otherwise be remembered with no following refresh to install
// its routes. Marking pending makes applyNodeRefresh start that fresh read once
// the superseded completion is dropped, while the superseded read's state is
// never installed and cannot overwrite the status the Event just set.
func (coordinator *runtimeCoordinator) supersedeNodeRefreshWithReplacement(nodeID int) {
	coordinator.refreshRevisionFor(nodeID).Add(1)
	if coordinator.refreshInFlight[nodeID] {
		coordinator.refreshPending[nodeID] = true
	}
}

// refreshRevisionFor returns one node's refresh revision counter, creating it on
// first use. Only the coordinator adds to the map; a refresh effect holds the
// pointer it was started with and reads it.
func (coordinator *runtimeCoordinator) refreshRevisionFor(nodeID int) *atomic.Uint64 {
	revision := coordinator.nodeRefreshRevision[nodeID]
	if revision == nil {
		revision = &atomic.Uint64{}
		coordinator.nodeRefreshRevision[nodeID] = revision
	}
	return revision
}

// nodeRecordFor returns the record of one node, creating an absent one so an
// Event about a node the snapshot omitted is still reported.
func (coordinator *runtimeCoordinator) nodeRecordFor(nodeID int) *nodeRecord {
	record := coordinator.nodes[nodeID]
	if record == nil {
		record = &nodeRecord{nodeID: nodeID, present: true}
		coordinator.nodes[nodeID] = record
	}
	record.present = true
	return record
}

// startNodeAvailabilityEffect reports fresh availability for one node's owned
// mappings in owned-mapping order. Reports for one node are chained so a later
// report always lands after an earlier one: a wake up's stale unavailable batch
// can never overwrite the availability its refresh already reported. Every
// completion carries the generation that started it, so a report whose
// generation ended is ignored instead of being mistaken for a Session failure.
func (coordinator *runtimeCoordinator) startNodeAvailabilityEffect(nodeID int, observedAt time.Time) {
	reports := coordinator.nodeAvailabilityReports(nodeID, observedAt)
	if len(reports) == 0 {
		return
	}
	scope := coordinator.scope
	generation := coordinator.generation
	if scope == nil {
		coordinator.startEffect(func() runtimeEvent {
			return taskCompleted{
				generation: generation,
				err:        coordinator.adapter.reportAvailability(coordinator.ctx, reports),
			}
		})
		return
	}
	previous := coordinator.availabilityChain[nodeID]
	next := make(chan struct{})
	coordinator.availabilityChain[nodeID] = next
	coordinator.startGenerationEffect(scope, func() runtimeEvent {
		defer close(next)
		if previous != nil {
			select {
			case <-previous:
			case <-scope.ctx.Done():
				return taskCompleted{scope: scope, generation: generation}
			}
		}
		return taskCompleted{
			scope:      scope,
			generation: generation,
			err:        coordinator.adapter.reportAvailability(scope.ctx, reports),
		}
	})
}

// requestNodeRefresh refreshes one node's inventory through a correlated
// node.get_state and, when it is still eligible, re-registers it. Refreshes for
// the same node are coalesced: at most one is in flight and at most one is
// pending, and the pending one re-reads the node after the superseded
// completion is dropped. Every request advances the node's refresh revision, so
// an in-flight completion that a later Event superseded is recognized as stale
// instead of reinstalling routes.
func (coordinator *runtimeCoordinator) requestNodeRefresh(nodeID int) {
	if nodeID <= 0 {
		return
	}
	coordinator.invalidateNodeRefresh(nodeID)
	if coordinator.refreshInFlight[nodeID] {
		coordinator.refreshPending[nodeID] = true
		return
	}
	connection := coordinator.connection
	if !coordinator.dispatchable || connection == nil {
		return
	}
	coordinator.refreshInFlight[nodeID] = true
	generation := coordinator.generation
	refreshed := coordinator.refreshRevisionFor(nodeID)
	refreshedAt := refreshed.Load()
	homeID := coordinator.homeID
	home := coordinator.home
	scope := coordinator.scope
	refreshTimeout := coordinator.adapter.refreshTimeout
	coordinator.startGenerationEffect(scope, func() runtimeEvent {
		ctx := coordinator.effectContext(scope)
		outcome := nodeRefreshCompleted{
			scope:      scope,
			generation: generation,
			nodeID:     nodeID,
			revision:   refreshedAt,
			observedAt: time.Now().UTC(),
		}
		// The inventory read is bounded so an unanswered request can never hold
		// this node's routes for the whole generation. The bound is applied only
		// here: Register below runs under the generation's own lifetime, because
		// a first registration may legitimately outlive one node read.
		refreshContext, cancelRefresh := context.WithTimeout(ctx, refreshTimeout)
		state, err := connection.GetNodeState(refreshContext, nodeID)
		cancelRefresh()
		if err != nil {
			outcome.err = err
			return outcome
		}
		outcome.state = state
		planned, rejection := planNodeState(homeID, state)
		if rejection != nil {
			outcome.rejection = rejection
			return outcome
		}
		// Register only while this generation is still the active one, and only
		// while this read is still the node's newest one. A read that a later
		// Event superseded must not re-register the node it describes.
		if scope.ended() || refreshed.Load() != refreshedAt {
			return outcome
		}
		binding, err := coordinator.adapter.session.Register(ctx, planned.Registration)
		if err != nil {
			if _, rejected := errors.AsType[*adapter.RegistrationRejectedError](err); rejected {
				outcome.rejection = &nodeRejection{
					HomeID: home,
					NodeID: nodeID,
					Code:   rejectionNodeInvalidDescriptor,
				}
				return outcome
			}
			outcome.err = &sessionOperationError{operation: "register refreshed Z-Wave Device", err: err}
			return outcome
		}
		routes, err := bindEntityRoutes(binding, planned)
		if err != nil {
			outcome.rejection = &nodeRejection{
				HomeID: home,
				NodeID: nodeID,
				Code:   rejectionNodeInvalidDescriptor,
			}
			return outcome
		}
		outcome.deviceID = binding.DeviceID
		outcome.routes = routes
		return outcome
	})
}

// applyNodeRefresh installs the result of one node refresh. Only a successful
// re-registration may change routes; a rejected refresh invalidates them
// without attempting a zero-Entity registration. A refresh whose revision a
// later Event or invalidation superseded installs nothing, but a Registration
// that committed after the revision check cannot be recalled, so its bindings
// are still remembered.
func (coordinator *runtimeCoordinator) applyNodeRefresh(event nodeRefreshCompleted) error {
	if event.scope != coordinator.scope || event.generation != coordinator.generation {
		return nil
	}
	delete(coordinator.refreshInFlight, event.nodeID)
	var err error
	if event.revision == coordinator.refreshRevisionFor(event.nodeID).Load() {
		err = coordinator.installNodeRefresh(event)
	} else {
		coordinator.rememberSupersededRefresh(event)
	}
	if coordinator.refreshPending[event.nodeID] {
		delete(coordinator.refreshPending, event.nodeID)
		coordinator.requestNodeRefresh(event.nodeID)
	}
	return err
}

// installNodeRefresh applies one refresh outcome and reports that node's fresh
// availability.
func (coordinator *runtimeCoordinator) installNodeRefresh(event nodeRefreshCompleted) error {
	replayValueAdded := false
	switch {
	case isSessionOperationFailed(event.err):
		return event.err
	case event.err != nil && isUpstreamRejection(event.err):
		coordinator.markNodeMissing(event.nodeID)
	case event.err != nil:
		coordinator.adapter.logger.WarnContext(
			coordinator.ctx,
			"Z-Wave node refresh failed",
			slog.String(eventKey, "adapter.node_refresh_failed"),
			slog.String("error_code", codeUpstreamConnectionFailed),
		)
		coordinator.dropGeneration(event.err)
		// The generation is gone, so its routes are gone with it. Reporting the
		// refreshed node's availability now would describe a generation that no
		// longer exists, so the failed refresh ends here.
		return nil //nolint:nilerr // The refresh failure already dropped the generation; it is not terminal.
	case event.rejection != nil:
		record := coordinator.nodeRecordFor(event.nodeID)
		record.state = event.state
		// The refreshed plan still rejects this node, so its routes are dropped.
		// A value added frame that already waits for a route is kept: a later
		// refresh may complete the plan that projects it.
		coordinator.clearNodeRoutes(event.nodeID)
		coordinator.invalidateNodeAttempts(event.nodeID, errStaleRoute)
	default:
		record := coordinator.nodeRecordFor(event.nodeID)
		record.state = event.state
		next := routableRoutes(event.state, event.routes)
		changed := !sameRoutes(record.routes, next)
		if err := coordinator.replaceNodeRoutes(event.nodeID, next); err != nil {
			return err
		}
		coordinator.rememberNodeRoutes(reconciledNode{
			nodeID:   event.nodeID,
			deviceID: event.deviceID,
			routes:   event.routes,
		})
		if changed {
			// A real route change rejects every attempt that was never accepted.
			// An identical refresh changes nothing and must keep queued attempts.
			coordinator.invalidateNodeAttempts(event.nodeID, errStaleRoute)
		} else {
			coordinator.revalidateQueuedAttempts(event.nodeID)
		}
		replayValueAdded = true
	}
	coordinator.startNodeAvailabilityEffect(event.nodeID, event.observedAt)
	if replayValueAdded {
		// A queued value report that waited for this refresh now has the route it
		// needed, so it is replayed with its original receive time instead of
		// being lost with the inventory read that created its plan.
		coordinator.replayPendingValueAdded(event.nodeID)
	}
	return nil
}

// rememberSupersededRefresh records the bindings a superseded refresh committed.
// A Registration that started before a later Event advanced the node's refresh
// revision cannot be recalled, so its canonical Entities exist in Core whether
// or not their routes are installed. The stale routes are never installed: the
// node's current state and Event authority stay authoritative, and the newly
// known Entities are reported unavailable until a current refresh proves them
// routable.
func (coordinator *runtimeCoordinator) rememberSupersededRefresh(event nodeRefreshCompleted) {
	if event.deviceID == "" || len(event.routes) == 0 {
		return
	}
	coordinator.rememberNodeRoutes(reconciledNode{
		nodeID:   event.nodeID,
		deviceID: event.deviceID,
		routes:   event.routes,
	})
	coordinator.adapter.logger.DebugContext(
		coordinator.ctx,
		"superseded Z-Wave node refresh kept its committed mappings",
		slog.String(eventKey, "adapter.node_refresh_superseded"),
	)
	// The new mappings are known but unroutable in the current generation, so
	// their current status is resolved from the node's live state and installed
	// routes, which never include the stale route set.
	coordinator.startNodeAvailabilityEffect(event.nodeID, event.observedAt)
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
