package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func registerGetAdapter(api huma.API, handler *handler) {
	const adaptersTag = "Adapters"
	huma.Register(api, huma.Operation{
		OperationID: "get-adapter", Method: http.MethodGet, Path: "/adapters/{adapter_id}",
		Summary: "Get an Adapter and its current health", Tags: []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.getAdapter)
}

type GetAdapterInput struct {
	AdapterID string `path:"adapter_id" doc:"Subject-safe Adapter ID"`
}

type GetAdapterOutput struct {
	Body AdapterBody
}

func (handler *handler) getAdapter(ctx context.Context, input *GetAdapterInput) (*GetAdapterOutput, error) {
	if !validAdapterID(input.AdapterID) {
		return nil, huma.Error400BadRequest("adapter_id must be a subject-safe slug")
	}
	adapter, err := handler.adapters.GetAdapter(ctx, input.AdapterID)
	switch {
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return nil, huma.Error404NotFound("adapter not found")
	case err != nil:
		return nil, handler.internalAPIError(ctx, "get-adapter", "service", err)
	}
	return &GetAdapterOutput{Body: adapterBody(adapter)}, nil
}
