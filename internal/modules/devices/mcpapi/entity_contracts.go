//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

func availabilitySummarySchema() map[string]any {
	return recordSchema(map[string]any{"status": stringSchema(), "reason_code": stringSchema()}, "status")
}

func stateSummarySchema() map[string]any {
	return recordSchema(
		map[string]any{"value": map[string]any{}, "observed_at": stringSchema()},
		"value",
		"observed_at",
	)
}

func entitySummarySchema() map[string]any {
	return recordSchema(map[string]any{
		"entity_id": stringSchema(), "device_id": stringSchema(), "name": stringSchema(), "type": stringSchema(),
		"enabled": map[string]any{"type": "boolean"}, "availability": availabilitySummarySchema(),
		"state": nullableSchema(stateSummarySchema()),
	}, "entity_id", "device_id", "name", "type", "enabled", "availability", "state")
}
