package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// internalAPIError consumes the private cause at its diagnostic owner before
// returning a native Huma problem with no cause in its serializable details.
func (handler *handler) internalAPIError(ctx context.Context, operation, stage string, cause error) error {
	handler.logInternalFailure(ctx, operation, stage, cause)
	return huma.NewError(http.StatusInternalServerError, "internal error")
}

func (handler *handler) logInternalFailure(ctx context.Context, operation, stage string, cause error) {
	if expectedRequestCancellation(ctx, cause) {
		return
	}
	handler.logger.ErrorContext(ctx, "device HTTP operation failed",
		slog.String("event", "device.http_internal_failure"),
		slog.String("operation", operation), slog.String("stage", stage),
		slog.String("error_code", "internal_error"), slog.String("error_type", fmt.Sprintf("%T", cause)),
	)
}

func expectedRequestCancellation(ctx context.Context, err error) bool {
	ownerErr := ctx.Err()
	if ownerErr == nil {
		return false
	}
	for err != nil {
		if err == ownerErr { //nolint:errorlint // Require the exact owner sentinel in a single-cause chain.
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

const problemJSONContentType = "application/problem+json"

type labelConflictError struct {
	huma.ErrorModel

	Code string `json:"code" enum:"label_name_conflict,label_limit_reached"`
}

// Schema retains the required domain fields, not the native optional base shape.
func (labelConflictError) Schema(registry huma.Registry) *huma.Schema {
	type fields labelConflictError
	schema := huma.SchemaFromType(registry, reflect.TypeFor[fields]())
	schema.Required = append(schema.Required, "type", "title", "status", "detail")
	return schema
}

func labelConflictProblem(detail, code string) error {
	problem := &labelConflictError{Code: code}
	problem.Type = "about:blank"
	problem.Title = http.StatusText(http.StatusConflict)
	problem.Status = http.StatusConflict
	problem.Detail = detail
	return problem
}

func labelConflictResponses(api huma.API) map[string]*huma.Response {
	schema := huma.SchemaFromType(api.OpenAPI().Components.Schemas, reflect.TypeFor[labelConflictError]())
	return map[string]*huma.Response{
		"409": {
			Description: http.StatusText(http.StatusConflict),
			Content:     map[string]*huma.MediaType{problemJSONContentType: {Schema: schema}},
		},
	}
}

// labelError maps domain Label errors shared by the Label operations.
func labelError(err error) error {
	switch {
	case errors.Is(err, devices.ErrInvalidLabel):
		return huma.NewError(http.StatusBadRequest, "invalid label")
	case errors.Is(err, devices.ErrLabelNotFound):
		return huma.NewError(http.StatusNotFound, "label not found")
	case errors.Is(err, devices.ErrLabelNameConflict):
		return labelConflictProblem("a label with this name already exists", "label_name_conflict")
	case errors.Is(err, devices.ErrLabelLimitReached):
		return labelConflictProblem("the household label limit has been reached", "label_limit_reached")
	default:
		return nil
	}
}
