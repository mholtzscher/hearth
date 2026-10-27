package automations

import (
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
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

// FunctionStateStatus labels the closed execution family without losing its evidence.
func FunctionStateStatus(state FunctionExecutionState) string {
	switch state.(type) {
	case FunctionRunning:
		return executionRunning
	case FunctionSucceeded:
		return executionSucceeded
	case FunctionFailed:
		return executionFailed
	case FunctionInterrupted:
		return executionInterrupted
	default:
		panic("invalid Function execution state")
	}
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
