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

// A transformer lets Huma register one component shared by create and save.
// Inlining the same $id twice would declare duplicate schema resources.
func (definitionContract) TransformSchema(_ huma.Registry, _ *huma.Schema) *huma.Schema {
	var document map[string]any
	if err := json.Unmarshal(viewsv1.Schema, &document); err != nil {
		panic(fmt.Errorf("dashboard catalog schema: %w", err))
	}
	return &huma.Schema{Extensions: document}
}
