package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"

// Register installs the complete Adapter catalog with independent dependencies.
func Register(server *mcptools.Server, management Adapters, functions Functions) {
	h := &handler{adapters: management}
	f := &functionHandler{service: functions}
	registerListAdapters(server, h)
	registerGetAdapter(server, h)
	registerListAdapterHealthHistory(server, h)
	registerListAdapterFunctions(server, f)
	registerGetAdapterFunction(server, f)
	registerInvokeAdapterFunction(server, f)
	registerListAdapterFunctionInvocations(server, f)
	registerGetAdapterFunctionInvocation(server, f)
}
