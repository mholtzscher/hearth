package httpapi

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
)

// Register publishes Adapter operations. A nil logger discards HTTP diagnostics.
func Register(api huma.API, management Adapters, functions Functions, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &handler{adapters: management, functions: functions, logger: logger}
	registerListAdapters(api, h)
	registerGetAdapter(api, h)
	registerListAdapterHealthHistory(api, h)
	registerListAdapterFunctions(api, h)
	registerGetAdapterFunction(api, h)
	registerInvokeAdapterFunction(api, h)
	registerListAdapterFunctionInvocations(api, h)
	registerGetAdapterFunctionInvocation(api, h)
}
