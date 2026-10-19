package simulator

import (
	"net/http"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

// registerFunctionControls exposes the one control a held simulated Function
// needs: releasing it. Core owns discovery, invocation, and retained history, so
// this channel adds no list, capture, or behavior mutation.
func registerFunctionControls(mux *http.ServeMux, runtimes map[string]*scripted.FunctionRuntime) {
	mux.HandleFunc(
		"POST /v1/sim/functions/invocations/{invocation_id}/release",
		func(writer http.ResponseWriter, request *http.Request) {
			invocationID := request.PathValue("invocation_id")
			for _, runtime := range runtimes {
				if err := runtime.Release(invocationID); err == nil {
					writer.WriteHeader(http.StatusNoContent)
					return
				}
			}
			writeControlError(writer, http.StatusNotFound, "unknown scripted function invocation")
		},
	)
}
