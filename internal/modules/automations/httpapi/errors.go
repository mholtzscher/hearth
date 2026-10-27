package httpapi

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
// admission.
type automationProblemError struct {
	Type       string `json:"type"                  format:"uri" default:"about:blank"`
	Title      string `json:"title"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
	Code       string `json:"code"`
	HistoryID  string `json:"history_id,omitempty"`
	HistoryURL string `json:"history_url,omitempty"`
}

func (problem *automationProblemError) Error() string { return problem.Detail }

func (problem *automationProblemError) GetStatus() int { return problem.Status }

func (*automationProblemError) ContentType(string) string {
	return problemContentType
}

func newProblem(status int, code, detail string) error {
	return &automationProblemError{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code,
	}
}

// problemResponse publishes the stable problem shape for one documented status.
func problemResponse(description string) *huma.Response {
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
		problemContentType: {Schema: problemSchema()},
	}}
}

//nolint:goconst // Problem fields and schema types stay readable together.
func problemSchema() *huma.Schema {
	return &huma.Schema{
		Type:     "object",
		Required: []string{"type", "title", "status", "detail", "code"},
		Properties: map[string]*huma.Schema{
			"type":   {Type: "string"},
			"title":  {Type: "string"},
			"status": {Type: "integer"},
			"detail": {Type: "string"},
			"code":   {Type: "string"},
		},
	}
}

// mapDomainError translates one module error into an HTTP problem without leaking internal text.
func mapDomainError(err error) error {
	if definition, ok := errors.AsType[*automations.DefinitionError](
		err,
	); ok && len(definition.Issues) > 0 &&
		definition.Issues[0].Path != "" {
		return newProblem(
			http.StatusBadRequest,
			"invalid_automation",
			"invalid automation request at "+definition.Issues[0].Path,
		)
	}
	if reference, ok := errors.AsType[*automations.FunctionReferenceError](err); ok {
		return huma.Error422UnprocessableEntity("invalid automation Function reference", &huma.ErrorDetail{
			Location: "body.steps", Message: "step " + string(reference.StepID) + ": " + reference.ReasonCode,
		})
	}
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
