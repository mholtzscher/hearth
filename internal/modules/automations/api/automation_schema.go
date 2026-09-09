package api

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// publishAutomationDefinitionSchema preserves every canonical JSON Schema keyword,
// including resource-local references that Huma's typed Schema cannot represent.
func publishAutomationDefinitionSchema(api huma.API, raw json.RawMessage) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		panic(fmt.Errorf("automation API schema publication: %w", err))
	}
	api.OpenAPI().Components.Schemas.Map()["AutomationDefinition"] = &huma.Schema{Extensions: document}
}

func automationDefinitionRequestBody() *huma.RequestBody {
	return &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		"application/json": {Schema: &huma.Schema{Ref: "#/components/schemas/AutomationDefinition"}},
	}}
}
