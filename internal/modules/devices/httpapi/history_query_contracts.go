package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// historyQueryResponses publishes a history POST's result schema beside the
// problem responses both history operations share.
//
//nolint:goconst // The operations advertise JSON request and response media.
func historyQueryResponses(registry huma.Registry, result *huma.Schema) map[string]*huma.Response {
	problem := registry.Schema(reflect.TypeFor[historyProblemError](), false, "")
	framework := registry.Schema(reflect.TypeFor[huma.ErrorModel](), true, "")
	responses := map[string]*huma.Response{
		"200": {Description: "Query result", Content: map[string]*huma.MediaType{"application/json": {Schema: result}}},
	}
	for _, status := range []int{400, 404, 408, 413, 415, 422, 500, 503} {
		responses[strconv.Itoa(status)] = &huma.Response{
			Description: http.StatusText(status),
			Content: map[string]*huma.MediaType{
				"application/problem+json": {Schema: &huma.Schema{AnyOf: []*huma.Schema{problem, framework}}},
			},
		}
	}
	return responses
}

// encodeHistoryAnswer encodes a mapped history result, refusing output over
// the shared result limit. Mapping and encoding failures are internal.
func (h *handler) encodeHistoryAnswer(
	ctx context.Context, operation string, mapped any, mapErr error,
) (*queryHistoryOutput, error) {
	internal := historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
	if mapErr != nil {
		h.logInternalFailure(ctx, operation, "mapping", mapErr)
		return nil, internal
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		h.logInternalFailure(ctx, operation, "mapping", err)
		return nil, internal
	}
	if len(encoded) > devices.HistoryMaxResultBytes {
		return nil, historyQueryProblem(
			http.StatusUnprocessableEntity, "query_limit", "History query result exceeds the limit.", "",
		)
	}
	return &queryHistoryOutput{ContentType: "application/json", Body: encoded}, nil
}
