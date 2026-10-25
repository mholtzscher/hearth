package automations

import (
	"errors"
	"fmt"
)

// StepKind identifies the closed family of a normalized Step.
type StepKind string

const (
	// StepKindCommand dispatches one static Entity Operation.
	StepKindCommand StepKind = "command"
	// StepKindNotification sends one static notification message.
	StepKindNotification StepKind = "notification"
	StepKindFunction     StepKind = "function"
	// StepKindIf selects Then or Else using one Condition root.
	StepKindIf StepKind = "if"
	// StepKindChoose selects the first matching alternative or Default.
	StepKindChoose StepKind = "choose"
	// StepKindDelay waits for one fixed elapsed duration.
	StepKindDelay        StepKind = "delay"
	StepKindSetVariables StepKind = "set_variables"
	StepKindReadState    StepKind = "read_state"
)

// BranchID identifies an alternative within its Choose Step.
type BranchID string

// IfStep contains a predicate and its nonempty arms. Nil Else means omitted.
type IfStep struct {
	Conditions Condition
	Then       []Step
	Else       []Step
}

// ChooseStep contains ordered alternatives. Nil Default means omitted.
type ChooseStep struct {
	Branches []ChooseBranch
	Default  []Step
}

// ChooseBranch is one stable, identified alternative with a nonempty sequence.
type ChooseBranch struct {
	ID         BranchID
	Conditions Condition
	Steps      []Step
}

// CommandLeaves returns all Commands and their IDs in stable zero-based attempt order.
// Input must be an unchanged normalized definition sequence from DecodeDefinition
// or NormalizeDefinition. This helper does not validate arbitrary or cyclic input.
// It walks sequences left to right, If Then before Else, and Choose alternatives
// before Default. Returned Commands borrow the normalized definition's parameter bytes.
func CommandLeaves(steps []Step) []CommandLeaf {
	leaves := make([]CommandLeaf, 0)
	visitCommandLeaves(steps, func(step CommandLeaf) { leaves = append(leaves, step) })
	return leaves
}

func visitCommandLeaves(steps []Step, visit func(CommandLeaf)) {
	for _, step := range steps {
		switch body := step.Body.(type) {
		case CommandStep:
			visit(CommandLeaf{ID: step.ID, Command: body})
		case IfStep:
			visitCommandLeaves(body.Then, visit)
			visitCommandLeaves(body.Else, visit)
		case ChooseStep:
			for _, branch := range body.Branches {
				visitCommandLeaves(branch.Steps, visit)
			}
			visitCommandLeaves(body.Default, visit)
		case DelayStep, NotificationStep, FunctionStep, SetVariablesStep, ReadStateStep:
			// Non-Command leaves never consume a Command attempt position.
		default:
			panic("invalid normalized Step body")
		}
	}
}

const (
	automationAllStepsMaxCount       = 64
	automationStepMaxDepth           = 8
	automationTotalConditionMaxNodes = 256
)

// stepTreePreparation bounds arbitrary input before copying any recursive payload.
type stepTreePreparation struct {
	ids            map[StepID]bool
	triggerIDs     map[TriggerID]bool
	nodes          int
	sideEffects    int
	conditionNodes int
}

func (walk *stepTreePreparation) condition(root Condition, branch bool) (Condition, error) {
	conditions := newConditionTreeWalk()
	conditions.allowTrigger = branch
	conditions.triggerIDs = walk.triggerIDs
	if err := conditions.visit(&root, 1); err != nil {
		return Condition{}, err
	}
	walk.conditionNodes += conditions.nodes
	if walk.conditionNodes > automationTotalConditionMaxNodes {
		return Condition{}, definitionIssue("/steps", "definition exceeds the total Condition node bound")
	}
	return cloneAutomationCondition(root), nil
}

func (walk *stepTreePreparation) sequence(steps []Step, depth int, path string) ([]Step, error) {
	if depth > automationStepMaxDepth {
		return nil, definitionIssue("/steps", "Step tree exceeds the maximum depth")
	}
	if len(steps) < 1 || len(steps) > automationStepMaxCount {
		return nil, definitionIssue("/steps", "each present sequence needs 1 to 32 Steps")
	}
	normalized := make([]Step, 0, len(steps))
	for index, input := range steps {
		walk.nodes++
		if walk.nodes > automationAllStepsMaxCount {
			return nil, definitionIssue("/steps", "definition exceeds the total Step node bound")
		}
		if _, err := ParseStepID(string(input.ID)); err != nil {
			return nil, err
		}
		if walk.ids[input.ID] {
			return nil, definitionIssue("/steps", "Step IDs must be globally unique")
		}
		walk.ids[input.ID] = true
		stepPath := fmt.Sprintf("%s/%d", path, index)
		step, err := walk.step(input, depth, stepPath)
		if err != nil {
			if binding, ok := errors.AsType[*bindingError](err); ok {
				return nil, definitionIssue(stepPath+"/"+binding.path, dataFailureCode(err))
			}
			return nil, err
		}
		normalized = append(normalized, step)
	}
	return normalized, nil
}

func (walk *stepTreePreparation) step(input Step, depth int, path string) (Step, error) {
	switch input.Body.(type) {
	case CommandStep, NotificationStep, FunctionStep, SetVariablesStep, ReadStateStep:
		walk.sideEffects++
		if walk.sideEffects > automationStepMaxCount {
			return Step{}, definitionIssue("/steps", "definition exceeds 32 side-effect Steps")
		}
	case IfStep, ChooseStep, DelayStep:
		// Control flow and delays count only toward the tree bounds.
	default:
		return Step{}, definitionIssue("/steps", "unsupported Step body")
	}
	switch body := input.Body.(type) {
	case SetVariablesStep:
		if len(body.Assignments) == 0 {
			return Step{}, invalid("assignments cannot be empty")
		}
		assignments, err := normalizeVariables(body.Assignments, false)
		return Step{ID: input.ID, Body: SetVariablesStep{Assignments: assignments}}, bindingAt(err, "assignments")
	case ReadStateStep:
		return normalizeReadStateStep(input.ID, body)
	case CommandStep:
		return normalizeCommandStepValue(input.ID, body)
	case NotificationStep:
		return normalizeNotificationStep(input.ID, body)
	case FunctionStep:
		return normalizeFunctionStep(input.ID, body)
	case IfStep:
		return walk.ifStep(input.ID, body, depth, path)
	case ChooseStep:
		return walk.chooseStep(input.ID, body, depth, path)
	case DelayStep:
		return normalizeDelayStep(input.ID, body)
	default:
		return Step{}, definitionIssue("/steps", "unsupported Step body")
	}
}

func (walk *stepTreePreparation) optionalSequence(steps []Step, depth int, path string) ([]Step, error) {
	if steps == nil {
		return nil, nil // Nil means the optional arm is omitted.
	}
	return walk.sequence(steps, depth, path)
}

func (walk *stepTreePreparation) ifStep(id StepID, input IfStep, depth int, path string) (Step, error) {
	condition, err := walk.condition(input.Conditions, true)
	if err != nil {
		return Step{}, bindingAt(err, "conditions")
	}
	then, err := walk.sequence(input.Then, depth+1, path+"/then")
	if err != nil {
		return Step{}, err
	}
	otherwise, err := walk.optionalSequence(input.Else, depth+1, path+"/else")
	if err != nil {
		return Step{}, err
	}
	return Step{ID: id, Body: IfStep{Conditions: condition, Then: then, Else: otherwise}}, nil
}

func (walk *stepTreePreparation) chooseStep(id StepID, input ChooseStep, depth int, path string) (Step, error) {
	if len(input.Branches) < 1 || len(input.Branches) > automationStepMaxCount {
		return Step{}, definitionIssue("/steps", "Choose needs 1 to 32 alternatives")
	}
	branches := make([]ChooseBranch, 0, len(input.Branches))
	seen := make(map[BranchID]bool)
	for index, branch := range input.Branches {
		if !subjectSlugPattern.MatchString(string(branch.ID)) || seen[branch.ID] {
			return Step{}, definitionIssue("/steps", "Choose branch IDs must be valid and unique within their Step")
		}
		seen[branch.ID] = true
		condition, err := walk.condition(branch.Conditions, true)
		if err != nil {
			return Step{}, bindingAt(err, fmt.Sprintf("branches/%d/conditions", index))
		}
		children, err := walk.sequence(branch.Steps, depth+1, fmt.Sprintf("%s/branches/%d/steps", path, index))
		if err != nil {
			return Step{}, err
		}
		branches = append(branches, ChooseBranch{ID: branch.ID, Conditions: condition, Steps: children})
	}
	fallback, err := walk.optionalSequence(input.Default, depth+1, path+"/default")
	if err != nil {
		return Step{}, err
	}
	return Step{ID: id, Body: ChooseStep{Branches: branches, Default: fallback}}, nil
}
