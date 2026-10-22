//nolint:goconst // Complete closed alternatives pin terminal evidence semantics.
package mcpapi

func invocationRecordSchema(detail bool) map[string]any {
	var variants []any
	for _, status := range []string{"running", "succeeded", "failed", "not_executed", "unknown"} {
		properties, required := invocationIdentitySchema(status)
		properties["started_at"], properties["completed_at"] = textSchema(), textSchema()
		required = append(required, "started_at", "completed_at")
		if detail {
			properties["arguments"], properties["deadline"] = objectSchema(), textSchema()
			required = append(required, "arguments", "deadline")
		}
		switch status {
		case "running":
			properties["completed_at"] = map[string]any{"type": "null"}
		case "succeeded":
			if detail {
				properties["result"] = objectSchema()
				required = append(required, "result")
			}
		default:
			properties["reason_code"] = map[string]any{"type": "string", "pattern": `^[a-z0-9][a-z0-9_.-]{0,127}$`}
			required = append(required, "reason_code")
		}
		variants = append(variants, functionToolSchema(properties, required...))
	}
	return map[string]any{"type": "object", "oneOf": variants}
}

func invocationIdentitySchema(status string) (map[string]any, []string) {
	function := functionSummaryProperties()
	return map[string]any{
		"invocation_id": map[string]any{
			"type":    "string",
			"pattern": `^afi_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		},
		"adapter_id":       function["adapter_id"],
		"function_key":     function["function_key"],
		"catalog_revision": function["catalog_revision"],
		"status":           map[string]any{"type": "string", "enum": []any{status}},
	}, []string{"invocation_id", "adapter_id", "function_key", "catalog_revision", "status"}
}
