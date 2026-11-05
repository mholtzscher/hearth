package agent

import (
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

const functionToolType = "function"

// Both provider protocols receive the complete MCP schema without normalizing
// optional properties or changing the contract's validation vocabulary.
func toolParameters(info *schema.ToolInfo) (map[string]any, error) {
	parameters, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		return nil, fmt.Errorf("agent tool %s schema: %w", info.Name, err)
	}
	raw, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}
