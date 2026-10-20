//go:build integration || e2e

package hearthd //nolint:testpackage // Exercises private application assembly through real transports.

import (
	"encoding/json"
	"testing"

	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/api"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const functionExactObject = `{"sequence":9007199254740993,"tiny":1e-10000,"zero":0e10000}`

func exactIntegrationFunction(handler adapter.FunctionHandler) adapter.Function {
	schema := json.RawMessage(
		`{"type":"object","properties":{"sequence":{"type":"integer","const":9007199254740993},"tiny":{"type":"number","const":1e-10000},"zero":{"type":"number","const":0e10000}},"required":["sequence","tiny","zero"],"additionalProperties":false}`,
	)
	return adapter.Function{
		Descriptor: adapter.FunctionDescriptor{Key: "inspect", Description: "Return the supplied sequence",
			InputSchema: schema, OutputSchema: schema, TimeoutMS: 60000},
		Handler: handler,
	}
}

func assertExactFunctionInvocation(t *testing.T, invocation adaptersapi.InvocationBody) {
	t.Helper()
	if invocation.ID == "" || invocation.Status != "succeeded" || invocation.CompletedAt == nil ||
		string(invocation.Arguments) != functionExactObject || string(invocation.Result) != functionExactObject {
		t.Fatalf("exact retained successful invocation: %+v", invocation)
	}
}
