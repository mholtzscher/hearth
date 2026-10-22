package httpapi

import "github.com/danielgtaylor/huma/v2"

func Register(api huma.API, management Adapters, functions Functions) {
	h := &handler{adapters: management, functions: functions}
	registerListAdapters(api, h)
	registerGetAdapter(api, h)
	registerListAdapterHealthHistory(api, h)
	registerListAdapterFunctions(api, h)
	registerGetAdapterFunction(api, h)
	registerInvokeAdapterFunction(api, h)
	registerListAdapterFunctionInvocations(api, h)
	registerGetAdapterFunctionInvocation(api, h)
}
