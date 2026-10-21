package mcpapi //nolint:testpackage // Shared transport fixtures exercise the module's public registrations.

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type stubAdapters struct {
	listAdapters             func(context.Context, adapters.ListAdaptersParams) (adapters.Page[adapters.AdapterInstance], error)
	getAdapter               func(context.Context, string) (adapters.AdapterInstance, error)
	listAdapterHealthHistory func(context.Context, adapters.ListAdapterHealthParams) (adapters.Page[adapters.HealthTransition], error)
}

func (stub *stubAdapters) ListAdapters(
	ctx context.Context,
	params adapters.ListAdaptersParams,
) (adapters.Page[adapters.AdapterInstance], error) {
	if stub.listAdapters == nil {
		panic("unexpected ListAdapters call")
	}
	return stub.listAdapters(ctx, params)
}

func (stub *stubAdapters) GetAdapter(ctx context.Context, id string) (adapters.AdapterInstance, error) {
	if stub.getAdapter == nil {
		panic("unexpected GetAdapter call")
	}
	return stub.getAdapter(ctx, id)
}

func (stub *stubAdapters) ListAdapterHealthHistory(
	ctx context.Context,
	params adapters.ListAdapterHealthParams,
) (adapters.Page[adapters.HealthTransition], error) {
	if stub.listAdapterHealthHistory == nil {
		panic("unexpected ListAdapterHealthHistory call")
	}
	return stub.listAdapterHealthHistory(ctx, params)
}

const apiAdapterID = "simulator"
