package automations

// findSnapshotStep consumes a prepared tree and resolves immutable evidence identity.
func findSnapshotStep(steps []Step, id StepID) *Step {
	for index := range steps {
		step := &steps[index]
		if step.ID == id {
			return step
		}
		for _, sequence := range snapshotChildSequences(*step) {
			if found := findSnapshotStep(sequence, id); found != nil {
				return found
			}
		}
	}
	return nil
}

func snapshotChildSequences(step Step) [][]Step {
	switch step.Kind {
	case StepKindCommand, StepKindDelay:
		return nil
	case StepKindIf:
		return [][]Step{step.If.Then, step.If.Else}
	case StepKindChoose:
		sequences := make([][]Step, 0, len(step.Choose.Branches)+1)
		for _, branch := range step.Choose.Branches {
			sequences = append(sequences, branch.Steps)
		}
		return append(sequences, step.Choose.Default)
	default:
		return nil
	}
}
