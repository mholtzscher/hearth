package mcpapi

// NormalizeSchemaForTest exercises schema rewriting without a runtime tool
// schema override API, which the registration convention deliberately removes.
func NormalizeSchemaForTest(schema any) (map[string]any, error) {
	return normalizePortableSchema(schema)
}
