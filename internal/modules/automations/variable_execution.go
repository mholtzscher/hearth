package automations

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

func (service *Service) initializeValues(ctx context.Context, run Run, values *runValues) bool {
	assigned, raw, err := resolveAssignments(ctx, values, run.Snapshot.Variables, "/variables")
	if err != nil {
		return service.failValues(ctx, run, values, nil, "/variables", err, nil)
	}
	env := values.env
	env.Vars = assigned
	if err = exprvalue.ValidateEnvironment(env); err != nil {
		return service.failValues(ctx, run, values, nil, "/variables", err, nil)
	}
	record := values.record(nil, service.dependencies.Now(), VariablesInitialized{Values: raw})
	if !service.commitValues(ctx, run, values, record) {
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
	path := stepBindingPath(run.Snapshot.Steps, id, "/steps") + "/assignments"
	assigned, raw, err := resolveAssignments(ctx, values, step.Assignments, path)
	if err != nil {
		return service.failValues(ctx, run, values, &id, path, err, nil)
	}
	vars := make(map[string]any, len(values.env.Vars)+len(assigned))
	for name, value := range values.env.Vars {
		vars[name] = value
	}
	for name, value := range assigned {
		vars[name] = value
	}
	env := values.env
	env.Vars = vars
	if err = exprvalue.ValidateEnvironment(env); err != nil {
		return service.failValues(ctx, run, values, &id, path, err, nil)
	}
	record := values.record(&id, service.dependencies.Now(), VariablesAssigned{Values: raw})
	if !service.commitValues(ctx, run, values, record) {
		return false
	}
	values.env = env
	return true
}

func (values *runValues) withOutput(id StepID, output exprvalue.Value) (exprvalue.Environment, error) {
	env := values.env
	env.Steps = make(map[string]any, len(values.env.Steps)+1)
	for key, value := range values.env.Steps {
		env.Steps[key] = value
	}
	env.Steps[string(id)] = map[string]any{"output": output.Data}
	return env, exprvalue.ValidateEnvironment(env)
}
