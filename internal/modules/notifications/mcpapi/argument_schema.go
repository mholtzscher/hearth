package mcpapi

const (
	schemaType           = "type"
	schemaString         = "string"
	maximumPageLimit     = 200
	maximumTitleLength   = 256
	maximumMessageLength = 2000
	defaultPageLimit     = 50
)

// argumentSchema distinguishes omission from null before Go scalar decoding.
func argumentSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		schemaType: "object", "additionalProperties": false,
		"properties": properties, "required": required,
	}
}

func stringSchema() map[string]any { return map[string]any{schemaType: schemaString} }

func pageProperties() map[string]any {
	return map[string]any{
		"limit":  map[string]any{schemaType: "integer", "minimum": 1, "maximum": maximumPageLimit},
		"cursor": stringSchema(),
	}
}

func pageLimit(limit int) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return limit
}
