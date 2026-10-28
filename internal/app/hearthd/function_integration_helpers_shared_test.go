//go:build integration || e2e

package hearthd //nolint:testpackage // Exercises private application assembly through real transports.

import (
	"encoding/json"
	"testing"

	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	adaptersmcp "github.com/mholtzscher/hearth/internal/modules/adapters/mcpapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const functionExactObject = `{"sequence":9007199254740993,"tiny":1e-10000,"zero":0e10000}`

// Automation outputs must enter the strict expression environment. Keep opaque
// vendor arguments adversarial, but return a representable successful value.
const automationFunctionOutput = `{"sequence":9007199254740993,"tiny":0.125,"zero":0}`

func expressionIntegrationFunction(handler adapter.FunctionHandler) adapter.Function {
	function := exactIntegrationFunction(handler)
	function.Descriptor.OutputSchema = json.RawMessage(
		`{"type":"object","properties":{"sequence":{"type":"integer"},"tiny":{"type":"number"},"zero":{"type":"number"}},"required":["sequence","tiny","zero"],"additionalProperties":false}`,
	)
	return function
}

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
	assertFunctionInvocationResult(t, invocation, functionExactObject)
}

func assertFunctionInvocationResult(t *testing.T, invocation adaptersapi.InvocationBody, result string) {
	t.Helper()
	if invocation.ID == "" || invocation.Status != "succeeded" || invocation.CompletedAt == nil ||
		string(invocation.Arguments) != functionExactObject || string(invocation.Result) != result {
		t.Fatalf("exact retained successful invocation: %+v", invocation)
	}
}

func assertExactFunctionMCPReceipt(t *testing.T, receipt adaptersmcp.InvocationSuccess) {
	t.Helper()
	if receipt.InvocationID == "" || receipt.Status != "succeeded" || string(receipt.Result) != functionExactObject {
		t.Fatalf("exact compact receipt: %+v", receipt)
	}
}

func assertExactFunctionMCPDetail(t *testing.T, detail adaptersmcp.InvocationDetail, id string) {
	t.Helper()
	assertFunctionMCPResult(t, detail, id, functionExactObject)
}

func assertFunctionMCPResult(t *testing.T, detail adaptersmcp.InvocationDetail, id, result string) {
	t.Helper()
	if detail.InvocationID != id || detail.Status != "succeeded" || detail.CompletedAt == nil ||
		string(detail.Arguments) != functionExactObject || string(detail.Result) != result {
		t.Fatalf("exact retained MCP evidence: %+v", detail)
	}
}
