//go:build integration || e2e

package hearthd //nolint:testpackage // Application assembly fixtures.

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const automationFunctionArguments = `{"sequence":9007199254740993,"tiny":0.125,"zero":0}`

func automationTestFunction(handler adapter.FunctionHandler) adapter.Function {
	function := expressionIntegrationFunction(handler)
	function.Descriptor.InputSchema = json.RawMessage(
		`{"type":"object","properties":{"sequence":{"type":"integer"},"tiny":{"type":"number"},"zero":{"type":"number"}},"required":["sequence","tiny","zero"],"additionalProperties":false}`,
	)
	return function
}
