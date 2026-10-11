package automations

// BranchDecisionBody identifies the reached branch family and its result.
//
//sumtype:decl
type BranchDecisionBody interface{ isBranchDecisionBody() }

// IfDecision records the result of one reached If.
type IfDecision struct{ Result IfDecisionResult }

// ChooseDecision records the result of one reached Choose.
type ChooseDecision struct{ Result ChooseDecisionResult }

func (IfDecision) isBranchDecisionBody()     {}
func (ChooseDecision) isBranchDecisionBody() {}

// IfDecisionResult distinguishes selection, unknown evidence, and read errors.
//
//sumtype:decl
type IfDecisionResult interface{ isIfDecisionResult() }

// ChooseDecisionResult carries the evaluated alternative prefix for a result.
//
//sumtype:decl
type ChooseDecisionResult interface{ isChooseDecisionResult() }

// IfArm identifies the selected Then, Else, or absent Else arm.
type IfArm string

const (
	// IfThen selects the Then sequence.
	IfThen IfArm = "then"
	// IfElse selects the Else sequence.
	IfElse IfArm = "else"
	// IfNoMatch records a false If with no Else sequence.
	IfNoMatch IfArm = "no_match"
)

// ChooseFallbackArm identifies the Default or its absence.
type ChooseFallbackArm string

const (
	// ChooseDefault selects the Default sequence.
	ChooseDefault ChooseFallbackArm = "default"
	// ChooseNoMatch records false alternatives without a Default sequence.
	ChooseNoMatch ChooseFallbackArm = "no_match"
)

// IfSelected carries the one completed root and selected arm.
type IfSelected struct {
	Arm        IfArm
	Evaluation ConditionEvaluation
}

// IfUnknown fails the Run with the one unknown root's evidence.
type IfUnknown struct{ Evaluation ConditionEvaluation }

// IfError fails the Run before a complete root evaluation is available.
type IfError struct{ FailureCode string }

func (IfSelected) isIfDecisionResult() {}
func (IfUnknown) isIfDecisionResult()  {}
func (IfError) isIfDecisionResult()    {}

// ChooseEvaluation identifies one completed alternative evaluation.
type ChooseEvaluation struct {
	BranchID   BranchID
	Evaluation ConditionEvaluation
}

// ChooseSelected identifies the first true alternative and evaluated prefix.
type ChooseSelected struct {
	BranchID    BranchID
	Evaluations []ChooseEvaluation
}

// ChooseFallback follows a complete false alternative sequence.
type ChooseFallback struct {
	Arm         ChooseFallbackArm
	Evaluations []ChooseEvaluation
}

// ChooseUnknown fails at the first unknown alternative.
type ChooseUnknown struct{ Evaluations []ChooseEvaluation }

// ChooseError retains the false prefix completed before an evaluation error.
type ChooseError struct {
	FailureCode string
	Evaluations []ChooseEvaluation
}

func (ChooseSelected) isChooseDecisionResult() {}
func (ChooseFallback) isChooseDecisionResult() {}
func (ChooseUnknown) isChooseDecisionResult()  {}
func (ChooseError) isChooseDecisionResult()    {}
