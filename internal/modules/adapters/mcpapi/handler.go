package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

// Adapters is the management read surface consumed by these tools.
type Adapters interface {
	ListAdapters(context.Context, adapters.ListAdaptersParams) (adapters.Page[adapters.AdapterInstance], error)
	GetAdapter(context.Context, string) (adapters.AdapterInstance, error)
	ListAdapterHealthHistory(
		context.Context,
		adapters.ListAdapterHealthParams,
	) (adapters.Page[adapters.HealthTransition], error)
}

// Functions preserves the independent Function service and admission policy.
type Functions interface {
	ListFunctions(context.Context, string, adapters.PageInput) (adapters.FunctionPage, error)
	GetFunction(context.Context, string, adapters.FunctionKey) (adapters.Function, error)
	Invoke(context.Context, adapters.InvokeInput) (adapters.Invocation, error)
	ListInvocations(context.Context, adapters.InvocationFilter) (adapters.InvocationPage, error)
	GetInvocation(context.Context, adapters.InvocationID) (adapters.Invocation, error)
}

type handler struct {
	adapters  Adapters
	functions Functions
}
