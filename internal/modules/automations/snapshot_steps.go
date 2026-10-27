package automations

import "strconv"

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
	step, _ := snapshotStepPath(steps, id, "/steps")
	return step
}

func stepBindingPath(steps []Step, id StepID) string {
	_, path := snapshotStepPath(steps, id, "/steps")
	return path
}

//nolint:gocognit // One bounded traversal resolves both immutable identity and its definition JSON Pointer.
func snapshotStepPath(steps []Step, id StepID, prefix string) (*Step, string) {
	for index := range steps {
		step := &steps[index]
		path := prefix + "/" + strconv.Itoa(index)
		if step.ID == id {
			return step, path
		}
		switch body := step.Body.(type) {
		case IfStep:
			if found, pointer := snapshotStepPath(body.Then, id, path+"/then"); found != nil {
				return found, pointer
			}
			if found, pointer := snapshotStepPath(body.Else, id, path+"/else"); found != nil {
				return found, pointer
			}
		case ChooseStep:
			for branchIndex, branch := range body.Branches {
				if found, pointer := snapshotStepPath(
					branch.Steps,
					id,
					path+"/branches/"+strconv.Itoa(branchIndex)+"/steps",
				); found != nil {
					return found, pointer
				}
			}
			if found, pointer := snapshotStepPath(body.Default, id, path+"/default"); found != nil {
				return found, pointer
			}
		case CommandStep, DelayStep, NotificationStep, FunctionStep, ReadStateStep, SetVariablesStep:
		}
	}
	return nil, ""
}
