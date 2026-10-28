package automations

import (
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

const functionOutcomeNotExecuted = "not_executed"

//sumtype:decl
type FunctionExecutionState interface{ functionExecutionState() }

//sumtype:decl
type FunctionTerminalState interface {
	FunctionExecutionState
	functionTerminalState()
}
type FunctionRunning struct{}
type FunctionSucceeded struct {
	CompletedAt     time.Time
	CatalogRevision adapters.CatalogID
}
type FunctionFailed struct {
	CompletedAt time.Time
	Evidence    FunctionFailure
}
type FunctionInterrupted struct {
	CompletedAt time.Time
	ReasonCode  string
}

func (FunctionRunning) functionExecutionState()     {}
func (FunctionSucceeded) functionExecutionState()   {}
func (FunctionFailed) functionExecutionState()      {}
func (FunctionInterrupted) functionExecutionState() {}
func (FunctionSucceeded) functionTerminalState()    {}
func (FunctionFailed) functionTerminalState()       {}
func (FunctionInterrupted) functionTerminalState()  {}

//sumtype:decl
type FunctionFailure interface{ functionFailure() }
type FunctionAdmissionRejected struct{ ReasonCode string }
type FunctionInvocationFailed struct {
	CatalogRevision adapters.CatalogID
	Outcome         adapters.Evidence
}

func (FunctionAdmissionRejected) functionFailure() {}
func (FunctionInvocationFailed) functionFailure()  {}

var functionCatalogPattern = regexp.MustCompile(
	`^afc_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)
var functionReasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

// Validate checks intrinsic Function execution evidence and its reserved Invocation identity.
func (details FunctionExecutionDetails) Validate() error {
	if details.Arguments != nil {
		if err := validateResolvedObject(details.Arguments); err != nil {
			return err
		}
	}
	if len(details.Output) > 0 {
		if _, err := exprvalue.NormalizeEvidence(details.Output); err != nil {
			return invalid("invalid Function output")
		}
	}
	if len(details.Output) > 0 || details.ValueFailure != nil {
		if _, ok := details.State.(FunctionSucceeded); !ok || len(details.Output) > 0 && details.ValueFailure != nil {
			return invalid("invalid Function value outcome")
		}
	}
	if details.ValueFailure != nil && (details.ValueFailure.Field != "" || details.ValueFailure.ConditionID != "") {
		return invalid("post-success Function failure must identify Step")
	}
	if details.ValueFailure != nil {
		if err := validateDataFailure(*details.ValueFailure); err != nil {
			return err
		}
		switch details.ValueFailure.FailureCode {
		case failureExpressionValueInvalid, failureExpressionLimit, failureRunDataLimit:
		default:
			return invalid("invalid post-success Function failure")
		}
	}
	if _, err := adapters.ParseInvocationID(string(details.InvocationID)); err != nil {
		return invalid("invalid Function invocation identity")
	}
	if err := ValidateFunctionExecutionState(details.State); err != nil {
		return err
	}
	if !isValidExecutionTimestamp(details.StartedAt) {
		return invalid("invalid execution start or kind")
	}
	return nil
}

// Kind identifies a Function execution.
func (FunctionExecutionDetails) Kind() StepKind { return StepKindFunction }

// Phase derives the lifecycle label from a validated Function state.
func (details FunctionExecutionDetails) Phase() string {
	switch details.State.(type) {
	case FunctionRunning:
		return executionRunning
	case FunctionSucceeded:
		if details.ValueFailure != nil {
			return executionFailed
		}
		return executionSucceeded
	case FunctionFailed:
		return executionFailed
	case FunctionInterrupted:
		return executionInterrupted
	default:
		panic("invalid Function execution state")
	}
}

// StartTime returns the recorded Function reach time.
func (details FunctionExecutionDetails) StartTime() time.Time { return details.StartedAt }

// CompletionTime projects a validated Function state; running has no completion.
func (details FunctionExecutionDetails) CompletionTime() *time.Time {
	return FunctionTerminalTime(details.State)
}

// RunFailure preserves the Invocation or admission reason on the parent Run.
func (details FunctionExecutionDetails) RunFailure() (RunOutcome, bool) {
	switch state := details.State.(type) {
	case FunctionFailed:
		return FailedRun{FailureCode: functionFailureCode(state.Evidence)}, true
	case FunctionInterrupted:
		return InterruptedRun{FailureCode: state.ReasonCode}, true
	case FunctionSucceeded:
		if details.ValueFailure != nil {
			return FailedRun{FailureCode: details.ValueFailure.FailureCode}, true
		}
		return nil, false
	case FunctionRunning:
		return nil, false
	default:
		return nil, false
	}
}

func functionFailureCode(failure FunctionFailure) string {
	switch failure := failure.(type) {
	case FunctionAdmissionRejected:
		return failure.ReasonCode
	case FunctionInvocationFailed:
		switch evidence := failure.Outcome.(type) {
		case adapters.Failed:
			return evidence.ReasonCode
		case adapters.NotExecuted:
			return evidence.ReasonCode
		case adapters.Unknown:
			return evidence.ReasonCode
		case adapters.Succeeded:
			return FailureInternalError
		default:
			return FailureInternalError
		}
	default:
		return FailureInternalError
	}
}

func ValidateFunctionExecutionState(state FunctionExecutionState) error {
	if state == nil {
		return invalid("unsupported Function execution state")
	}
	switch terminal := state.(type) {
	case FunctionRunning:
		return nil
	case FunctionSucceeded, FunctionFailed, FunctionInterrupted:
		return validateFunctionTerminalState(terminal)
	default:
		return invalid("unsupported Function execution state")
	}
}

func FunctionStateCompletion(state FunctionExecutionState) (*time.Time, error) {
	if err := ValidateFunctionExecutionState(state); err != nil {
		return nil, err
	}
	if at := FunctionTerminalTime(state); at != nil {
		return at, nil
	}
	return nil, nil //nolint:nilnil // Running executions have no completion timestamp.
}

func validateFunctionTerminalState(terminal FunctionExecutionState) error {
	switch state := terminal.(type) {
	case FunctionSucceeded:
		if adapters.ValidFunctionTime(state.CompletedAt) &&
			functionCatalogPattern.MatchString(string(state.CatalogRevision)) {
			return nil
		}
	case FunctionFailed:
		if !adapters.ValidFunctionTime(state.CompletedAt) {
			return invalid("invalid Function completion time")
		}
		return validateFunctionFailure(state.Evidence)
	case FunctionInterrupted:
		if adapters.ValidFunctionTime(state.CompletedAt) && (state.ReasonCode == FailureCoreStopping ||
			state.ReasonCode == FailureCoreRestarted || state.ReasonCode == FailureExecutorFault) {
			return nil
		}
	case FunctionRunning:
		return invalid("Function completion is not terminal")
	default:
		return invalid("unsupported Function completion state")
	}
	return invalid("invalid Function completion")
}

func validateFunctionFailure(failure FunctionFailure) error {
	switch evidence := failure.(type) {
	case FunctionAdmissionRejected:
		switch evidence.ReasonCode {
		case "adapter_not_found", "function_not_found", "function_contract_changed", "catalog_changed",
			"adapter_unavailable", "invalid_function_arguments", "function_admission_closed":
			return nil
		}
	case FunctionInvocationFailed:
		if !functionCatalogPattern.MatchString(string(evidence.CatalogRevision)) {
			return invalid("invalid Function catalog revision")
		}
		var reason string
		switch outcome := evidence.Outcome.(type) {
		case adapters.Failed:
			reason = outcome.ReasonCode
		case adapters.NotExecuted:
			reason = outcome.ReasonCode
		case adapters.Unknown:
			reason = outcome.ReasonCode
		case adapters.Succeeded:
			return invalid("success is not Function failure evidence")
		default:
			return invalid("invalid Function invocation outcome")
		}
		if functionReasonPattern.MatchString(reason) {
			return nil
		}
	default:
		return invalid("unsupported Function failure evidence")
	}
	return invalid("invalid Function failure evidence")
}

// FunctionTerminalTime returns the terminal evidence time; running has no completion.
func FunctionTerminalTime(state FunctionExecutionState) *time.Time {
	switch state := state.(type) {
	case FunctionRunning:
		return nil
	case FunctionSucceeded:
		return &state.CompletedAt
	case FunctionFailed:
		return &state.CompletedAt
	case FunctionInterrupted:
		return &state.CompletedAt
	default:
		panic("invalid Function execution state")
	}
}
