// command.go owns Command admission, per-upstream FIFO dispatch, and evidence linking. ESPHome commands have no
// acknowledgement: a successful frame write permits acceptance, and only state pushes on the same live subscription
// become linked evidence.

package esphome

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	operationSet = "set"
	// commandQueueLimit bounds Commands for one upstream entity, including the active one.
	commandQueueLimit = 16
	// commandWriteTimeout bounds one command frame write.
	commandWriteTimeout = 2 * time.Second
)

// commandRequest is one Command submitted by the SDK handler goroutine. The worker always answers it once.
type commandRequest struct {
	command   adapter.Command
	responder adapter.Responder
	deadline  time.Time
	result    chan error
}

func (request *commandRequest) finish(err error) { request.result <- err }

// commandAttempt is one admitted Command and its translated upstream request.
type commandAttempt struct {
	request  *commandRequest
	route    boundRoute
	target   json.RawMessage
	native   nativeapi.Command
	evidence adapter.CommandEvidence
	token    uint64
	timer    *time.Timer
}

func (attempt *commandAttempt) stopTimer() {
	if attempt.timer != nil {
		attempt.timer.Stop()
	}
}

// commandQueue serializes Commands for one upstream entity. A light's power and brightness Entities share one queue.
type commandQueue struct {
	active *commandAttempt
	queued []*commandAttempt
}

// submit hands a Command to the worker and waits for its acceptance or rejection. Evidence publication continues
// after submit returns.
func (w *worker) submit(ctx context.Context, command adapter.Command, responder adapter.Responder) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(commandWriteTimeout)
	}
	request := &commandRequest{command: command, responder: responder, deadline: deadline, result: make(chan error, 1)}
	select {
	case w.commands <- request:
	case <-w.done:
		return responder.RejectUnavailable("ESPHome adapter is stopping")
	case <-ctx.Done():
		return responder.RejectUnavailable("Command expired before dispatch")
	}
	return <-request.result
}

// handleCommand admits one Command into its upstream queue.
func (w *worker) handleCommand(ctx context.Context, gen *generation, request *commandRequest) error {
	if err := gen.conn.Err(); err != nil {
		request.finish(request.responder.RejectUnavailable("ESPHome device disconnected"))
		return err
	}
	route, found := gen.byEntityID[request.command.EntityID]
	if !found {
		request.finish(request.responder.RejectUnavailable("Entity is not available on the connected ESPHome device"))
		return nil
	}
	if request.command.OperationName != operationSet {
		request.finish(request.responder.Reject("unsupported operation"))
		return nil
	}
	target, native, err := prepareSet(route, request.command.Parameters)
	if err != nil {
		request.finish(request.responder.Reject("invalid set parameters"))
		return nil //nolint:nilerr // The rejection is the outcome; the Command is not a worker failure.
	}
	queue := gen.queues[route.Upstream]
	if queue == nil {
		queue = &commandQueue{}
		gen.queues[route.Upstream] = queue
	}
	pending := len(queue.queued)
	if queue.active != nil {
		pending++
	}
	if pending >= commandQueueLimit {
		request.finish(request.responder.RejectUnavailable("too many pending Commands for this ESPHome entity"))
		return nil
	}
	queue.queued = append(queue.queued, &commandAttempt{request: request, route: route, target: target, native: native})
	if queue.active == nil {
		return w.startNext(ctx, gen, queue)
	}
	return nil
}

// startNext dispatches queued attempts until one awaits evidence, the queue empties, or a write fails.
func (w *worker) startNext(ctx context.Context, gen *generation, queue *commandQueue) error {
	for queue.active == nil && len(queue.queued) > 0 {
		attempt := queue.queued[0]
		queue.queued = queue.queued[1:]
		if !time.Now().Before(attempt.request.deadline) {
			attempt.request.finish(attempt.request.responder.RejectUnavailable("Command expired while queued"))
			continue
		}
		sendContext, cancel := context.WithDeadline(
			ctx,
			earlierDeadline(attempt.request.deadline, time.Now().Add(commandWriteTimeout)),
		)
		err := gen.conn.Send(sendContext, attempt.native)
		cancel()
		if err != nil {
			// The connection fails itself on a write error; the serve loop ends the generation next.
			attempt.request.finish(attempt.request.responder.RejectUnavailable("ESPHome command write failed"))
			return err
		}
		evidence, err := attempt.request.responder.Accept()
		attempt.request.finish(err)
		if err != nil {
			continue
		}
		attempt.evidence = evidence
		if cached, known := gen.cache[attempt.route.EntityID]; known &&
			stateMatches(attempt.route.Kind, cached.Value, attempt.target) {
			if publishEvidenceErr := w.publishEvidence(ctx, attempt, cached); publishEvidenceErr != nil {
				return publishEvidenceErr
			}
			continue
		}
		w.nextToken++
		attempt.token = w.nextToken
		attempt.timer = w.expireAt(attempt.request.deadline, attempt.token)
		queue.active = attempt
	}
	return nil
}

// publishLinked publishes a push through the active attempt's evidence and resolves the attempt on a match.
func (w *worker) publishLinked(
	ctx context.Context,
	queue *commandQueue,
	observation adapter.Observation,
) error {
	attempt := queue.active
	if !time.Now().Before(attempt.request.deadline) {
		return w.publishAfterExpiry(ctx, queue, observation)
	}
	if _, err := attempt.evidence.PublishObservation(ctx, observation); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil &&
			!time.Now().Before(attempt.request.deadline) {
			return w.publishAfterExpiry(ctx, queue, observation)
		}
		return &sessionError{operation: "publish linked observation", err: err}
	}
	if !stateMatches(attempt.route.Kind, observation.Value, attempt.target) {
		return nil
	}
	attempt.stopTimer()
	queue.active = nil
	return nil
}

// publishEvidence links the cached State of a live subscription. The firmware sends no push for an unchanged value,
// so the cache is the device's current State.
func (w *worker) publishEvidence(ctx context.Context, attempt *commandAttempt, observation adapter.Observation) error {
	if _, err := attempt.evidence.PublishObservation(ctx, observation); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil &&
			!time.Now().Before(attempt.request.deadline) {
			if _, publishErr := w.adapter.session.PublishObservation(ctx, observation); publishErr != nil {
				return w.publicationFailure(ctx, publishErr)
			}
			return nil
		}
		return &sessionError{operation: "publish linked observation", err: err}
	}
	return nil
}

// publishAfterExpiry preserves a late State report without linking it to an expired Command.
func (w *worker) publishAfterExpiry(
	ctx context.Context,
	queue *commandQueue,
	observation adapter.Observation,
) error {
	queue.active.stopTimer()
	queue.active = nil
	if _, err := w.adapter.session.PublishObservation(ctx, observation); err != nil {
		return w.publicationFailure(ctx, err)
	}
	return nil
}

func earlierDeadline(first, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}

// handleExpiry drops an awaiting attempt whose deadline passed and starts the next one.
func (w *worker) handleExpiry(ctx context.Context, gen *generation, token uint64) error {
	for _, queue := range gen.queues {
		if queue.active != nil && queue.active.token == token {
			queue.active = nil
			return w.startNext(ctx, gen, queue)
		}
	}
	return nil
}

// expireAt delivers token to the worker at deadline, unless the worker has stopped.
func (w *worker) expireAt(deadline time.Time, token uint64) *time.Timer {
	return time.AfterFunc(time.Until(deadline), func() {
		select {
		case w.expired <- token:
		case <-w.done:
		}
	})
}

// prepareSet decodes set parameters for the route and builds the target State and upstream request.
func prepareSet(route boundRoute, parameters json.RawMessage) (json.RawMessage, nativeapi.Command, error) {
	key, deviceID := route.Upstream.Key, route.Upstream.DeviceID
	switch route.Kind {
	case routeSwitchPower, routeLightPower:
		decoded, err := decodePowerSetParameters(parameters)
		if err != nil {
			return nil, nil, err
		}
		target, _ := json.Marshal(decoded.Value)
		if route.Kind == routeSwitchPower {
			return target, nativeapi.SwitchCommand{Key: key, DeviceID: deviceID, On: decoded.Value}, nil
		}
		return target, nativeapi.LightCommand{Key: key, DeviceID: deviceID, On: decoded.Value}, nil
	case routeLightBrightness:
		decoded, err := decodeBrightnessSetParameters(parameters)
		if err != nil {
			return nil, nil, err
		}
		target, _ := json.Marshal(decoded.Value)
		if decoded.Value == 0 {
			return target, nativeapi.LightCommand{Key: key, DeviceID: deviceID, On: false}, nil
		}
		brightness := float64(decoded.Value) / 100 //nolint:mnd // Percent to the device's 0..1 scale.
		return target, nativeapi.LightCommand{Key: key, DeviceID: deviceID, On: true, Brightness: &brightness}, nil
	case routeTemperature, routeHumidity, routePressure, routeNumeric, routeBinary:
		fallthrough
	default:
		return nil, nil, errors.New("esphome: Entity has no set operation")
	}
}

func decodePowerSetParameters(parameters json.RawMessage) (contractpowerv1.SetParameters, error) {
	codecs, err := contractpowerv1.Compile()
	if err != nil {
		return contractpowerv1.SetParameters{}, err
	}
	decoded, _, err := codecs.SetParameters.Decode(parameters)
	if err == nil {
		err = contractpowerv1.ValidateSetParameters(powerSupport(), contractpowerv1.SetSupport{}, decoded)
	}
	return decoded, err
}

func decodeBrightnessSetParameters(parameters json.RawMessage) (contractbrightnessv1.SetParameters, error) {
	codecs, err := contractbrightnessv1.Compile()
	if err != nil {
		return contractbrightnessv1.SetParameters{}, err
	}
	decoded, _, err := codecs.SetParameters.Decode(parameters)
	if err == nil {
		support := brightnessSupport()
		err = contractbrightnessv1.ValidateSetParameters(support, support.Operations.Set, decoded)
	}
	return decoded, err
}
