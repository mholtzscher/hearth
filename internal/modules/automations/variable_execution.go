package automations

import (
	"context"
	"maps"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

func (service *Service) initializeValues(ctx context.Context, run Run, values *runValues) bool {
	values.evaluationContext, values.stopContext = ctx, service.executionStop
	assigned, raw, err := resolveAssignments(ctx, values, run.Snapshot.Variables, "variables")
	if err != nil {
		return service.failInitialization(ctx, run, "variables", err)
	}
	env := values.env
	env.Vars = assigned
	if err = exprvalue.ValidateEnvironment(env); err != nil {
		return service.failInitialization(ctx, run, "variables", err)
	}
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, "", service.executionStopReason())
		return false
	}
	write, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.RecordRunValueInitialization(write, run.ID, RunValueInitialization{
		RecordedAt: service.dependencies.Now().UTC(), Body: ValuesInitialized{Values: raw},
	})
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, "")
		return false
	}
	values.env = env
	return true
}

func (service *Service) executeAssignment(
	ctx context.Context,
	run Run,
	values *runValues,
	id StepID,
	step SetVariablesStep,
) bool {
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, id, service.executionStopReason())
		return false
	}
	assigned, raw, err := resolveAssignments(ctx, values, step.Assignments, "assignments")
	if err != nil {
		return service.failValues(ctx, run, id, StepKindSetVariables, "assignments", err, nil)
	}
	vars := make(map[string]any, len(values.env.Vars)+len(assigned))
	maps.Copy(vars, values.env.Vars)
	maps.Copy(vars, assigned)
	if len(vars) > automationVariableMaxCount {
		return service.failValues(ctx, run, id, StepKindSetVariables, "assignments",
			&exprvalue.Failure{Code: failureExpressionLimit}, nil)
	}
	env := values.env
	env.Vars = vars
	if err = exprvalue.ValidateEnvironment(env); err != nil {
		return service.failValues(ctx, run, id, StepKindSetVariables, "assignments", err, nil)
	}
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, id, service.executionStopReason())
		return false
	}
	record := StepExecutionRecord{
		RunID:    run.ID,
		StepID:   id,
		Evidence: ValuesAssigned{At: service.dependencies.Now().UTC(), Values: raw},
	}
	if !service.commitReached(ctx, run, record) {
		return false
	}
	values.env = env
	return true
}

func (values *runValues) withOutput(id StepID, output exprvalue.Value) (exprvalue.Environment, error) {
	env := values.env
	env.Steps = make(map[string]any, len(values.env.Steps)+1)
	maps.Copy(env.Steps, values.env.Steps)
	env.Steps[string(id)] = map[string]any{"output": output.Data}
	return env, exprvalue.ValidateEnvironment(env)
}
