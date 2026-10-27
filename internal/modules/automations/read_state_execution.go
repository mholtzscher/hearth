//nolint:goconst // State-read failure classes are fixed protocol tokens, never upstream diagnostics.
package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

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
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, id, service.executionStopReason())
		return false
	}
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
		return service.failValues(ctx, run, id, StepKindReadState, path, &exprvalue.Failure{Code: code}, nil)
	}
	entry, covered := snapshot.Entries[step.EntityID]
	if !covered {
		return service.failValues(
			ctx,
			run,
			id,
			StepKindReadState,
			path,
			&exprvalue.Failure{Code: "read_state_snapshot_incomplete"},
			nil,
		)
	}
	body, output, err := selectReadState(step, entry, at)
	if err != nil {
		return service.failValues(ctx, run, id, StepKindReadState, path, err, nil)
	}
	if body.UnusableReason != nil {
		if step.OnUnusable != ReadStateNull {
			state := &StateReadFailure{
				EntityID:       step.EntityID,
				Pointer:        step.Pointer,
				ReadAt:         at,
				Observation:    body.Observation,
				UnusableReason: *body.UnusableReason,
			}
			return service.failValues(
				ctx,
				run,
				id,
				StepKindReadState,
				path,
				&exprvalue.Failure{Code: "read_state_unusable"},
				state,
			)
		}
		output, _ = exprvalue.OwnValue(nil)
	}
	body.Output = output.JSON
	env, err := values.withOutput(id, output)
	if err != nil {
		return service.failValues(ctx, run, id, StepKindReadState, path, err, nil)
	}
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, id, service.executionStopReason())
		return false
	}
	body.At = at
	record := StepExecutionRecord{RunID: run.ID, StepID: id, Evidence: body}
	if !service.commitReached(ctx, run, record, StepKindReadState, path) {
		return false
	}
	values.env = env
	return true
}

// selectReadState decides from one immutable read. Operational corruption and
// value-domain failures are separate from the five deliberate absence reasons.
func selectReadState(
	step ReadStateStep,
	entry devices.EntityStateSnapshotEntry,
	at time.Time,
) (StateRead, exprvalue.Value, error) {
	body := StateRead{ReadAt: at}
	corrupt := &exprvalue.Failure{Code: "read_state_corrupt"}
	if entry.EntityID != step.EntityID || (!entry.Exists && entry.State != nil) {
		return body, exprvalue.Value{}, corrupt
	}
	if !entry.Exists {
		reason := ConditionUnknownEntityMissing
		body.UnusableReason = &reason
		return body, exprvalue.Value{}, nil
	}
	if entry.State == nil {
		reason := ConditionUnknownStateMissing
		body.UnusableReason = &reason
		return body, exprvalue.Value{}, nil
	}
	if entry.State.EntityID != step.EntityID {
		return body, exprvalue.Value{}, corrupt
	}
	body.Observation = &ObservationEvidence{
		ObservationID: entry.State.ObservationID,
		ObservedAt:    entry.State.ObservedAt.UTC(),
	}
	if validateObservationEvidence(*body.Observation) != nil || !utf8.Valid(entry.State.Value) ||
		!json.Valid(entry.State.Value) {
		return body, exprvalue.Value{}, corrupt
	}
	// Decode before fallback: malformed/unsupported numeric evidence is not absence.
	full, err := exprvalue.DecodeValue(json.RawMessage(entry.State.Value))
	if err != nil {
		if failure, ok := errors.AsType[*exprvalue.Failure](
			err,
		); ok && failure.Code == "expression_value_invalid" &&
			stateHasDuplicateKeys(entry.State.Value) {
			return body, exprvalue.Value{}, corrupt
		}
		return body, exprvalue.Value{}, err
	}
	if reason, unusable := boundedEvidenceUnknownReason(
		EntityStateCondition{MaxAgeSeconds: step.MaxAgeSeconds},
		body.Observation.ObservedAt,
		at,
	); unusable {
		body.UnusableReason = &reason
		return body, exprvalue.Value{}, nil
	}
	tokens, err := parseJSONPointer(step.Pointer)
	if err != nil {
		return body, exprvalue.Value{}, corrupt
	}
	selected, found := lookupJSONPointer(full.Data, tokens)
	if !found {
		reason := ConditionUnknownPointerMissing
		body.UnusableReason = &reason
		return body, exprvalue.Value{}, nil
	}
	output, err := exprvalue.OwnValue(selected)
	return body, output, err
}

// stateHasDuplicateKeys distinguishes corrupt JSON structure from a valid JSON
// number that the expression value domain cannot represent. Tokens retain
// [json.Number], so this diagnostic check never expands or rounds a number.
//
//nolint:gocognit,nestif // One bounded token pass tracks object-key position without decoding or converting JSON values again.
func stateHasDuplicateKeys(raw []byte) bool {
	type frame struct {
		keys    map[string]bool
		keyNext bool
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var stack []frame
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		} // Syntax has already passed json.Valid.
		if token == json.Delim('}') || token == json.Delim(']') {
			stack = stack[:len(stack)-1]
			continue
		}
		if len(stack) > 0 {
			current := &stack[len(stack)-1]
			if current.keys != nil && current.keyNext {
				key, ok := token.(string)
				if !ok {
					return false
				}
				if current.keys[key] {
					return true
				}
				current.keys[key] = true
				current.keyNext = false
				continue
			}
			if current.keys != nil {
				current.keyNext = true
			}
		}
		switch token {
		case json.Delim('{'):
			stack = append(stack, frame{keys: map[string]bool{}, keyNext: true})
		case json.Delim('['):
			stack = append(stack, frame{})
		}
		if len(stack) > automationValueMaxDepth {
			return false
		}
	}
}
