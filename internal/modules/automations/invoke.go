package automations

import (
	"context"
	"errors"
	"fmt"
)

// InvokeStep invokes one current Automation definition without arguments.
type InvokeStep struct{ AutomationID AutomationID }

func (InvokeStep) isStepBody() {}

// InvokeStepInput identifies a reached Invoke in its parent Run.
type InvokeStepInput struct {
	ParentRunID RunID
	StepID      StepID
}

//nolint:gocognit // Traverses every arm while retaining the exact authoring path.
func (service *Service) validateInvokeReferences(ctx context.Context, steps []Step, path string) error {
	for i, step := range steps {
		p := fmt.Sprintf("%s/%d", path, i)
		switch body := step.Body.(type) {
		case InvokeStep:
			if _, err := service.repository.GetAutomation(ctx, body.AutomationID); err != nil {
				if errors.Is(err, ErrAutomationNotFound) {
					return definitionIssue(p+"/automation_id", "referenced Automation does not exist")
				}
				return err
			}
		case IfStep:
			if err := service.validateInvokeReferences(ctx, body.Then, p+"/then"); err != nil {
				return err
			}
			if err := service.validateInvokeReferences(ctx, body.Else, p+"/else"); err != nil {
				return err
			}
		case ChooseStep:
			for j, branch := range body.Branches {
				if err := service.validateInvokeReferences(
					ctx,
					branch.Steps,
					fmt.Sprintf("%s/branches/%d/steps", p, j),
				); err != nil {
					return err
				}
			}
			if err := service.validateInvokeReferences(ctx, body.Default, p+"/default"); err != nil {
				return err
			}
		case CommandStep, DelayStep, FunctionStep, NotificationStep, ReadStateStep, SetVariablesStep:
		}
	}
	return nil
}
