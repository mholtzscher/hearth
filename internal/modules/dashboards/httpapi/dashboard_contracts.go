package httpapi

import (
	"encoding/json"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
)

// definitionContract publishes the authoritative catalog without a second
// property schema. Runtime decoding remains the strict raw-JSON boundary.
type definitionContract struct{}

func (definitionContract) Schema(_ huma.Registry) *huma.Schema {
	var document map[string]any
	if err := json.Unmarshal(viewsv1.Schema, &document); err != nil {
		panic(fmt.Errorf("dashboard catalog schema: %w", err))
	}
	return &huma.Schema{Extensions: document}
}
