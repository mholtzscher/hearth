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
	switch body := step.Body.(type) {
	case CommandStep, DelayStep:
		return nil
	case IfStep:
		return [][]Step{body.Then, body.Else}
	case ChooseStep:
		sequences := make([][]Step, 0, len(body.Branches)+1)
		for _, branch := range body.Branches {
			sequences = append(sequences, branch.Steps)
		}
		return append(sequences, body.Default)
	default:
		panic("invalid normalized Step body")
	}
}
