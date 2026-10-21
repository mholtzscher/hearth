package httpapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func operation(id, method, path, summary string, tags ...string) huma.Operation {
	if len(tags) == 0 {
		tags = []string{"Automations"}
	}
	return huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        tags,
		Errors:      []int{http.StatusBadRequest, http.StatusInternalServerError},
		Responses: map[string]*huma.Response{
			"409": problemResponse("Revision conflict or a Run is already active"),
			"503": problemResponse("Automation admission is unavailable"),
		},
	}
}
