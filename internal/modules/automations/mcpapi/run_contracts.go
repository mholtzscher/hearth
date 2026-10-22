//nolint:goconst // Literal schema keywords and wire fields keep operation contracts reviewable.
package mcpapi

import "github.com/danielgtaylor/huma/v2"

func runLifecycleSchema() []*huma.Schema {
	return []*huma.Schema{
		{
			Properties: map[string]*huma.Schema{"status": {Enum: []any{"running"}}},
			Not: &huma.Schema{
				AnyOf: []*huma.Schema{
					{Required: []string{"completed_at"}},
					{Required: []string{"failure_code"}},
				},
			},
		},
		{
			Properties: map[string]*huma.Schema{"status": {Enum: []any{"succeeded"}}},
			Required:   []string{"completed_at"},
			Not:        &huma.Schema{Required: []string{"failure_code"}},
		},
		{
			Properties: map[string]*huma.Schema{"status": {Enum: []any{"failed", "interrupted"}}},
			Required:   []string{"completed_at", "failure_code"},
		},
	}
}
