package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Automation problem documents and request bodies share two content types across
// this package's operations.
const (
	problemContentType = "application/problem+json"
	jsonContentType    = "application/json"
)

// automationProblemError is one RFC 9457 problem with a stable machine-readable
// code. HistoryID and HistoryURL are set only for a Condition-blocked manual
// admission and always reference the already-committed Skip. The document never
// contains internal database or upstream error text, definition trees, operands,
// or selected State values.
type automationProblemError struct {
	Type       string `json:"type"                  format:"uri" default:"about:blank"`
	Title      string `json:"title"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
	Code       string `json:"code"`
	HistoryID  string `json:"history_id,omitempty"`
	HistoryURL string `json:"history_url,omitempty"`
}

func (problem *automationProblemError) Error() string  { return problem.Detail }
func (problem *automationProblemError) GetStatus() int { return problem.Status }
func (*automationProblemError) ContentType(string) string {
	return problemContentType
}

func newProblem(status int, code, detail string) error {
	return &automationProblemError{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code,
	}
}

// conditionBlockedProblemCode maps one committed manual Condition Skip reason to
// its stable problem code. Busy and stale reasons never reach a blocked manual
// admission, so they fall back to the generic class code.
func conditionBlockedProblemCode(reason automations.SkipReason) string {
	switch reason {
	case automations.SkipConditionsFalse:
		return "conditions_false"
	case automations.SkipConditionsUnknown:
		return "conditions_unknown"
	case automations.SkipBusy, automations.SkipStaleFact:
		return "conditions_blocked"
	}
	return "conditions_blocked"
}

// newConditionBlockedProblem maps a committed manual Condition Skip to its 409
// problem with the history reference callers fetch to read the retained Skip.
func newConditionBlockedProblem(blocked *automations.ConditionsBlockedError) error {
	return &automationProblemError{
		Type:       "about:blank",
		Title:      http.StatusText(http.StatusConflict),
		Status:     http.StatusConflict,
		Detail:     "automation conditions prevented manual admission",
		Code:       conditionBlockedProblemCode(blocked.Reason),
		HistoryID:  string(blocked.SkipID),
		HistoryURL: "/v1/automations/" + string(blocked.AutomationID) + "/history/" + string(blocked.SkipID),
	}
}

// problemResponse publishes the stable problem shape for one documented status.
func problemResponse(description string) *huma.Response {
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
		problemContentType: {Schema: problemSchema(false)},
	}}
}

// conditionBlockedProblemResponse publishes the 409 problem shape with the
// optional committed-Skip history reference fields.
func conditionBlockedProblemResponse(description string) *huma.Response {
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
		problemContentType: {Schema: problemSchema(true)},
	}}
}

// problemSchema builds the closed problem document schema. History references
// are published only where a committed Condition Skip can produce them.
func problemSchema(withHistoryReferences bool) *huma.Schema {
	const stringType = "string"
	properties := map[string]*huma.Schema{
		"type":   {Type: stringType},
		"title":  {Type: stringType},
		"status": {Type: "integer"},
		"detail": {Type: stringType},
		"code":   {Type: stringType},
	}
	if withHistoryReferences {
		properties["history_id"] = &huma.Schema{Type: stringType}
		properties["history_url"] = &huma.Schema{Type: stringType}
	}
	return &huma.Schema{
		Type:       "object",
		Required:   []string{"type", "title", "status", "detail", "code"},
		Properties: properties,
	}
}

// mapDomainError translates one module error into an HTTP problem without
// leaking internal text. Definition schema issues are already safe and
// payload-free.
func mapDomainError(err error) error {
	switch {
	case errors.Is(err, automations.ErrAutomationNotFound), errors.Is(err, automations.ErrHistoryNotFound):
		return newProblem(http.StatusNotFound, "not_found", "automation resource not found")
	case errors.Is(err, automations.ErrRevisionConflict):
		return newProblem(http.StatusConflict, "revision_conflict", "automation revision conflict")
	case errors.Is(err, automations.ErrAutomationBusy):
		return newProblem(http.StatusConflict, "automation_busy", "automation already has a running run")
	case errors.Is(err, automations.ErrAutomationConditionsBlocked):
		if blocked, ok := errors.AsType[*automations.ConditionsBlockedError](err); ok {
			return newConditionBlockedProblem(blocked)
		}
		return newProblem(http.StatusConflict, "conditions_blocked", "automation conditions prevented manual admission")
	case errors.Is(err, automations.ErrConditionSnapshotRequired):
		return newProblem(
			http.StatusServiceUnavailable,
			"condition_snapshot_unavailable",
			"automation condition state is unavailable",
		)
	case errors.Is(err, automations.ErrAdmissionUnavailable):
		return newProblem(
			http.StatusServiceUnavailable, "admission_unavailable", "automation admission is unavailable",
		)
	case errors.Is(err, automations.ErrInvalidAutomation):
		return newProblem(http.StatusBadRequest, "invalid_automation", "invalid automation request")
	default:
		return huma.Error500InternalServerError("internal error")
	}
}
