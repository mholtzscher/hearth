package automations

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (service *Service) executeReadState(
	ctx context.Context,
	run Run,
	values *runValues,
	id StepID,
	step ReadStateStep,
) bool {
	path := stepBindingPath(run.Snapshot.Steps, id, "/steps")
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	snapshot, err := service.devices.GetEntityStateSnapshot(readContext, []devices.EntityID{step.EntityID})
	cancel()
	at := service.dependencies.Now().UTC()
	if err != nil {
		code := "read_state_read_failed"
		if errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
			code = "read_state_corrupt"
		}
		return service.failValues(ctx, run, values, &id, path, &exprvalue.Failure{Code: code}, nil)
	}
	entry, covered := snapshot.Entries[step.EntityID]
	if !covered {
		return service.failValues(
			ctx,
			run,
			values,
			&id,
			path,
			&exprvalue.Failure{Code: "read_state_snapshot_incomplete"},
			nil,
		)
	}
	body := StateCaptured{EntityID: step.EntityID, Pointer: step.Pointer, ReadAt: at}
	var reason ConditionUnknownReason
	var output exprvalue.Value
	switch {
	case !entry.Exists:
		if entry.State != nil {
			return service.failValues(ctx, run, values, &id, path, &exprvalue.Failure{Code: "read_state_corrupt"}, nil)
		}
		reason = ConditionUnknownEntityMissing
	case entry.State == nil:
		reason = ConditionUnknownStateMissing
	default:
		body.Observation = &ObservationEvidence{
			ObservationID: entry.State.ObservationID,
			ObservedAt:    entry.State.ObservedAt.UTC(),
		}
		if validateObservationEvidence(*body.Observation) != nil {
			return service.failValues(ctx, run, values, &id, path, &exprvalue.Failure{Code: "read_state_corrupt"}, nil)
		}
		// Validate JSON and all numeric evidence before unusable fallback. A broken
		// read is never treated as absence, even if its timestamp is also old.
		if !json.Valid(entry.State.Value) {
			return service.failValues(ctx, run, values, &id, path, &exprvalue.Failure{Code: "read_state_corrupt"}, nil)
		}
		full, decodeErr := exprvalue.DecodeValue(json.RawMessage(entry.State.Value))
		if decodeErr != nil {
			return service.failValues(ctx, run, values, &id, path, decodeErr, nil)
		}
		if bounded, unusable := boundedEvidenceUnknownReason(
			EntityStateCondition{MaxAgeSeconds: step.MaxAgeSeconds},
			body.Observation.ObservedAt,
			at,
		); unusable {
			reason = bounded
		} else {
			tokens, pointerErr := parseJSONPointer(step.Pointer)
			if pointerErr != nil {
				return service.failValues(
					ctx,
					run,
					values,
					&id,
					path,
					&exprvalue.Failure{Code: "read_state_corrupt"},
					nil,
				)
			}
			selected, found := lookupJSONPointer(full.Data, tokens)
			if !found {
				reason = ConditionUnknownPointerMissing
			} else {
				output, err = exprvalue.OwnValue(selected)
				if err != nil {
					return service.failValues(ctx, run, values, &id, path, err, nil)
				}
			}
		}
	}
	if reason != "" {
		if step.OnUnusable != ReadStateNull {
			state := &StateReadFailure{
				EntityID:       step.EntityID,
				Pointer:        step.Pointer,
				ReadAt:         at,
				Observation:    body.Observation,
				UnusableReason: reason,
			}
			return service.failValues(
				ctx,
				run,
				values,
				&id,
				path,
				&exprvalue.Failure{Code: "read_state_unusable"},
				state,
			)
		}
		body.UnusableReason = &reason
		output, _ = exprvalue.OwnValue(nil)
	}
	body.Output = output.JSON
	env, err := values.withOutput(id, output)
	if err != nil {
		return service.failValues(ctx, run, values, &id, path, err, nil)
	}
	record := values.record(&id, at, body)
	if !service.commitValues(ctx, run, values, record) {
		return false
	}
	values.env = env
	return true
}
