package mcpapi

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type failureCode string

const (
	failureNone                 failureCode = ""
	failureAutomationNotFound   failureCode = "automation_not_found"
	failureHistoryEntryNotFound failureCode = "automation_history_entry_not_found"
)

// automationFailure classifies domain outcomes without reconstructing HTTP errors.
func automationFailure(err error, missing failureCode) *mcptools.ToolError {
	if modeled, ok := errors.AsType[*mcptools.ToolError](err); ok {
		return modeled
	}
	var code, message string
	switch {
	case errors.Is(err, automations.ErrAutomationNotFound), errors.Is(err, automations.ErrHistoryNotFound):
		code, message = string(missing), "automation resource not found"
		if code == "" {
			code = "not_found"
		}
	case errors.Is(err, automations.ErrRevisionConflict):
		code, message = "revision_conflict", "automation revision conflict"
	case errors.Is(err, automations.ErrAutomationBusy):
		code, message = "automation_busy", "automation already has a running run"
	case errors.Is(err, automations.ErrAutomationConditionsBlocked):
		code, message = "conditions_blocked", "automation conditions prevented manual admission"
		if blocked, ok := errors.AsType[*automations.ConditionsBlockedError](err); ok {
			switch blocked.Reason {
			case automations.SkipConditionsFalse:
				code = "conditions_false"
			case automations.SkipConditionsUnknown:
				code = "conditions_unknown"
			case automations.SkipBusy, automations.SkipStaleFact:
			}
			return &mcptools.ToolError{Code: code, Message: message, Details: map[string]any{
				"history_id":  string(blocked.SkipID),
				"history_url": "/v1/automations/" + string(blocked.AutomationID) + "/history/" + string(blocked.SkipID),
			}}
		}
	case errors.Is(err, automations.ErrConditionSnapshotRequired):
		code, message = "condition_snapshot_unavailable", "automation condition state is unavailable"
	case errors.Is(err, automations.ErrAdmissionUnavailable):
		code, message = "admission_unavailable", "automation admission is unavailable"
	case errors.Is(err, automations.ErrInvalidAutomation):
		code, message = "invalid_automation", "invalid automation request"
	default:
		return mcptools.InternalToolError(err)
	}
	return &mcptools.ToolError{Code: code, Message: message}
}
