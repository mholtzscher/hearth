package httpapi

import "github.com/danielgtaylor/huma/v2"

func Register(api huma.API, management Adapters, functions Functions) {
	h := &handler{adapters: management}
	f := &functionHandler{service: functions}
	registerListAdapters(api, h)
	registerGetAdapter(api, h)
	registerListAdapterHealthHistory(api, h)
	registerListAdapterFunctions(api, f)
	registerGetAdapterFunction(api, f)
	registerInvokeAdapterFunction(api, f)
	registerListAdapterFunctionInvocations(api, f)
	registerGetAdapterFunctionInvocation(api, f)
}
