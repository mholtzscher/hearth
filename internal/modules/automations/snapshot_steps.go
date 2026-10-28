package automations

// StepWithPreparedSnapshot resolves one instruction in an unchanged prepared definition.
func StepWithPreparedSnapshot(snapshot Definition, id StepID) (Step, error) {
	step := findSnapshotStep(snapshot.Steps, id)
	if step == nil {
		return Step{}, invalid("snapshot has no Step %q", id)
	}
	return *step, nil
}

// findSnapshotStep consumes a prepared tree and resolves immutable evidence identity.
func findSnapshotStep(steps []Step, id StepID) *Step {
	for index := range steps {
		step := &steps[index]
		if step.ID == id {
			return step
		}
		var sequences [][]Step
		switch body := step.Body.(type) {
		case IfStep:
			sequences = [][]Step{body.Then, body.Else}
		case ChooseStep:
			for _, branch := range body.Branches {
				sequences = append(sequences, branch.Steps)
			}
			sequences = append(sequences, body.Default)
		case CommandStep, DelayStep, NotificationStep, FunctionStep, ReadStateStep, SetVariablesStep:
		}
		for _, sequence := range sequences {
			if found := findSnapshotStep(sequence, id); found != nil {
				return found
			}
		}
	}
	return nil
}
