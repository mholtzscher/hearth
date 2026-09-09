package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type automationProblemError struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

func (problem *automationProblemError) Error() string     { return problem.Detail }
func (problem *automationProblemError) GetStatus() int    { return problem.Status }
func (*automationProblemError) ContentType(string) string { return "application/problem+json" }
func automationCodedProblem(status int, code, detail string) error {
	return &automationProblemError{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: detail,
		Code:   code,
	}
}
func automationProblemResponse(description string) *huma.Response {
	const stringType = "string"
	return &huma.Response{Description: description, Content: map[string]*huma.MediaType{
		"application/problem+json": {
			Schema: &huma.Schema{
				Type:     "object",
				Required: []string{"type", "title", "status", "detail", "code"},
				Properties: map[string]*huma.Schema{
					"type": {
						Type: stringType,
					},
					"title":  {Type: stringType},
					"status": {Type: "integer"},
					"detail": {Type: stringType},
					"code":   {Type: stringType},
				},
			},
		},
	}}
}
func automationAPIError(err error) error {
	var validation *automations.AutomationDefinitionValidationError
	switch {
	case errors.As(err, &validation):
		details := make([]error, len(validation.Issues))
		for i, issue := range validation.Issues {
			details[i] = &huma.ErrorDetail{Location: "body" + issue.Path, Message: issue.Message}
		}
		return huma.Error422UnprocessableEntity("invalid automation definition", details...)
	case errors.Is(err, automations.ErrInvalidAutomation):
		return huma.Error400BadRequest("invalid automation request")
	case errors.Is(err, automations.ErrAutomationNotFound), errors.Is(err, automations.ErrAutomationRunNotFound):
		return huma.Error404NotFound("automation resource not found")
	case errors.Is(err, automations.ErrAutomationRevisionConflict):
		return automationCodedProblem(
			http.StatusConflict,
			"automation_revision_conflict",
			"automation revision conflict",
		)
	case errors.Is(err, automations.ErrAutomationRunActive):
		return automationCodedProblem(http.StatusConflict, "automation_run_active", "automation run is active")
	case errors.Is(err, automations.ErrAutomationUnavailable):
		return automationCodedProblem(
			http.StatusServiceUnavailable,
			"automation_unavailable",
			"automation admission is unavailable",
		)
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}

// automationAPI removes Huma's input echo before schema-link transformation, including malformed
// RawMessage bodies and query values, without changing other modules or statuses.
type automationAPI struct{ *huma.Group }

func (api automationAPI) Transform(ctx huma.Context, status string, value any) (any, error) {
	if problem, ok := value.(*huma.ErrorModel); ok {
		for _, detail := range problem.Errors {
			detail.Value = nil
			// Codec issues already carry safe JSON Pointer paths; parser errors do not.
			if !strings.HasPrefix(detail.Location, "body/") {
				detail.Message = "invalid request value"
			}
		}
	}
	return api.Group.Transform(ctx, status, value)
}
