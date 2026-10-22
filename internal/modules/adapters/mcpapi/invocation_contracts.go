//nolint:goconst // Shared invocation identity fields stay literal beside their constraints.
package mcpapi

import "github.com/danielgtaylor/huma/v2"

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
