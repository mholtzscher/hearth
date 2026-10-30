package automations

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// conditionPreparationError retains the deepest validated Condition identity.
// Path composition stays with bindingError, independent of runtime targets.
type conditionPreparationError struct {
	id    ConditionID
	cause error
}

func (*conditionPreparationError) Error() string         { return "invalid Condition definition" }
func (failure *conditionPreparationError) Unwrap() error { return failure.cause }

func definitionPreparationIssue(path string, stepID StepID, err error) error {
	code := dataFailureCode(err)
	issue := DefinitionIssue{Path: path, StepID: stepID, Code: code, Message: code}
	if binding, ok := errors.AsType[*bindingError](err); ok && binding.path != "" {
		issue.Path += "/" + binding.path
	}
	if condition, ok := errors.AsType[*conditionPreparationError](err); ok {
		issue.ConditionID = condition.id
		issue.Code, issue.Message = "invalid_condition", "invalid Condition definition"
	}
	if failure, ok := errors.AsType[*exprvalue.Failure](err); ok {
		issue.Code, issue.Message = failure.Code, failure.Code
		if failure.Located {
			issue.Span = &DefinitionSourceSpan{Start: failure.Start, End: failure.End}
		}
	}
	return &DefinitionError{Issues: []DefinitionIssue{issue}}
}
