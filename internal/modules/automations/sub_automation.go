package automations

import (
	"context"
	"errors"
	"fmt"
)

// SubAutomationStep runs one current Automation definition without arguments.
type SubAutomationStep struct{ AutomationID AutomationID }

func (SubAutomationStep) isStepBody() {}

// SubAutomationStepInput identifies a reached SubAutomation in its parent Run.
type SubAutomationStepInput struct {
	ParentRunID RunID
	StepID      StepID
}

//nolint:gocognit // Traverses every arm while retaining the exact authoring path.
func (service *Service) validateSubAutomationReferences(ctx context.Context, steps []Step, path string) error {
	for i, step := range steps {
		p := fmt.Sprintf("%s/%d", path, i)
		switch body := step.Body.(type) {
		case SubAutomationStep:
			if _, err := service.repository.GetAutomation(ctx, body.AutomationID); err != nil {
				if errors.Is(err, ErrAutomationNotFound) {
					return definitionIssue(p+"/automation_id", "referenced Automation does not exist")
				}
				return err
			}
		case IfStep:
			if err := service.validateSubAutomationReferences(ctx, body.Then, p+"/then"); err != nil {
				return err
			}
			if err := service.validateSubAutomationReferences(ctx, body.Else, p+"/else"); err != nil {
				return err
			}
		case ChooseStep:
			for j, branch := range body.Branches {
				if err := service.validateSubAutomationReferences(
					ctx,
					branch.Steps,
					fmt.Sprintf("%s/branches/%d/steps", p, j),
				); err != nil {
					return err
				}
			}
			if err := service.validateSubAutomationReferences(ctx, body.Default, p+"/default"); err != nil {
				return err
			}
		case CommandStep, DelayStep, FunctionStep, NotificationStep, ReadStateStep, SetVariablesStep:
		}
	}
	return nil
}
