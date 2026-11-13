package esphome

import (
	"context"
	"errors"
	"math"
	"time"

	contractcoverv1 "github.com/mholtzscher/hearth/entitytypes/coverv1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkcoverv1 "github.com/mholtzscher/hearth/sdk/adapter/coverv1"
)

const coverPercentMaximum = 100

func coverSupport(stop bool) sdkcoverv1.Support {
	support := sdkcoverv1.Support{Operations: sdkcoverv1.OperationSupport{Set: &sdkcoverv1.SetSupport{}}}
	if stop {
		support.Operations.Stop = &sdkcoverv1.StopSupport{}
	}
	return support
}

func coverState(update nativeapi.StateUpdate) (contractcoverv1.State, error) {
	if math.IsNaN(update.Position) || math.IsInf(update.Position, 0) || update.Position < 0 || update.Position > 1 {
		return contractcoverv1.State{}, errUnprojectable
	}
	return contractcoverv1.State{
		Position: int64(math.Round(update.Position * coverPercentMaximum)), Motion: update.CoverOperation,
	}, nil
}

func prepareCoverCommand(route boundRoute, command adapter.Command) (nativeapi.CoverCommand, error) {
	native := nativeapi.CoverCommand{Key: route.Upstream.Key, DeviceID: route.Upstream.DeviceID}
	codecs, err := contractcoverv1.Compile()
	if err != nil {
		return native, err
	}
	switch command.OperationName {
	case contractcoverv1.OperationSet:
		if route.Cover.Operations.Set == nil {
			return native, errors.New("esphome: cover position control is unsupported")
		}
		parameters, _, decodeErr := codecs.SetParameters.Decode(command.Parameters)
		if decodeErr != nil {
			return native, decodeErr
		}
		position := float64(parameters.Position) / coverPercentMaximum
		native.Position = &position
	case contractcoverv1.OperationStop:
		if route.Cover.Operations.Stop == nil {
			return native, errors.New("esphome: cover stop is unsupported")
		}
		if _, _, decodeErr := codecs.StopParameters.Decode(command.Parameters); decodeErr != nil {
			return native, decodeErr
		}
		native.Stop = true
	default:
		return native, errors.New("esphome: unsupported cover operation")
	}
	return native, nil
}

// handleCoverCommand serializes writes, not travel. A pending target must never delay Stop.
func (w *worker) handleCoverCommand(
	ctx context.Context,
	gen *generation,
	route boundRoute,
	request *commandRequest,
) error {
	native, err := prepareCoverCommand(route, request.command)
	if err != nil {
		request.finish(request.responder.Reject("invalid or unsupported cover command"))
		return nil //nolint:nilerr // The rejection is the outcome, not a worker failure.
	}
	if !time.Now().Before(request.deadline) {
		request.finish(request.responder.RejectUnavailable("Command expired before dispatch"))
		return nil
	}
	pruneCoverTargets(gen, route.Upstream)
	if !native.Stop && len(gen.coverTargets[route.Upstream]) >= commandQueueLimit {
		request.finish(request.responder.RejectUnavailable("too many pending Commands for this ESPHome cover"))
		return nil
	}
	sendContext, cancel := context.WithDeadline(
		ctx,
		earlierDeadline(request.deadline, time.Now().Add(commandWriteTimeout)),
	)
	err = gen.conn.Send(sendContext, native)
	cancel()
	if err != nil {
		request.finish(request.responder.RejectUnavailable("ESPHome command write failed"))
		return err
	}
	if native.Stop {
		// The frame was sent even if acceptance fails. Never let older evidence imply that travel continued on request.
		clearCoverTargets(gen, route.Upstream)
		_, acceptErr := request.responder.Accept()
		request.finish(acceptErr)
		return nil
	}
	evidence, err := request.responder.Accept()
	request.finish(err)
	if err != nil {
		return nil //nolint:nilerr // Acceptance failure has already settled the responder.
	}
	attempt := &commandAttempt{
		request: request, route: route, target: request.command.Parameters, native: native, evidence: evidence,
	}
	if cached, known := gen.cache[route.EntityID]; known && stateMatches(routeCover, cached.Value, attempt.target) {
		return w.publishEvidence(ctx, attempt, cached)
	}
	w.nextToken++
	attempt.token = w.nextToken
	attempt.timer = w.expireAt(request.deadline, attempt.token)
	gen.coverTargets[route.Upstream] = append(gen.coverTargets[route.Upstream], attempt)
	return nil
}

func clearCoverTargets(gen *generation, upstream upstreamRef) {
	for _, attempt := range gen.coverTargets[upstream] {
		attempt.stopTimer()
	}
	delete(gen.coverTargets, upstream)
}

func pruneCoverTargets(gen *generation, upstream upstreamRef) {
	attempts := gen.coverTargets[upstream]
	kept := attempts[:0]
	for _, attempt := range attempts {
		if time.Now().Before(attempt.request.deadline) {
			kept = append(kept, attempt)
		} else {
			attempt.stopTimer()
		}
	}
	gen.coverTargets[upstream] = kept
}

// publishCover links a push to at most one matching target. Other State is ordinary evidence.
func (w *worker) publishCover(
	ctx context.Context,
	gen *generation,
	upstream upstreamRef,
	observation adapter.Observation,
) error {
	pruneCoverTargets(gen, upstream)
	for index, attempt := range gen.coverTargets[upstream] {
		if !stateMatches(routeCover, observation.Value, attempt.target) {
			continue
		}
		attempt.stopTimer()
		attempts := gen.coverTargets[upstream]
		gen.coverTargets[upstream] = append(attempts[:index], attempts[index+1:]...)
		return w.publishEvidence(ctx, attempt, observation)
	}
	if _, err := w.adapter.session.PublishObservation(ctx, observation); err != nil {
		return w.publicationFailure(ctx, err)
	}
	return nil
}
