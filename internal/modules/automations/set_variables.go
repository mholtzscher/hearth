package automations

type SetVariablesStep struct{ Assignments map[VariableName]ValueBinding }

func (SetVariablesStep) isStepBody() {}

func normalizeSetVariablesStep(id StepID, body SetVariablesStep) (Step, error) {
	if len(body.Assignments) == 0 {
		return Step{}, invalid("assignments cannot be empty")
	}
	assignments, err := normalizeVariables(body.Assignments, false)
	if err != nil {
		return Step{}, err
	}
	return Step{ID: id, Body: SetVariablesStep{Assignments: assignments}}, nil
}
