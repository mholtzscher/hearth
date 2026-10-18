package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type Adapters interface {
	ListAdapters(context.Context, adapters.ListAdaptersParams) (adapters.Page[adapters.AdapterInstance], error)
	GetAdapter(context.Context, string) (adapters.AdapterInstance, error)
	ListAdapterHealthHistory(
		context.Context,
		adapters.ListAdapterHealthParams,
	) (adapters.Page[adapters.HealthTransition], error)
}

type Handler struct {
	adapters Adapters
}

func Register(api huma.API, service Adapters) {
	const adaptersTag = "Adapters"
	handler := &Handler{adapters: service}
	huma.Register(api, huma.Operation{
		OperationID: "list-adapters", Method: http.MethodGet, Path: "/adapters",
		Summary: "List Adapters and their current health", Tags: []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.ListAdapters)
	huma.Register(api, huma.Operation{
		OperationID: "get-adapter", Method: http.MethodGet, Path: "/adapters/{adapter_id}",
		Summary: "Get an Adapter and its current health", Tags: []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetAdapter)
	huma.Register(api, huma.Operation{
		OperationID: "list-adapter-health-history", Method: http.MethodGet,
		Path: "/adapters/{adapter_id}/health/history", Summary: "List an Adapter's health history",
		Tags:   []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.ListAdapterHealthHistory)
}
