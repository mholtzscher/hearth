// adapter.go owns the Adapter lifecycle: the minimal SDK Session seam, strict
// configuration, the reconnect supervisor, and one connection generation's
// startup reconciliation order. Every blocking Session or WebSocket call runs
// outside the serial runtime coordinator.

package zwavejs

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// reconnectMinimum and reconnectMaximum bound the exponential reconnect
	// backoff. Jitter is applied within each delay so several Adapters do not
	// retry one Z-Wave JS UI in lockstep.
	reconnectMinimum = 250 * time.Millisecond
	reconnectMaximum = 5 * time.Second

	// mappingPageLimit is the owned-mapping page size requested at startup.
	mappingPageLimit = 200

	// availabilityPage is the maximum Entity availability batch the SDK accepts.
	availabilityPage = 256

	// runtimeEventBuffer bounds the coordinator's incoming event queue.
	runtimeEventBuffer = 256

	// bufferedEventLimit bounds Events that arrive while reconciliation is in
	// progress. Exceeding it ends the generation rather than dropping an Event
	// silently.
	bufferedEventLimit = 1024

	// concurrentRuntimeComponents is the number of goroutines Run supervises.
	concurrentRuntimeComponents = 2

	// jitterDivisor halves a reconnect delay before jitter is added.
	jitterDivisor = 2

	// operationSet is the only Command operation v1 plans.
	operationSet = "set"
)

// Fixed unhealthy reason codes.
const (
	externalSystemUnavailableReason = "hearth.external_system_unavailable"
	incompatibleProtocolReason      = "adapter.hearth-adapter-zwavejs.incompatible_protocol"
	invalidSnapshotReason           = "adapter.hearth-adapter-zwavejs.invalid_snapshot"
	networkIdentityMismatchReason   = "adapter.hearth-adapter-zwavejs.network_identity_mismatch"
)

// Session is the minimal SDK seam this Adapter consumes. It has no Entity Event
// method because v1 plans no event-source Entity.
type Session interface {
	ListOwnedMappings(context.Context, adapter.OwnedMappingPageRequest) (adapter.OwnedMappingPage, error)
	Register(context.Context, adapter.Registration) (adapter.Binding, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

// Config is the Adapter's own configuration. URL validation beyond presence is
// owned by the process assembly package.
type Config struct {
	URL string
}

// Adapter is the stateless Z-Wave JS Adapter.
type Adapter struct {
	session Session
	config  Config
	logger  *slog.Logger
	dialer  zwaveDialer

	runtimeEvents chan runtimeEvent
	runtimeDone   chan struct{}

	// retryDelay and pollHintInterval are seams for deterministic tests. They
	// never change production behavior.
	retryDelay       func(time.Duration) time.Duration
	pollHintInterval time.Duration

	// refreshTimeout bounds one node.get_state inventory read. It is a seam for
	// deterministic tests so a test can prove the bound without waiting for the
	// conservative production value.
	refreshTimeout time.Duration
}

// sessionOperationError reports a failed SDK Session call. It is terminal:
// Run returns it rather than reconnecting.
type sessionOperationError struct {
	operation string
	err       error
}

func (err *sessionOperationError) Error() string { return err.operation + ": " + err.err.Error() }
func (err *sessionOperationError) Unwrap() error { return err.err }

// New builds an Adapter over the production WebSocket dialer.
func New(session Session, config Config, logger *slog.Logger) (*Adapter, error) {
	return newAdapter(session, config, logger, websocketDialer{})
}

// newAdapter builds an Adapter over an injected connection seam.
func newAdapter(
	session Session,
	config Config,
	logger *slog.Logger,
	dialer zwaveDialer,
) (*Adapter, error) {
	switch {
	case session == nil:
		return nil, errors.New("Z-Wave JS adapter Session is required")
	case strings.TrimSpace(config.URL) == "":
		return nil, errors.New("Z-Wave JS URL is required")
	case dialer == nil:
		return nil, errors.New("Z-Wave JS dialer is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		session:          session,
		config:           config,
		logger:           logger.With(slog.String("component", adapterComponent)),
		dialer:           dialer,
		runtimeEvents:    make(chan runtimeEvent, runtimeEventBuffer),
		runtimeDone:      make(chan struct{}),
		retryDelay:       jitterReconnect,
		pollHintInterval: defaultPollHintInterval,
		refreshTimeout:   defaultNodeRefreshTimeout,
	}, nil
}

// HandleCommand is defined in command.go; it submits one Command to the serial
// runtime coordinator and waits for its single response.

// Run supervises the reconnect loop and the serial runtime coordinator. Parent
// cancellation is graceful shutdown and returns nil.
func (zwave *Adapter) Run(ctx context.Context) error {
	runContext, cancel := context.WithCancel(ctx)
	results := make(chan error, concurrentRuntimeComponents)
	coordinator := newRuntimeCoordinator(runContext, zwave)
	go func() { results <- coordinator.run() }()
	go func() { results <- zwave.runConnections(runContext) }()

	first := <-results
	cancel()
	second := <-results
	if ctx.Err() != nil {
		return nil //nolint:nilerr // Parent cancellation is graceful shutdown.
	}
	for _, err := range []error{first, second} {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

// runConnections dials, reconciles, and reconnects with bounded exponential
// backoff. A failed Session call ends the loop and the process.
func (zwave *Adapter) runConnections(ctx context.Context) error {
	delay := reconnectMinimum
	var generation uint64
	for {
		generation++
		synchronized, err := zwave.runConnection(ctx, generation)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if isSessionOperationFailed(err) {
			return err
		}
		if healthErr := zwave.reportUnhealthy(
			ctx,
			generation,
			unhealthyReason(err),
		); healthErr != nil {
			return healthErr
		}
		if synchronized {
			delay = reconnectMinimum
		}
		wait := zwave.retryDelay(delay)
		zwave.logger.DebugContext(
			ctx,
			"Z-Wave JS connection retrying",
			slog.String(eventKey, "dependency.retrying"),
			slog.String("dependency", adapterComponent),
			slog.String("error_code", zwaveJSErrorCode(err)),
			slog.Int64("retry_in_ms", max(wait.Milliseconds(), 0)),
		)
		if !sleepContext(ctx, wait) {
			return ctx.Err()
		}
		if delay < reconnectMaximum {
			delay *= 2
			if delay > reconnectMaximum {
				delay = reconnectMaximum
			}
		}
	}
}

// sleepContext waits for one reconnect delay. It reports false when the parent
// context ended first.
func sleepContext(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(max(wait, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

//nolint:gosec // Backoff jitter needs no cryptographic randomness.
func jitterReconnect(delay time.Duration) time.Duration {
	half := delay / jitterDivisor
	if half <= 0 {
		return delay
	}
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

// runConnection performs one connection generation. It reports whether the
// generation reached full reconciliation, and the failure that ended it. The
// deferred invalidation ends the generation before any unhealthy report.
func (zwave *Adapter) runConnection(ctx context.Context, generation uint64) (bool, error) {
	owned, err := zwave.listOwnedMappings(ctx)
	if err != nil {
		return false, err
	}

	connectionContext, cancelConnection := context.WithCancelCause(ctx)
	connection, err := zwave.dialer.Dial(
		connectionContext,
		zwave.config.URL,
		schemaVersion29,
		defaultUserAgentComponents(),
	)
	if err != nil {
		cancelConnection(nil)
		return false, err
	}
	defer func() {
		_ = zwave.invalidateGeneration(ctx, generation, context.Cause(connectionContext))
		cancelConnection(nil)
		connection.Close()
	}()
	go zwave.monitorConnectionLoss(connectionContext, cancelConnection, connection)

	version, snapshot, err := connection.StartListening(connectionContext)
	if err != nil {
		return false, preferConnectionError(ctx, connectionContext, err)
	}
	go zwave.pumpEvents(connectionContext, generation, connection, cancelConnection)

	if err = verifyConnectionHomeIDs(version, snapshot); err != nil {
		return false, preferConnectionError(ctx, connectionContext, err)
	}
	if err = verifyNetworkIdentity(owned, *version.HomeID); err != nil {
		return false, preferConnectionError(ctx, connectionContext, err)
	}

	network := planNetwork(*version.HomeID, snapshot)
	nodes, err := zwave.buildReconciliation(connectionContext, snapshot, network)
	if err != nil {
		return false, preferConnectionError(ctx, connectionContext, err)
	}
	observedAt := time.Now().UTC()
	err = zwave.activateReconciliation(
		connectionContext,
		generation,
		connection,
		cancelConnection,
		*version.HomeID,
		owned,
		nodes,
		observedAt,
	)
	if err != nil {
		return false, preferConnectionError(ctx, connectionContext, err)
	}
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case <-connectionContext.Done():
		return true, preferConnectionError(ctx, connectionContext, context.Cause(connectionContext))
	}
}

// verifyConnectionHomeIDs requires the version frame and the start-listening
// snapshot to describe the same Z-Wave network.
func verifyConnectionHomeIDs(version serverVersion, snapshot networkSnapshot) error {
	if version.HomeID == nil {
		return &malformedVersionFrameError{Reason: "the version frame carried no Home ID"}
	}
	if snapshot.State.Controller.HomeID == nil {
		return &malformedSnapshotError{Reason: "the snapshot carried no controller Home ID"}
	}
	return nil
}

// monitorConnectionLoss cancels the connection context when the generation's
// terminal error arrives, so the connection loop leaves its select promptly.
func (zwave *Adapter) monitorConnectionLoss(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	connection zwaveConnection,
) {
	select {
	case err := <-connection.Lost():
		if err == nil {
			err = errors.New("Z-Wave JS connection lost")
		}
		cancel(err)
	case <-ctx.Done():
	}
}

// pumpEvents forwards validated Events from one connection generation to the
// runtime coordinator. Each Event carries the generation's own connection and
// cancellation, so an overflow before activation can terminate exactly the
// incoming generation. Events that arrive before routes are installed are
// buffered by the coordinator and replayed once reconciliation completes.
func (zwave *Adapter) pumpEvents(
	ctx context.Context,
	generation uint64,
	connection zwaveConnection,
	disconnect context.CancelCauseFunc,
) {
	events := connection.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			event.ReceivedAt = event.ReceivedAt.UTC()
			select {
			case zwave.runtimeEvents <- upstreamEvent{
				generation: generation,
				connection: connection,
				disconnect: disconnect,
				event:      event,
			}:
			case <-ctx.Done():
				return
			case <-zwave.runtimeDone:
				return
			}
		}
	}
}

// preferConnectionError returns the connection's terminal cause when the
// connection ended before the parent context did, so a lost socket is never
// reported as a local operation failure.
func preferConnectionError(
	parent context.Context,
	connectionContext context.Context,
	operationErr error,
) error {
	if parent.Err() == nil && connectionContext.Err() != nil {
		return context.Cause(connectionContext)
	}
	return operationErr
}

// buildReconciliation registers every eligible node and pairs the returned
// canonical Entity IDs with the planned routes. Nodes the snapshot reports but
// the planner does not plan keep their facts, so availability can name the
// exact reason without a zero-Entity registration.
func (zwave *Adapter) buildReconciliation(
	ctx context.Context,
	snapshot networkSnapshot,
	network networkPlan,
) ([]reconciledNode, error) {
	states := make(map[int]nodeState, len(snapshot.State.Nodes))
	for _, node := range snapshot.State.Nodes {
		states[node.NodeID] = node
	}
	nodes := make([]reconciledNode, 0, len(snapshot.State.Nodes))
	seen := make(map[int]struct{}, len(snapshot.State.Nodes))
	for _, node := range network.Nodes {
		binding, err := zwave.session.Register(ctx, node.Registration)
		if err != nil {
			if rejected, ok := errors.AsType[*adapter.RegistrationRejectedError](err); ok {
				zwave.logIsolatedNode(ctx, string(rejected.Code))
				continue
			}
			return nil, &sessionOperationError{operation: "register Z-Wave Device", err: err}
		}
		routes, err := bindEntityRoutes(binding, node)
		if err != nil {
			zwave.logIsolatedNode(ctx, string(rejectionNodeInvalidDescriptor))
			continue
		}
		seen[node.NodeID] = struct{}{}
		nodes = append(nodes, reconciledNode{
			nodeID: node.NodeID,
			state:  states[node.NodeID],
			routes: routes,
		})
	}
	for _, node := range snapshot.State.Nodes {
		if _, ok := seen[node.NodeID]; ok {
			continue
		}
		seen[node.NodeID] = struct{}{}
		nodes = append(nodes, reconciledNode{nodeID: node.NodeID, state: node})
	}
	return nodes, nil
}

// listOwnedMappings pages through every mapping owned by this Adapter. It
// refuses a pagination that does not advance or repeats a cursor, so an
// unbounded listing can never spin.
func (zwave *Adapter) listOwnedMappings(ctx context.Context) ([]adapter.OwnedMapping, error) {
	var result []adapter.OwnedMapping
	cursor := ""
	seen := make(map[string]struct{})
	for {
		page, err := zwave.session.ListOwnedMappings(ctx, adapter.OwnedMappingPageRequest{
			Limit:  mappingPageLimit,
			Cursor: cursor,
		})
		if err != nil {
			return nil, &sessionOperationError{operation: "list owned Z-Wave mappings", err: err}
		}
		result = append(result, page.Items...)
		if page.NextCursor == "" {
			return result, nil
		}
		if page.NextCursor == cursor {
			return nil, errors.New("zwavejs: owned mapping pagination did not advance")
		}
		if _, duplicate := seen[page.NextCursor]; duplicate {
			return nil, errors.New("zwavejs: owned mapping pagination repeated a cursor")
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}

// activateReconciliation installs one generation's routes in the coordinator.
// The coordinator reports healthy, then fresh availability, then snapshot
// Observations before it activates Events and Commands.
func (zwave *Adapter) activateReconciliation(
	ctx context.Context,
	generation uint64,
	connection zwaveConnection,
	disconnect context.CancelCauseFunc,
	homeID uint32,
	owned []adapter.OwnedMapping,
	nodes []reconciledNode,
	observedAt time.Time,
) error {
	result := make(chan error, 1)
	event := reconciliationSubmitted{
		generation: generation,
		connection: connection,
		disconnect: disconnect,
		homeID:     homeID,
		mappings:   owned,
		nodes:      nodes,
		observedAt: observedAt,
		result:     result,
	}
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

// invalidateGeneration ends one generation's routes and every attempt that was
// never accepted, before any unhealthy report.
func (zwave *Adapter) invalidateGeneration(
	ctx context.Context,
	generation uint64,
	cause error,
) error {
	result := make(chan error, 1)
	event := generationInvalidated{generation: generation, cause: cause, result: result}
	select {
	case zwave.runtimeEvents <- event:
	case <-ctx.Done():
		return ctx.Err()
	case <-zwave.runtimeDone:
		return nil
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-zwave.runtimeDone:
		return nil
	}
}

// reportUnhealthy invalidates the generation's routes first, then reports the
// fixed unhealthy reason.
func (zwave *Adapter) reportUnhealthy(ctx context.Context, generation uint64, reason string) error {
	if err := zwave.invalidateGeneration(ctx, generation, errors.New(reason)); err != nil {
		return err
	}
	if err := zwave.session.SetHealth(ctx, adapter.HealthReport{
		Status:           adapter.HealthUnhealthy,
		SourceObservedAt: time.Now().UTC(),
		ReasonCode:       reason,
	}); err != nil {
		return &sessionOperationError{operation: "report unhealthy Z-Wave JS connection", err: err}
	}
	return nil
}
