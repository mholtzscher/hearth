package api

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// mcpFailureCode is the stable, machine-readable code an agent branches on when
// an Automation MCP Tool reports a missing parent.
//
// Huma collapses every Hearth not-found into one problem code, because an HTTP
// client already knows which resource the route addressed. MCP does not: one
// tool per resource means the code can name the missing resource, so an agent
// can branch on automation_not_found or automation_history_entry_not_found
// instead of parsing prose. Every other code and message crosses unchanged from
// the domain problem.
type mcpFailureCode string

const (
	// mcpFailureNone marks a call with no missing-parent outcome.
	mcpFailureNone mcpFailureCode = ""
	// mcpFailureAutomationNotFound is the missing-Automation outcome.
	mcpFailureAutomationNotFound mcpFailureCode = "automation_not_found"
	// mcpFailureHistoryEntryNotFound is the missing retained Run or Skip outcome.
	mcpFailureHistoryEntryNotFound mcpFailureCode = "automation_history_entry_not_found"
)

// automationProblemNotFoundCode is the one Huma problem code
// [mapDomainError] reports for a missing Automation or history entry; the MCP
// mapper replaces it with the specific code of the tool that failed.
const automationProblemNotFoundCode = "not_found"

// mcpAutomationFailure maps one failed Automation call to the tool failure an
// agent branches on. missing is the code to publish instead of the shared Huma
// not-found code, or [mcpFailureNone] when the call has no missing-parent
// outcome.
//
// The failure crosses as a [mcpapi.ToolError], so the SDK renders it as an
// isError tool result carrying "<code>: <message>" and structured content with
// the stable failure code, human-readable message, and optional details. Any
// error the module does not classify stays a generic internal failure whose
// cause is retained for the server-side diagnostic log, so no unpublished server
// detail reaches the client.
func mcpAutomationFailure(err error, missing mcpFailureCode) *mcpapi.ToolError {
	problem := automationProblem(err)
	if problem == nil {
		return mcpapi.InternalToolError(err)
	}
	code := problem.Code
	if code == automationProblemNotFoundCode && missing != mcpFailureNone {
		code = string(missing)
	}
	return &mcpapi.ToolError{
		Code:    code,
		Message: problem.Detail,
		Details: problemDetails(problem),
	}
}

// automationProblem returns the classified problem one failed call carries,
// either directly or through the module error mapping, or nil when the failure
// is unclassified.
func automationProblem(err error) *automationProblemError {
	var problem *automationProblemError
	if errors.As(err, &problem) {
		return problem
	}
	mapped := mapDomainError(err)
	if errors.As(mapped, &problem) {
		return problem
	}
	return nil
}

func problemDetails(problem *automationProblemError) map[string]any {
	if problem.HistoryID == "" && problem.HistoryURL == "" {
		return nil
	}
	details := make(map[string]any)
	if problem.HistoryID != "" {
		details["history_id"] = problem.HistoryID
	}
	if problem.HistoryURL != "" {
		details["history_url"] = problem.HistoryURL
	}
	return details
}
