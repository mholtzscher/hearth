package simulator

import (
	"net/http"
	"sort"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

type functionInvocationInfo struct {
	AdapterID    string `json:"adapter_id"`
	InvocationID string `json:"invocation_id"`
}

// registerFunctionControls exposes the pending holds and the one control a held
// Function needs: releasing it. Core owns discovery, invocation, and retained
// history. This channel adds no capture or behavior mutation.
func registerFunctionControls(mux *http.ServeMux, runtimes map[string]*scripted.FunctionRuntime) {
	mux.HandleFunc("GET /v1/sim/functions/invocations", func(writer http.ResponseWriter, _ *http.Request) {
		items := make([]functionInvocationInfo, 0)
		for adapterID, runtime := range runtimes {
			for _, invocationID := range runtime.Pending() {
				items = append(items, functionInvocationInfo{AdapterID: adapterID, InvocationID: invocationID})
			}
		}
		sort.Slice(items, func(left, right int) bool {
			if items[left].AdapterID != items[right].AdapterID {
				return items[left].AdapterID < items[right].AdapterID
			}
			return items[left].InvocationID < items[right].InvocationID
		})
		writeControlJSON(writer, http.StatusOK, items)
	})
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
