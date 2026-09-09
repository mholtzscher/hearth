package automations

import "errors"

var (
	ErrInvalidAutomation            = errors.New("invalid automation")
	ErrAutomationNotFound           = errors.New("automation not found")
	ErrAutomationRunNotFound        = errors.New("automation run not found")
	ErrAutomationRevisionConflict   = errors.New("automation revision conflict")
	ErrAutomationRunActive          = errors.New("automation run active")
	ErrAutomationUnavailable        = errors.New("automation unavailable")
	ErrAutomationTransitionConflict = errors.New("automation transition conflict")
)

const (
	AutomationTriggerKindCron                               = "cron"
	AutomationRunSourceManual          AutomationRunSource  = "manual"
	AutomationRunSourceScheduled       AutomationRunSource  = "scheduled"
	AutomationRunStatusRunning         AutomationRunStatus  = "running"
	AutomationRunStatusSucceeded       AutomationRunStatus  = "succeeded"
	AutomationRunStatusFailed          AutomationRunStatus  = "failed"
	AutomationRunStatusInterrupted     AutomationRunStatus  = "interrupted"
	AutomationStepStatusPending        AutomationStepStatus = "pending"
	AutomationStepStatusRunning        AutomationStepStatus = "running"
	AutomationStepStatusSatisfied      AutomationStepStatus = "satisfied"
	AutomationStepStatusDispatched     AutomationStepStatus = "dispatched"
	AutomationStepStatusFailed         AutomationStepStatus = "failed"
	AutomationStepStatusNotAttempted   AutomationStepStatus = "not_attempted"
	AutomationStepStatusInterrupted    AutomationStepStatus = "interrupted"
	AutomationFailureCommandIDConflict                      = "command_id_conflict"
	AutomationFailureInvalidCommand                         = "invalid_command"
	AutomationFailureEntityNotFound                         = "entity_not_found"
	AutomationFailureInternalError                          = "internal_error"
	AutomationFailureCoreRestarted                          = "core_restarted"
	AutomationFailureCoreStopping                           = "core_stopping"
)

// AutomationPrecreationFailureCode reports whether code is a confirmed pre-creation
// failure code. Use it only when persisting AutomationStepCompletion.PrecreationFailure,
// never for ownership reads: owned terminal commands may copy internal_error while
// staying visible. Ambiguous executor faults must never persist these codes.
func AutomationPrecreationFailureCode(code string) bool {
	switch code {
	case AutomationFailureCommandIDConflict,
		AutomationFailureInvalidCommand,
		AutomationFailureEntityNotFound,
		AutomationFailureInternalError:
		return true
	default:
		return false
	}
}
