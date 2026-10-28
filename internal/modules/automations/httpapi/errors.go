package httpapi

import (
	"errors"
	"net/http"
	"reflect"
	"strconv"

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
// code and optional safe, structured diagnostics.
type automationProblemError struct {
	Type   string              `json:"type"             format:"uri" default:"about:blank"`
	Title  string              `json:"title"`
	Status int                 `json:"status"`
	Detail string              `json:"detail"`
	Code   string              `json:"code"`
	Errors []*huma.ErrorDetail `json:"errors,omitempty"`
}

func (problem *automationProblemError) Error() string { return problem.Detail }

func (problem *automationProblemError) GetStatus() int { return problem.Status }

func (*automationProblemError) ContentType(string) string {
	return problemContentType
}

func newProblem(status int, code, detail string) *automationProblemError {
	return &automationProblemError{
		Type: "about:blank", Title: http.StatusText(status), Status: status, Detail: detail, Code: code,
	}
}

// problemResponses publishes domain problems alongside framework failures for
// statuses that Huma can return before or outside the operation handler.
func problemResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	registry := api.OpenAPI().Components.Schemas
	domain := registry.Schema(reflect.TypeFor[automationProblemError](), false, "")
	responses := make(map[string]*huma.Response, len(statuses))
	for _, status := range statuses {
		schema := domain
		switch status {
		case http.StatusBadRequest,
			http.StatusUnprocessableEntity,
			http.StatusRequestEntityTooLarge,
			http.StatusInternalServerError:
			schema = &huma.Schema{AnyOf: []*huma.Schema{
				domain, registry.Schema(reflect.TypeFor[huma.ErrorModel](), true, ""),
			}}
		}
		responses[strconv.Itoa(status)] = &huma.Response{
			Description: http.StatusText(status),
			Content:     map[string]*huma.MediaType{problemContentType: {Schema: schema}},
		}
	}
	return responses
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
		problem := newProblem(
			http.StatusUnprocessableEntity,
			"invalid_function_reference",
			"invalid automation Function reference",
		)
		problem.Errors = []*huma.ErrorDetail{{
			Location: "body.steps", Message: "step " + string(reference.StepID) + ": " + reference.ReasonCode,
		}}
		return problem
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
		return newProblem(http.StatusInternalServerError, "internal_error", "internal error")
	}
}
