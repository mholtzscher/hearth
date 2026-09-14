package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// automationProblemError is one RFC 9457 problem with a stable machine-readable code.
// It never contains internal database or upstream error text.
type automationProblemError struct {
	Type   string `json:"type"   format:"uri" default:"about:blank"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

func (problem *automationProblemError) Error() string     { return problem.Detail }
func (problem *automationProblemError) GetStatus() int    { return problem.Status }
func (*automationProblemError) ContentType(string) string { return "application/problem+json" }

func newProblem(status int, code, detail string) error {
	return &automationProblemError{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code,
	}
}

// problemResponse publishes the stable problem shape for one documented status.
func problemResponse(description string) *huma.Response {
	const stringType = "string"
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
		"application/problem+json": {
			Schema: &huma.Schema{
				Type:     "object",
				Required: []string{"type", "title", "status", "detail", "code"},
				Properties: map[string]*huma.Schema{
					"type":   {Type: stringType},
					"title":  {Type: stringType},
					"status": {Type: "integer"},
					"detail": {Type: stringType},
					"code":   {Type: stringType},
				},
			},
		},
	}}
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
