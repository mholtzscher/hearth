//nolint:goconst // Branch error allowlists use the pure bridge's fixed diagnostic tokens verbatim.
package automations

import "time"

// BranchOutcome records selection or failure, never command completion.
type BranchOutcome string

const branchFailureStateCorrupt = "branch_state_corrupt"
const branchFailureConditionUnknown = "branch_condition_unknown"

const (
	BranchThen    BranchOutcome = "then"
	BranchElse    BranchOutcome = "else"
	BranchChosen  BranchOutcome = "branch"
	BranchDefault BranchOutcome = "default"
	BranchNoMatch BranchOutcome = "no_match"
	BranchUnknown BranchOutcome = "unknown"
	BranchError   BranchOutcome = "error"
)

// BranchDecision records one reached branch in reached-decision order.
type BranchDecision struct {
	Position    int
	StepID      StepID
	EvaluatedAt time.Time
	Body        BranchDecisionBody
}

// Kind derives the Step family from the supported decision body.
func (decision BranchDecision) Kind() StepKind {
	switch decision.Body.(type) {
	case IfDecision:
		return StepKindIf
	case ChooseDecision:
		return StepKindChoose
	default:
		return ""
	}
}

// Outcome derives the retained selection label.
func (decision BranchDecision) Outcome() BranchOutcome {
	switch body := decision.Body.(type) {
	case IfDecision:
		switch result := body.Result.(type) {
		case IfSelected:
			return BranchOutcome(result.Arm)
		case IfUnknown:
			return BranchUnknown
		case IfError:
			return BranchError
		default:
			return ""
		}
	case ChooseDecision:
		switch result := body.Result.(type) {
		case ChooseSelected:
			return BranchChosen
		case ChooseFallback:
			return BranchOutcome(result.Arm)
		case ChooseUnknown:
			return BranchUnknown
		case ChooseError:
			return BranchError
		default:
			return ""
		}
	default:
		return ""
	}
}

// FailureCode derives unknown failures and exposes operational error codes.
func (decision BranchDecision) FailureCode() *string {
	var code string
	switch body := decision.Body.(type) {
	case IfDecision:
		switch result := body.Result.(type) {
		case IfSelected:
			return nil
		case IfUnknown:
			code = branchFailureConditionUnknown
		case IfError:
			code = result.FailureCode
		default:
			return nil
		}
	case ChooseDecision:
		switch result := body.Result.(type) {
		case ChooseSelected, ChooseFallback:
			return nil
		case ChooseUnknown:
			code = branchFailureConditionUnknown
		case ChooseError:
			code = result.FailureCode
		default:
			return nil
		}
	default:
		return nil
	}
	return &code
}

// Validate checks branch execution shape; snapshot-relative selection checks remain separate.
func (details BranchExecutionDetails) Validate() error {
	if err := validateBranchDecisionShape(details.Decision); err != nil {
		return err
	}
	code := details.Decision.FailureCode()
	valueError := code != nil && isBranchValueFailureCode(*code)
	if valueError != (details.ValueFailure != nil) {
		return invalid("branch value failure presence disagrees with decision")
	}
	if details.ValueFailure != nil {
		if err := validateDataFailure(*details.ValueFailure); err != nil {
			return err
		}
		code := details.Decision.FailureCode()
		if code == nil || *code != details.ValueFailure.FailureCode {
			return invalid("branch value failure disagrees with decision")
		}
		switch b := details.Decision.Body.(type) {
		case IfDecision:
			if _, ok := b.Result.(IfError); !ok {
				return invalid("branch value failure requires error")
			}
		case ChooseDecision:
			if _, ok := b.Result.(ChooseError); !ok {
				return invalid("branch value failure requires error")
			}
		default:
			return invalid("invalid branch")
		}
	}
	if !isValidExecutionTimestamp(details.Decision.EvaluatedAt) {
		return invalid("invalid execution start or kind")
	}
	// Branch times are also embedded in JSON evidence. Preserve the codec's
	// representability checks when validating without serializing that evidence.
	if !jsonEvidenceTime(details.Decision.EvaluatedAt) {
		return invalid("branch evidence time cannot be encoded")
	}
	switch body := details.Decision.Body.(type) {
	case IfDecision:
		switch result := body.Result.(type) {
		case IfSelected:
			return validateBranchObservationTimes(result.Evaluation)
		case IfUnknown:
			return validateBranchObservationTimes(result.Evaluation)
		case IfError:
			return nil
		}
	case ChooseDecision:
		var evaluations []ChooseEvaluation
		switch result := body.Result.(type) {
		case ChooseSelected:
			evaluations = result.Evaluations
		case ChooseFallback:
			evaluations = result.Evaluations
		case ChooseUnknown:
			evaluations = result.Evaluations
		case ChooseError:
			evaluations = result.Evaluations
		}
		for _, evaluation := range evaluations {
			if err := validateBranchObservationTimes(evaluation.Evaluation); err != nil {
				return err
			}
		}
	}
	return nil
}

// Kind identifies the reached If or Choose family.
func (details BranchExecutionDetails) Kind() StepKind { return details.Decision.Kind() }

// Phase reports selection success or branch failure, not child completion.
func (details BranchExecutionDetails) Phase() string {
	if details.Decision.FailureCode() != nil {
		return executionFailed
	}
	return executionSucceeded
}

// StartTime returns when this branch was evaluated.
func (details BranchExecutionDetails) StartTime() time.Time { return details.Decision.EvaluatedAt }

// CompletionTime equals the evaluation time because branch selection is instantaneous.
func (details BranchExecutionDetails) CompletionTime() *time.Time {
	return &details.Decision.EvaluatedAt
}

// RunFailure derives unknown or operational branch failure without inventing child outcomes.
func (details BranchExecutionDetails) RunFailure() (RunOutcome, bool) {
	if code := details.Decision.FailureCode(); code != nil {
		return FailedRun{FailureCode: *code}, true
	}
	return nil, false
}

func validateBranchObservationTimes(evaluation ConditionEvaluation) error {
	for _, node := range evaluation.Nodes {
		switch evidence := node.Evidence.(type) {
		case KnownStateEvidence:
			if !jsonEvidenceTime(evidence.Observation.ObservedAt) {
				return invalid("branch Observation time cannot be encoded")
			}
		case UnknownStateEvidence:
			if evidence.Observation != nil && !jsonEvidenceTime(evidence.Observation.ObservedAt) {
				return invalid("branch Observation time cannot be encoded")
			}
		case TriggerMatchEvidence:
		}
	}
	return nil
}

func jsonEvidenceTime(at time.Time) bool {
	year := at.UTC().Year()
	return year >= 0 && year <= 9999
}

func validateBranchDecisionShape(decision BranchDecision) error {
	if decision.Position < 0 || decision.Position >= automationAllStepsMaxCount ||
		!subjectSlugPattern.MatchString(string(decision.StepID)) || decision.EvaluatedAt.IsZero() {
		return invalid("branch decision has invalid position, Step identity, or time")
	}
	switch body := decision.Body.(type) {
	case IfDecision:
		return validateIfResultShape(body.Result, decision.EvaluatedAt)
	case ChooseDecision:
		return validateChooseResultShape(body.Result, decision.EvaluatedAt)
	default:
		return invalid("unsupported branch decision representation")
	}
}

func validateIfResultShape(result IfDecisionResult, at time.Time) error {
	switch value := result.(type) {
	case IfSelected:
		want := ConditionFalse
		switch value.Arm {
		case IfThen:
			want = ConditionTrue
		case IfElse, IfNoMatch:
		default:
			return invalid("invalid If arm")
		}
		return validateTimedRoot(value.Evaluation, at, want)
	case IfUnknown:
		return validateTimedRoot(value.Evaluation, at, ConditionUnknown)
	case IfError:
		return validateBranchErrorCode(value.FailureCode, 0)
	default:
		return invalid("unsupported If result representation")
	}
}

func validateChooseResultShape(result ChooseDecisionResult, at time.Time) error {
	var evaluations []ChooseEvaluation
	want := ConditionFalse
	switch value := result.(type) {
	case ChooseSelected:
		evaluations, want = value.Evaluations, ConditionTrue
		if len(evaluations) == 0 || value.BranchID != evaluations[len(evaluations)-1].BranchID {
			return invalid("Choose selection must identify the final true root")
		}
	case ChooseFallback:
		evaluations = value.Evaluations
		if value.Arm != ChooseDefault && value.Arm != ChooseNoMatch {
			return invalid("invalid Choose fallback arm")
		}
	case ChooseUnknown:
		evaluations, want = value.Evaluations, ConditionUnknown
	case ChooseError:
		evaluations = value.Evaluations
		if err := validateBranchErrorCode(value.FailureCode, len(evaluations)); err != nil {
			return err
		}
	default:
		return invalid("unsupported Choose result representation")
	}
	if _, failing := result.(ChooseError); !failing && len(evaluations) == 0 {
		return invalid("Choose selection requires evidence")
	}
	return validateChoosePrefix(evaluations, at, want)
}

func validateChoosePrefix(evaluations []ChooseEvaluation, at time.Time, want ConditionResult) error {
	if len(evaluations) > automationStepMaxCount {
		return invalid("Choose prefix exceeds alternative bound")
	}
	seen := make(map[BranchID]bool)
	for index, item := range evaluations {
		if !subjectSlugPattern.MatchString(string(item.BranchID)) || seen[item.BranchID] {
			return invalid("Choose evaluations require unique valid branch IDs")
		}
		seen[item.BranchID] = true
		rootWant := ConditionFalse
		if index == len(evaluations)-1 {
			rootWant = want
		}
		if err := validateTimedRoot(item.Evaluation, at, rootWant); err != nil {
			return err
		}
	}
	return nil
}

func validateBranchErrorCode(code string, prefix int) error {
	switch code {
	case "branch_state_read_failed", "branch_snapshot_incomplete":
		if prefix != 0 {
			return invalid("branch read error cannot retain a completed prefix")
		}
	case branchFailureStateCorrupt:
	case "expression_missing",
		"expression_type",
		"expression_evaluation",
		"expression_value_invalid",
		"expression_limit",
		"resolved_input_invalid",
		"run_data_limit":
	default:
		return invalid("invalid branch error code")
	}
	return nil
}

func isBranchValueFailureCode(code string) bool {
	switch code {
	case "expression_missing", "expression_type", "expression_evaluation", "expression_value_invalid",
		"expression_limit", "resolved_input_invalid", "run_data_limit":
		return true
	default:
		return false
	}
}

func validateTimedRoot(evaluation ConditionEvaluation, at time.Time, want ConditionResult) error {
	if !evaluation.EvaluatedAt.Equal(at) || evaluation.Result != want {
		return invalid("branch root result or time disagrees with outcome")
	}
	return validateConditionEvaluationShape(evaluation)
}
