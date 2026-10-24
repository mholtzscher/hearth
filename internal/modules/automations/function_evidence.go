package automations

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type FunctionStart struct {
	RunID        RunID
	StepID       StepID
	Position     int
	InvocationID adapters.InvocationID
	StartedAt    time.Time
}

//sumtype:decl
type FunctionExecutionState interface{ functionExecutionState() }
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

//sumtype:decl
type FunctionFailure interface{ functionFailure() }
type FunctionAdmissionRejected struct{ ReasonCode string }
type FunctionInvocationFailed struct {
	CatalogRevision adapters.CatalogID
	Outcome         adapters.Evidence
}

func (FunctionAdmissionRejected) functionFailure() {}
func (FunctionInvocationFailed) functionFailure()  {}

type FunctionExecution struct {
	FunctionStart

	State FunctionExecutionState
}
type FunctionCompletion struct {
	RunID        RunID
	StepID       StepID
	InvocationID adapters.InvocationID
	State        FunctionExecutionState
}

var functionCatalogPattern = regexp.MustCompile(
	`^afc_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)
var functionReasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

func ValidateFunctionStart(start FunctionStart) error {
	if _, err := ParseRunID(string(start.RunID)); err != nil {
		return err
	}
	if _, err := ParseStepID(string(start.StepID)); err != nil {
		return err
	}
	if _, err := adapters.ParseInvocationID(string(start.InvocationID)); err != nil {
		return invalid("invalid Function invocation identity")
	}
	if start.Position < 0 || start.Position >= automationStepMaxCount || !adapters.ValidFunctionTime(start.StartedAt) {
		return invalid("invalid Function execution position or time")
	}
	return nil
}

func ValidateFunctionCompletion(completion FunctionCompletion) error {
	if _, err := ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if _, err := ParseStepID(string(completion.StepID)); err != nil {
		return err
	}
	if _, err := adapters.ParseInvocationID(string(completion.InvocationID)); err != nil {
		return invalid("invalid Function invocation identity")
	}
	switch state := completion.State.(type) {
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
		return "running"
	case FunctionSucceeded:
		return "succeeded"
	case FunctionFailed:
		return "failed"
	case FunctionInterrupted:
		return "interrupted"
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

// EncodeFunctionExecution projects canonical evidence without copying Function results.
func EncodeFunctionExecution(execution FunctionExecution) (json.RawMessage, error) {
	if err := ValidateFunctionStart(execution.FunctionStart); err != nil {
		return nil, err
	}
	if _, running := execution.State.(FunctionRunning); !running {
		if err := ValidateFunctionCompletion(FunctionCompletion{RunID: execution.RunID, StepID: execution.StepID,
			InvocationID: execution.InvocationID, State: execution.State}); err != nil {
			return nil, err
		}
	}
	value := functionExecutionJSON{
		StepID:       execution.StepID,
		Position:     execution.Position,
		InvocationID: execution.InvocationID,
		Status:       FunctionStateStatus(execution.State),
		StartedAt:    execution.StartedAt,
		CompletedAt:  FunctionTerminalTime(execution.State),
	}
	switch state := execution.State.(type) {
	case FunctionRunning:
	case FunctionSucceeded:
		value.CatalogRevision = state.CatalogRevision
		value.InvocationOutcome = "succeeded"
	case FunctionFailed:
		switch evidence := state.Evidence.(type) {
		case FunctionAdmissionRejected:
			value.ReasonCode = evidence.ReasonCode
		case FunctionInvocationFailed:
			value.CatalogRevision = evidence.CatalogRevision
			switch outcome := evidence.Outcome.(type) {
			case adapters.Failed:
				value.InvocationOutcome, value.ReasonCode = "failed", outcome.ReasonCode
			case adapters.NotExecuted:
				value.InvocationOutcome, value.ReasonCode = "not_executed", outcome.ReasonCode
			case adapters.Unknown:
				value.InvocationOutcome, value.ReasonCode = "unknown", outcome.ReasonCode
			case adapters.Succeeded:
				return nil, invalid("success is not Function failure evidence")
			default:
				return nil, invalid("invalid Function invocation outcome")
			}
		default:
			return nil, invalid("invalid Function failure evidence")
		}
	case FunctionInterrupted:
		value.ReasonCode = state.ReasonCode
	default:
		return nil, invalid("invalid Function execution state")
	}
	return json.Marshal(value)
}

type functionExecutionJSON struct {
	StepID            StepID                `json:"step_id"`
	Position          int                   `json:"position"`
	InvocationID      adapters.InvocationID `json:"invocation_id"`
	Status            string                `json:"status"`
	StartedAt         time.Time             `json:"started_at"`
	CompletedAt       *time.Time            `json:"completed_at,omitempty"`
	CatalogRevision   adapters.CatalogID    `json:"catalog_revision,omitempty"`
	InvocationOutcome string                `json:"invocation_outcome,omitempty"`
	ReasonCode        string                `json:"reason_code,omitempty"`
}
