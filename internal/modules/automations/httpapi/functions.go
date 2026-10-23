package httpapi

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationFunctionExecutionBody carries canonical evidence independently of invocation retention.
type AutomationFunctionExecutionBody struct{ encoded json.RawMessage }

func (body AutomationFunctionExecutionBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
}

func functionExecutionBody(execution automations.FunctionExecution) AutomationFunctionExecutionBody {
	raw, err := automations.EncodeFunctionExecution(execution)
	if err != nil {
		panic(fmt.Errorf("invalid retained Function execution: %w", err))
	}
	return AutomationFunctionExecutionBody{encoded: raw}
}
