package hearthd //nolint:testpackage // Assembly fixtures implement the private constructor's consuming seams.

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type stubFunctions struct{ closed bool }

func (stub *stubFunctions) AdmissionOpen() bool { return !stub.closed }
func (*stubFunctions) ListFunctions(context.Context, string, adapters.PageInput) (adapters.FunctionPage, error) {
	return adapters.FunctionPage{Items: []adapters.Function{}}, nil
}
func (*stubFunctions) GetFunction(context.Context, string, adapters.FunctionKey) (adapters.Function, error) {
	return adapters.Function{}, adapters.ErrFunctionNotFound
}
func (*stubFunctions) Invoke(context.Context, adapters.InvokeInput) (adapters.Invocation, error) {
	return adapters.Invocation{}, adapters.ErrAdapterUnavailable
}
func (*stubFunctions) ListInvocations(context.Context, adapters.InvocationFilter) (adapters.InvocationPage, error) {
	return adapters.InvocationPage{Items: []adapters.Invocation{}}, nil
}
func (*stubFunctions) GetInvocation(context.Context, adapters.InvocationID) (adapters.Invocation, error) {
	return adapters.Invocation{}, adapters.ErrInvocationNotFound
}
