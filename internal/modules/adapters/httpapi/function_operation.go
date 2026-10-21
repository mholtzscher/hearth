package httpapi

import (
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

func functionOperation(api huma.API, id, method, path, summary string) huma.Operation {
	errors := []int{
		http.StatusUnprocessableEntity,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusServiceUnavailable,
		http.StatusInternalServerError,
	}
	responses := make(map[string]*huma.Response, len(errors))
	for _, status := range errors {
		responses[strconv.Itoa(status)] = &huma.Response{Description: http.StatusText(status),
			Content: map[string]*huma.MediaType{"application/problem+json": {
				Schema: huma.SchemaFromType(
					api.OpenAPI().Components.Schemas,
					reflect.TypeFor[functionProblemError](),
				),
			}}}
	}
	return huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        []string{"Adapters"},
		Errors:      errors,
		Responses:   responses,
	}
}
