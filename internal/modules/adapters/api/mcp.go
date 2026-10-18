package api

import (
	"context"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

const (
	mcpToolListAdapters             = "list_adapters"
	mcpToolGetAdapter               = "get_adapter"
	mcpToolListAdapterHealthHistory = "list_adapter_health_history"
)

func RegisterMCP(server *mcpapi.Server, service Adapters) {
	handler := &Handler{adapters: service}
	mcpapi.Register(server, mcpapi.Tool[mcpListAdaptersInput, AdapterCollectionBody]{
		Name: mcpToolListAdapters, Description: "List Adapters and their current health", Handler: handler.listAdapters,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpGetAdapterInput, AdapterBody]{
		Name: mcpToolGetAdapter, Description: "Get an Adapter and its current health", Handler: handler.getAdapter,
	})
	mcpapi.Register(server, mcpapi.Tool[mcpListAdapterHealthHistoryInput, AdapterHealthTransitionCollectionBody]{
		Name:        mcpToolListAdapterHealthHistory,
		Description: "List an Adapter's health history",
		Handler:     handler.listAdapterHealthHistory,
	})
	handler.registerResources(server)
}

func mcpRead[I, O any](
	ctx context.Context,
	read func(context.Context, *I) (*O, error),
	input *I,
	missing mcpFailureCode,
) (*O, error) {
	output, err := read(ctx, input)
	if err != nil {
		return nil, mcpReadFailure(err, missing)
	}
	if output == nil {
		return nil, mcpapi.InternalToolError(nil)
	}
	return output, nil
}

func (handler *Handler) listAdapters(ctx context.Context, input mcpListAdaptersInput) (AdapterCollectionBody, error) {
	output, err := mcpRead(
		ctx,
		handler.ListAdapters,
		&ListAdaptersInput{Limit: mcpPageSize(input.Limit), Cursor: input.Cursor},
		mcpFailureNone,
	)
	if err != nil {
		return AdapterCollectionBody{}, err
	}
	return output.Body, nil
}

func (handler *Handler) getAdapter(ctx context.Context, input mcpGetAdapterInput) (AdapterBody, error) {
	output, err := mcpRead(
		ctx,
		handler.GetAdapter,
		&GetAdapterInput{AdapterID: string(input.AdapterID)},
		mcpFailureAdapterNotFound,
	)
	if err != nil {
		return AdapterBody{}, err
	}
	return output.Body, nil
}

func (handler *Handler) listAdapterHealthHistory(
	ctx context.Context,
	input mcpListAdapterHealthHistoryInput,
) (AdapterHealthTransitionCollectionBody, error) {
	output, err := mcpRead(
		ctx,
		handler.ListAdapterHealthHistory,
		&ListAdapterHealthHistoryInput{
			AdapterID: string(input.AdapterID),
			Limit:     mcpPageSize(input.Limit),
			Cursor:    input.Cursor,
		},
		mcpFailureAdapterNotFound,
	)
	if err != nil {
		return AdapterHealthTransitionCollectionBody{}, err
	}
	return output.Body, nil
}
