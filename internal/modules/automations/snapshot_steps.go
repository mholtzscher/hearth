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
		switch body := step.Body.(type) {
		case IfStep:
			if found := findSnapshotStep(body.Then, id); found != nil {
				return found
			}
			if found := findSnapshotStep(body.Else, id); found != nil {
				return found
			}
		case ChooseStep:
			for _, branch := range body.Branches {
				if found := findSnapshotStep(branch.Steps, id); found != nil {
					return found
				}
			}
			if found := findSnapshotStep(body.Default, id); found != nil {
				return found
			}
		case CommandStep, DelayStep, NotificationStep, FunctionStep, ReadStateStep, SetVariablesStep:
		}
	}
	return nil
}
