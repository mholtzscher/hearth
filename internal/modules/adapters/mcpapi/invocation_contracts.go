//nolint:goconst // Complete closed alternatives pin terminal evidence semantics.
package mcpapi

import "github.com/danielgtaylor/huma/v2"

func invocationRecordSchema(detail bool) *huma.Schema {
	function := functionSummarySchema().Properties
	var variants []*huma.Schema
	for _, status := range []string{"running", "succeeded", "failed", "not_executed", "unknown"} {
		properties, required := invocationIdentitySchema(status, function)
		properties["started_at"], properties["completed_at"] = textSchema(), textSchema()
		required = append(required, "started_at", "completed_at")
		if detail {
			properties["arguments"], properties["deadline"] = objectSchema(), textSchema()
			required = append(required, "arguments", "deadline")
		}
		switch status {
		case "running":
			properties["completed_at"] = &huma.Schema{Type: "null"}
		case "succeeded":
			if detail {
				properties["result"] = objectSchema()
				required = append(required, "result")
			}
		default:
			properties["reason_code"] = &huma.Schema{Type: "string", Pattern: `^[a-z0-9][a-z0-9_.-]{0,127}$`}
			required = append(required, "reason_code")
		}
		variants = append(variants, functionToolSchema(properties, required...))
	}
	return &huma.Schema{Type: "object", OneOf: variants}
}

func invocationIdentitySchema(status string, function map[string]*huma.Schema) (map[string]*huma.Schema, []string) {
	return map[string]*huma.Schema{
		"invocation_id": {
			Type: "string", Pattern: `^afi_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		},
		"adapter_id":       function["adapter_id"],
		"function_key":     function["function_key"],
		"catalog_revision": function["catalog_revision"],
		"status":           {Type: "string", Enum: []any{status}},
	}, []string{"invocation_id", "adapter_id", "function_key", "catalog_revision", "status"}
}
