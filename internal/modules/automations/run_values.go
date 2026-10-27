//nolint:goconst // Safe data-failure tokens are shared with the pure bridge's error protocol.
package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// runValues belongs to one worker and is discarded on interruption, never replayed.
type runValues struct {
	evaluationContext context.Context
	stopContext       context.Context
	conditionPaths    map[ConditionID]string
	branchPath        string
	env               exprvalue.Environment
}

func newRunValues(run Run) (*runValues, error) {
	trigger := map[string]any{
		"kind":                string(CauseSource(run.Cause)),
		"matched_trigger_ids": append([]TriggerID{}, run.MatchedTriggerIDs...),
	}
	// JSON projections below preserve original number tokens until strict ingress.
	var detail any
	switch cause := run.Cause.(type) {
	case ManualCause, ScheduleCause:
	case HeldStateCause:
		detail = map[string]any{
			"trigger_id": cause.Evidence.TriggerID,
			"started_at": cause.Evidence.StartedAt,
			"due_at":     cause.Evidence.DueAt,
		}
		trigger["evidence"] = detail
	case DeviceFactCause:
		switch fact := cause.Fact.(type) {
		case ObservationFact:
			observation := map[string]any{
				"fact_id":        fact.FactID,
				"family":         "observation",
				"entity_id":      fact.EntityID,
				"emitted_at":     fact.EmittedAt,
				"observation_id": fact.ObservationID,
				"disposition":    fact.Disposition,
				"value":          json.RawMessage(fact.Value),
			}
			if len(fact.PreviousValue) > 0 {
				observation["previous_value"] = json.RawMessage(fact.PreviousValue)
			}
			detail = observation
		case EntityEventFact:
			detail = map[string]any{"fact_id": fact.FactID, "family": "entity_event", "entity_id": fact.EntityID,
				"emitted_at": fact.EmittedAt, "event_id": fact.EventID, "name": fact.Name}
		}
		trigger["fact"] = detail
	default:
		return nil, &exprvalue.Failure{Code: "expression_value_invalid"}
	}
	triggerData := make(map[string]any, len(trigger))
	// Root envelopes aggregate fragments; do not charge their overhead against
	// an individual fact/evidence value's byte, depth or node allowance.
	for _, key := range []string{"kind", "matched_trigger_ids", "fact", "evidence"} {
		fragment, exists := trigger[key]
		if !exists {
			continue
		}
		raw, err := json.Marshal(fragment)
		if err != nil {
			return nil, &exprvalue.Failure{Code: "expression_value_invalid"}
		}
		owned, err := exprvalue.DecodeValue(raw)
		if err != nil {
			return nil, err
		}
		triggerData[key] = owned.Data
	}
	values := &runValues{env: exprvalue.Environment{
		Run: map[string]any{
			"id":            string(run.ID),
			"automation_id": string(run.AutomationID),
			"revision":      run.Revision,
			"started_at":    run.StartedAt.UTC().Format(time.RFC3339Nano),
		},
		Trigger: triggerData,
		Vars:    map[string]any{},
		Steps:   map[string]any{},
	}}
	return values, exprvalue.ValidateEnvironment(values.env)
}

func (values *runValues) resolve(ctx context.Context, binding ValueBinding, path string) (exprvalue.Value, error) {
	if err := values.evaluationStopped(ctx); err != nil {
		return exprvalue.Value{}, err
	}
	var result exprvalue.Value
	var err error
	switch body := binding.Body.(type) {
	case LiteralValue:
		result, err = exprvalue.DecodeValue(body.Value)
	case ExpressionValue:
		result, err = exprvalue.Evaluate(body.program, values.env)
	default:
		err = &exprvalue.Failure{Code: "expression_value_invalid"}
	}
	if err != nil {
		return exprvalue.Value{}, &bindingError{path: path, cause: err}
	}
	if err = values.evaluationStopped(ctx); err != nil {
		return exprvalue.Value{}, err
	}
	return result, nil
}

func (values *runValues) evaluationStopped(ctx context.Context) error {
	for _, lifetime := range []context.Context{ctx, values.evaluationContext, values.stopContext} {
		if lifetime != nil && lifetime.Err() != nil {
			return lifetime.Err()
		}
	}
	return nil
}

// Effect literals are opaque destination JSON, not expression ingress.
func (values *runValues) resolvePayload(
	ctx context.Context,
	binding ValueBinding,
	path string,
) (json.RawMessage, error) {
	if body, ok := binding.Body.(LiteralValue); ok {
		if len(body.Value) > automationValueMaxBytes {
			return nil, &bindingError{path: path, cause: &exprvalue.Failure{Code: "expression_limit"}}
		}
		return append(json.RawMessage(nil), body.Value...), nil
	}
	result, err := values.resolve(ctx, binding, path)
	return result.JSON, err
}

//nolint:gocognit // Recursive closed Step traversal retains exact definition JSON Pointers across every arm.
func stepBindingPath(steps []Step, id StepID, prefix string) string {
	for index, step := range steps {
		path := fmt.Sprintf("%s/%d", prefix, index)
		if step.ID == id {
			return path
		}
		switch body := step.Body.(type) {
		case IfStep:
			if result := stepBindingPath(body.Then, id, path+"/then"); result != "" {
				return result
			}
			if result := stepBindingPath(body.Else, id, path+"/else"); result != "" {
				return result
			}
		case ChooseStep:
			for branchIndex, branch := range body.Branches {
				if result := stepBindingPath(
					branch.Steps,
					id,
					fmt.Sprintf("%s/branches/%d/steps", path, branchIndex),
				); result != "" {
					return result
				}
			}
			if result := stepBindingPath(body.Default, id, path+"/default"); result != "" {
				return result
			}
		case CommandStep, FunctionStep, NotificationStep, DelayStep, SetVariablesStep, ReadStateStep:
		}
	}
	return ""
}

func resolveAssignments(
	ctx context.Context,
	values *runValues,
	bindings map[VariableName]ValueBinding,
	path string,
) (map[string]any, json.RawMessage, error) {
	names := make([]string, 0, len(bindings))
	for name := range bindings {
		names = append(names, string(name))
	}
	sort.Strings(names)
	assigned := make(map[string]any, len(names))
	for _, name := range names {
		value, err := values.resolve(ctx, bindings[VariableName(name)], path+"/"+name)
		if err != nil {
			return nil, nil, err
		}
		assigned[name] = value.Data
	}
	batch, err := exprvalue.OwnValue(assigned)
	if err != nil {
		return nil, nil, err
	}
	object, ok := batch.Data.(map[string]any)
	if !ok {
		return nil, nil, &exprvalue.Failure{Code: "expression_value_invalid"}
	}
	return object, batch.JSON, nil
}
func (values *runValues) conditionRoot(root Condition, path string) {
	values.conditionPaths = map[ConditionID]string{}
	var visit func(Condition, string)
	visit = func(node Condition, pointer string) {
		switch body := node.Body.(type) {
		case ExpressionCondition:
			values.conditionPaths[node.ID] = pointer + "/value"
		case AllCondition:
			for index, child := range body.Children {
				visit(child, fmt.Sprintf("%s/children/%d", pointer, index))
			}
		case AnyCondition:
			for index, child := range body.Children {
				visit(child, fmt.Sprintf("%s/children/%d", pointer, index))
			}
		case NotCondition:
			visit(body.Child, pointer+"/child")
		case EntityStateCondition, TriggerCondition:
		}
	}
	visit(root, path)
}

func valueFailure(path string, err error, state *StateReadFailure) ValueFailure {
	result := ValueFailure{Path: path, FailureCode: dataFailureCode(err), State: state}
	if binding, ok := errors.AsType[*bindingError](err); ok && binding.path != "" {
		result.Path = binding.path
	}
	if failure, ok := errors.AsType[*exprvalue.Failure](err); ok && failure.End > failure.Start {
		result.SourceStart, result.SourceEnd = &failure.Start, &failure.End
	}
	return result
}

func encodeResolvedInputs(fields map[string]json.RawMessage) (json.RawMessage, error) {
	for _, fragment := range fields {
		if len(fragment) > automationValueMaxBytes {
			return nil, &exprvalue.Failure{Code: "expression_limit"}
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, &exprvalue.Failure{Code: "resolved_input_invalid"}
	}
	return raw, nil
}

func resolvedInputField(inputs json.RawMessage, name string) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(inputs, &fields) != nil {
		return nil
	}
	return fields[name]
}

func (service *Service) failInitialization(ctx context.Context, run Run, path string, cause error) bool {
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, "", service.executionStopReason())
		return false
	}
	init := RunValueInitialization{RecordedAt: service.dependencies.Now().UTC(),
		Body: InitializationFailed{Failure: valueFailure(path, cause, nil)}}
	write, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordRunValueInitialization(write, run.ID, init)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, "")
	}
	return false
}

func (service *Service) failValues(ctx context.Context, run Run, id StepID, kind StepKind,
	path string, cause error, state *StateReadFailure,
) bool {
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, id, service.executionStopReason())
		return false
	}
	write, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordStep(write, StepExecutionRecord{RunID: run.ID, StepID: id,
		Evidence: ValueResolutionFailed{Kind: kind, At: service.dependencies.Now().UTC(),
			Failure: valueFailure(path, cause, state)}})
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, id)
	}
	return false
}

// commitReached handles a definitive capacity rejection before an effect or context advance.
func (service *Service) commitReached(
	ctx context.Context,
	run Run,
	record StepExecutionRecord,
	kind StepKind,
	path string,
) bool {
	write, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordStep(write, record)
	cancel()
	if err == nil {
		return true
	}
	if failure, ok := errors.AsType[*exprvalue.Failure](err); ok && failure.Code == "run_data_limit" {
		return service.failValues(ctx, run, record.StepID, kind, path, err, nil)
	}
	service.interruptRunExecutorFault(ctx, run.ID, record.StepID)
	return false
}
