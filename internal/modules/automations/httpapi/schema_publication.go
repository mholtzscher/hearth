package httpapi

import (
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
)

// publishDefinitionSchema keeps the canonical resource ID so its local references
// work inside OpenAPI and at Huma's standalone schema endpoint.
func publishDefinitionSchema(api huma.API, raw json.RawMessage) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		panic(fmt.Errorf("automation API schema publication: %w", err))
	}
	api.OpenAPI().Components.Schemas.Map()["AutomationDefinition"] = &huma.Schema{Extensions: document}
}
