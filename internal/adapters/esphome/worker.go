package esphome

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// worker owns one device. Its run goroutine is the only reader and writer of the fields below the channels.
type worker struct {
	adapter *Adapter
	index   int
	device  Device
	logger  *slog.Logger

	commands chan *commandRequest
	expired  chan uint64
	done     chan struct{}

	// owned maps each registered Entity key to its canonical Entity ID.
	owned        map[string]string
	availability *availabilityTracker
	nextToken    uint64
}

// connectResult is the outcome of one connection attempt, built off the worker goroutine.
type connectResult struct {
	conn    nativeapi.Conn
	plan    devicePlan
	binding adapter.Binding
	err     error
}

// generation is one live connection's routes, cache, and Command queues.
type generation struct {
	conn       nativeapi.Conn
	byUpstream map[upstreamRef][]boundRoute
	byEntityID map[string]boundRoute
	// cache holds each Entity's last projected value on this connection.
	cache  map[string]adapter.Observation
	queues map[upstreamRef]*commandQueue
	// coverTargets await position evidence but never block another cover write.
	coverTargets map[upstreamRef][]*commandAttempt
}

type boundRoute struct {
	entityRoute

	EntityID string
}

func newWorker(owner *Adapter, index int, device Device, owned map[string]string) *worker {
	copied := make(map[string]string, len(owned))
	maps.Copy(copied, owned)
	return &worker{
		adapter:      owner,
		index:        index,
		device:       device,
		logger:       owner.logger.With(slog.Int("device_index", index)),
		commands:     make(chan *commandRequest),
		expired:      make(chan uint64),
		done:         make(chan struct{}),
		owned:        copied,
		availability: newAvailabilityTracker(),
	}
}

// run connects, serves, and reconnects until ctx ends. It returns only Session failures.
func (w *worker) run(ctx context.Context) error {
	defer close(w.done)
	if w.device.Transport.mode == transportPlaintext {
		w.logger.WarnContext(ctx, "device transport is unencrypted",
			slog.String("event", "device.insecure_plaintext"))
	}
	var delay time.Duration
	for {
		result, stopped := w.connect(ctx)
		if stopped {
			return nil
		}
		var (
			endErr error
			lived  time.Duration
		)
		if result.err != nil {
			endErr = result.err
		} else {
			started := time.Now()
			endErr = w.serve(ctx, result)
			lived = time.Since(started)
		}
		if ctx.Err() != nil {
			return nil
		}
		if isSessionFailure(endErr) {
			return endErr
		}
		reason := connectionReason(endErr)
		w.logger.WarnContext(ctx, "device connection ended",
			slog.String("event", "device.connection_ended"),
			slog.String("reason_code", reason),
		)
		if err := w.markUnavailable(ctx, reason); err != nil {
			return err
		}
		delay = w.nextDelay(delay, lived, reason)
		if !w.wait(ctx, w.adapter.timing.jitter(delay)) {
			return nil
		}
	}
}

// nextDelay grows exponentially, resets after a stable generation, and waits at the ceiling for operator-fixable
// causes.
func (w *worker) nextDelay(previous, lived time.Duration, reason string) time.Duration {
	const backoffMultiplier = 2
	policy := w.adapter.timing
	switch {
	case configurationReason(reason):
		return policy.maximumBackoff
	case lived >= policy.stableAfter || previous == 0:
		return policy.initialBackoff
	default:
		return min(max(previous*backoffMultiplier, policy.initialBackoff), policy.maximumBackoff)
	}
}

// wait sleeps for delay while rejecting Commands. It reports false when ctx ended.
func (w *worker) wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		case request := <-w.commands:
			request.finish(request.responder.RejectUnavailable("ESPHome device is not connected"))
		}
	}
}

// connect runs one attempt in its own goroutine so Commands are still answered while it blocks. stopped reports ctx
// cancellation.
func (w *worker) connect(ctx context.Context) (connectResult, bool) {
	attemptContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan connectResult, 1)
	go func() { results <- w.attempt(attemptContext) }()
	for {
		select {
		case <-ctx.Done():
			cancel()
			if result := <-results; result.conn != nil {
				_ = result.conn.Close()
			}
			return connectResult{}, true
		case request := <-w.commands:
			request.finish(request.responder.RejectUnavailable("ESPHome device is not connected"))
		case result := <-results:
			return result, false
		}
	}
}

// attempt dials, verifies identity, plans, and registers. It touches no worker state.
func (w *worker) attempt(ctx context.Context) connectResult {
	deviceContext, cancel := context.WithTimeout(ctx, w.adapter.timing.connectTimeout)
	defer cancel()
	conn, err := w.adapter.dialer.Dial(deviceContext, nativeapi.DialConfig{
		Address: w.device.Address, Port: w.device.Port, PSK: w.device.Transport.key,
		ExpectMAC: w.device.MAC.String(), ClientInfo: clientInfo,
		InsecurePlaintext: w.device.Transport.mode == transportPlaintext,
	})
	if err != nil {
		return connectResult{err: err}
	}
	result := w.discover(deviceContext, ctx, conn)
	if result.err != nil {
		_ = conn.Close()
		return connectResult{err: result.err}
	}
	result.conn = conn
	return result
}

func (w *worker) discover(deviceContext, ctx context.Context, conn nativeapi.Conn) connectResult {
	info, err := conn.DeviceInfo(deviceContext)
	if err != nil {
		return connectResult{err: err}
	}
	if reported, parseErr := ParseMAC(info.MAC); parseErr != nil || reported != w.device.MAC {
		return connectResult{err: nativeapi.ErrIdentityMismatch}
	}
	entities, err := conn.ListEntities(deviceContext)
	if err != nil {
		return connectResult{err: err}
	}
	plan, err := planDevice(w.device.MAC, info, entities, w.device.Exposure)
	if err != nil {
		return connectResult{err: err}
	}
	if len(plan.Registration.Entities) == 0 {
		return connectResult{plan: plan}
	}
	// Core gets a fresh budget: slow device discovery must not consume registration's deadline.
	registrationContext, cancel := context.WithTimeout(ctx, w.adapter.timing.connectTimeout)
	defer cancel()
	binding, err := w.adapter.session.Register(registrationContext, plan.Registration)
	if err != nil {
		_, rejected := errors.AsType[*adapter.RegistrationRejectedError](err)
		_, invalid := errors.AsType[*adapter.ValidationError](err)
		if rejected || invalid {
			return connectResult{
				err: &unsupportedDeviceError{Reason: reasonPlanningFailed, Detail: "registration rejected"},
			}
		}
		return connectResult{err: &sessionError{operation: "register", err: err}}
	}
	return connectResult{plan: plan, binding: binding}
}

// serve runs one generation until the connection ends or ctx is cancelled.
func (w *worker) serve(ctx context.Context, result connectResult) error {
	gen, err := w.install(result)
	if err != nil {
		_ = result.conn.Close()
		return err
	}
	defer w.endGeneration(gen)
	if err = w.prepareGeneration(ctx, gen, result); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-gen.conn.Done():
			return gen.conn.Err()
		case update, open := <-gen.conn.Events():
			if connErr := gen.conn.Err(); connErr != nil {
				return connErr
			}
			if !open {
				return gen.conn.Err()
			}
			if handleStateErr := w.handleState(ctx, gen, update); handleStateErr != nil {
				return handleStateErr
			}
		case request := <-w.commands:
			if handleCommandErr := w.handleCommand(ctx, gen, request); handleCommandErr != nil {
				return handleCommandErr
			}
		case token := <-w.expired:
			if handleExpiryErr := w.handleExpiry(ctx, gen, token); handleExpiryErr != nil {
				return handleExpiryErr
			}
		}
	}
}

// prepareGeneration subscribes and reports availability before processing events.
func (w *worker) prepareGeneration(ctx context.Context, gen *generation, result connectResult) error {
	w.logPlan(ctx, result.plan, result.binding.DeviceID)
	if err := gen.conn.SubscribeStates(ctx); err != nil {
		return err
	}
	return w.reportPlanned(ctx, gen)
}

// install binds planned routes to registered Entity IDs and records ownership.
func (w *worker) install(result connectResult) (*generation, error) {
	gen := &generation{
		conn:         result.conn,
		byUpstream:   map[upstreamRef][]boundRoute{},
		byEntityID:   map[string]boundRoute{},
		cache:        map[string]adapter.Observation{},
		queues:       map[upstreamRef]*commandQueue{},
		coverTargets: map[upstreamRef][]*commandAttempt{},
	}
	if len(result.plan.Routes) == 0 {
		return gen, nil
	}
	if result.binding.BindingKey != result.plan.Registration.BindingKey {
		return nil, errors.New("esphome: registration answered a different Binding")
	}
	byKey := make(map[string]string, len(result.binding.Entities))
	for _, entity := range result.binding.Entities {
		byKey[entity.Key] = entity.EntityID
	}
	entityIDs := make([]string, 0, len(result.plan.Routes))
	for _, route := range result.plan.Routes {
		entityID := byKey[route.EntityKey]
		if entityID == "" {
			return nil, fmt.Errorf("esphome: registration omitted planned Entity %s", route.EntityKey)
		}
		bound := boundRoute{entityRoute: route, EntityID: entityID}
		gen.byUpstream[route.Upstream] = append(gen.byUpstream[route.Upstream], bound)
		gen.byEntityID[entityID] = bound
		w.owned[route.EntityKey] = entityID
		entityIDs = append(entityIDs, entityID)
	}
	w.adapter.router.assign(w, entityIDs)
	return gen, nil
}

// reportPlanned makes planned Entities available and every other owned Entity unavailable.
func (w *worker) reportPlanned(ctx context.Context, gen *generation) error {
	wanted := make(map[string]availabilityStatus, len(w.owned))
	for _, entityID := range w.owned {
		if _, planned := gen.byEntityID[entityID]; planned {
			wanted[entityID] = availabilityStatus{available: true}
		} else {
			wanted[entityID] = availabilityStatus{reason: reasonEntityMissing}
		}
	}
	return w.applyAvailability(ctx, wanted)
}

// markUnavailable reports every owned Entity unavailable with reason.
func (w *worker) markUnavailable(ctx context.Context, reason string) error {
	wanted := make(map[string]availabilityStatus, len(w.owned))
	for _, entityID := range w.owned {
		wanted[entityID] = availabilityStatus{reason: reason}
	}
	return w.applyAvailability(ctx, wanted)
}

func (w *worker) applyAvailability(ctx context.Context, wanted map[string]availabilityStatus) error {
	reports := w.availability.changes(wanted, time.Now().UTC())
	if len(reports) == 0 {
		return nil
	}
	if err := reportAvailability(ctx, w.adapter.session, reports); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &sessionError{operation: "report availability", err: err}
	}
	w.availability.commit(reports)
	return nil
}

// handleState projects one push into every Entity it feeds and links it to an awaiting Command when one targets
// that Entity.
func (w *worker) handleState(ctx context.Context, gen *generation, update nativeapi.StateUpdate) error {
	upstream := upstreamRef{Type: update.Type, DeviceID: update.DeviceID, Key: update.Key}
	receivedAt := update.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	routes := gen.byUpstream[upstream]
	observations := make(map[string]adapter.Observation, len(routes))
	// Refresh every sibling cache before a publication can advance the light's shared Command FIFO.
	for _, route := range routes {
		observation, ok, err := project(route.entityRoute, route.EntityID, update, receivedAt)
		if err != nil {
			delete(gen.cache, route.EntityID)
			w.logger.DebugContext(ctx, "state outside Entity type",
				slog.String("event", "observation.out_of_range"),
				slog.String("entity_id", route.EntityID),
			)
			continue
		}
		if !ok {
			delete(gen.cache, route.EntityID)
			continue
		}
		gen.cache[route.EntityID] = observation
		observations[route.EntityID] = observation
	}
	for _, route := range routes {
		observation, present := observations[route.EntityID]
		if !present {
			continue
		}
		if publishErr := w.publish(ctx, gen, upstream, route, observation); publishErr != nil {
			return publishErr
		}
	}
	// The whole frame belongs to the previously active Command. Only now may the shared FIFO dispatch another.
	if queue := gen.queues[upstream]; queue != nil && queue.active == nil {
		return w.startNext(ctx, gen, queue)
	}
	return nil
}

// publish sends one Observation, linked to the awaiting Command for this Entity when there is one.
func (w *worker) publish(
	ctx context.Context,
	gen *generation,
	upstream upstreamRef,
	route boundRoute,
	observation adapter.Observation,
) error {
	if route.Kind == routeCover {
		return w.publishCover(ctx, gen, upstream, observation)
	}
	if queue := gen.queues[upstream]; queue != nil && queue.active != nil &&
		queue.active.route.EntityID == route.EntityID {
		return w.publishLinked(ctx, queue, observation)
	}
	if _, err := w.adapter.session.PublishObservation(ctx, observation); err != nil {
		return w.publicationFailure(ctx, err)
	}
	return nil
}

// publicationFailure skips an Observation the SDK rejects as invalid and treats every other failure as terminal.
func (w *worker) publicationFailure(ctx context.Context, err error) error {
	var validation *adapter.ValidationError
	switch {
	case errors.As(err, &validation):
		w.logger.WarnContext(ctx, "observation rejected by SDK validation",
			slog.String("event", "observation.invalid"),
		)
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return &sessionError{operation: "publish observation", err: err}
	}
}

// endGeneration closes the connection and settles every Command the generation held.
func (w *worker) endGeneration(gen *generation) {
	_ = gen.conn.Close()
	for upstream := range gen.coverTargets {
		clearCoverTargets(gen, upstream)
	}
	for _, queue := range gen.queues {
		if queue.active != nil {
			queue.active.stopTimer()
			queue.active = nil
		}
		for _, attempt := range queue.queued {
			attempt.request.finish(attempt.request.responder.RejectUnavailable("ESPHome device disconnected"))
		}
		queue.queued = nil
	}
}

func (w *worker) logPlan(ctx context.Context, plan devicePlan, deviceID string) {
	w.logger.InfoContext(ctx, "device planned",
		slog.String("event", "device.planned"),
		slog.String("device_id", deviceID),
		slog.Int("entity_count", len(plan.Registration.Entities)),
		slog.Int("skipped_count", len(plan.Skipped)),
	)
	if len(plan.Registration.Entities) == 0 {
		w.logger.WarnContext(ctx, "device has no eligible entities",
			slog.String("event", "device.no_eligible_entities"),
		)
	}
	for range plan.UnmatchedOverrides {
		w.logger.WarnContext(ctx, "override matches no entity",
			slog.String("event", "overrides.unmatched"),
		)
	}
}

func isSessionFailure(err error) bool {
	var session *sessionError
	return errors.As(err, &session)
}
