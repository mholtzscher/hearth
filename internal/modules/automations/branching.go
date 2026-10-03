package automations

// StepKind identifies the closed family of a normalized Step.
type StepKind string

const (
	// StepKindCommand dispatches one static Entity Operation.
	StepKindCommand StepKind = "command"
	// StepKindIf selects Then or Else using one Condition root.
	StepKindIf StepKind = "if"
	// StepKindChoose selects the first matching alternative or Default.
	StepKindChoose StepKind = "choose"
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

// CommandLeaves returns all Command Steps in stable zero-based attempt order.
// Input must be an unchanged normalized definition sequence from DecodeDefinition
// or NormalizeDefinition. This helper does not validate arbitrary or cyclic input.
// It walks sequences left to right, If Then before Else, and Choose alternatives
// before Default. Returned Steps borrow the normalized definition's parameter bytes.
func CommandLeaves(steps []Step) []Step {
	leaves := make([]Step, 0)
	visitCommandLeaves(steps, func(step Step) { leaves = append(leaves, step) })
	return leaves
}

func visitCommandLeaves(steps []Step, visit func(Step)) {
	for _, step := range steps {
		switch step.Kind {
		case StepKindCommand:
			visit(step)
		case StepKindIf:
			visitCommandLeaves(step.If.Then, visit)
			visitCommandLeaves(step.If.Else, visit)
		case StepKindChoose:
			for _, branch := range step.Choose.Branches {
				visitCommandLeaves(branch.Steps, visit)
			}
			visitCommandLeaves(step.Choose.Default, visit)
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
	commands       int
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

func (walk *stepTreePreparation) sequence(steps []Step, depth int) ([]Step, error) {
	if depth > automationStepMaxDepth {
		return nil, definitionIssue("/steps", "Step tree exceeds the maximum depth")
	}
	if len(steps) < 1 || len(steps) > automationStepMaxCount {
		return nil, definitionIssue("/steps", "each present sequence needs 1 to 32 Steps")
	}
	normalized := make([]Step, 0, len(steps))
	for _, input := range steps {
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
		step, err := walk.step(input, depth)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, step)
	}
	return normalized, nil
}

func (walk *stepTreePreparation) step(input Step, depth int) (Step, error) {
	kind := input.Kind
	if kind == "" {
		kind = StepKindCommand
	}
	switch kind {
	case StepKindCommand:
		if input.If != nil || input.Choose != nil {
			return Step{}, definitionIssue("/steps", "Command family payload mismatch")
		}
		walk.commands++
		if walk.commands > automationStepMaxCount {
			return Step{}, definitionIssue("/steps", "definition exceeds 32 Command Steps")
		}
		return normalizeAutomationStepValue(input)
	case StepKindIf, StepKindChoose:
		if input.EntityID != "" || input.OperationName != "" || input.Parameters != nil {
			return Step{}, definitionIssue("/steps", "branch contains Command fields")
		}
	default:
		return Step{}, definitionIssue("/steps", "unknown Step kind")
	}
	if kind == StepKindIf {
		return walk.ifStep(input, depth)
	}
	return walk.chooseStep(input, depth)
}

func (walk *stepTreePreparation) optionalSequence(steps []Step, depth int) ([]Step, error) {
	if steps == nil {
		return nil, nil // Nil means the optional arm is omitted.
	}
	return walk.sequence(steps, depth)
}

func (walk *stepTreePreparation) ifStep(input Step, depth int) (Step, error) {
	if input.If == nil || input.Choose != nil {
		return Step{}, definitionIssue("/steps", "If family payload mismatch")
	}
	condition, err := walk.condition(input.If.Conditions, true)
	if err != nil {
		return Step{}, err
	}
	then, err := walk.sequence(input.If.Then, depth+1)
	if err != nil {
		return Step{}, err
	}
	otherwise, err := walk.optionalSequence(input.If.Else, depth+1)
	if err != nil {
		return Step{}, err
	}
	return Step{ID: input.ID, Kind: StepKindIf, If: &IfStep{Conditions: condition, Then: then, Else: otherwise}}, nil
}

func (walk *stepTreePreparation) chooseStep(input Step, depth int) (Step, error) {
	if input.Choose == nil || input.If != nil {
		return Step{}, definitionIssue("/steps", "Choose family payload mismatch")
	}
	if len(input.Choose.Branches) < 1 || len(input.Choose.Branches) > automationStepMaxCount {
		return Step{}, definitionIssue("/steps", "Choose needs 1 to 32 alternatives")
	}
	branches := make([]ChooseBranch, 0, len(input.Choose.Branches))
	seen := make(map[BranchID]bool)
	for _, branch := range input.Choose.Branches {
		if !subjectSlugPattern.MatchString(string(branch.ID)) || seen[branch.ID] {
			return Step{}, definitionIssue("/steps", "Choose branch IDs must be valid and unique within their Step")
		}
		seen[branch.ID] = true
		condition, err := walk.condition(branch.Conditions, true)
		if err != nil {
			return Step{}, err
		}
		children, err := walk.sequence(branch.Steps, depth+1)
		if err != nil {
			return Step{}, err
		}
		branches = append(branches, ChooseBranch{ID: branch.ID, Conditions: condition, Steps: children})
	}
	fallback, err := walk.optionalSequence(input.Choose.Default, depth+1)
	if err != nil {
		return Step{}, err
	}
	return Step{ID: input.ID, Kind: StepKindChoose, Choose: &ChooseStep{Branches: branches, Default: fallback}}, nil
}
