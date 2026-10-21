//nolint:goconst // Literal schema keywords and wire fields keep operation contracts reviewable.
package mcpapi

func runLifecycleSchema() []any {
	return []any{
		map[string]any{
			"properties": map[string]any{"status": enumSchema("running")},
			"not": map[string]any{
				"anyOf": []any{
					map[string]any{"required": []string{"completed_at"}},
					map[string]any{"required": []string{"failure_code"}},
				},
			},
		},
		map[string]any{
			"properties": map[string]any{"status": enumSchema("succeeded")},
			"required":   []string{"completed_at"},
			"not":        map[string]any{"required": []string{"failure_code"}},
		},
		map[string]any{
			"properties": map[string]any{"status": enumSchema("failed", "interrupted")},
			"required":   []string{"completed_at", "failure_code"},
		},
	}
}
