package mcpapi //nolint:testpackage // Pin native schema hooks for management evidence.

import (
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestManagementSchemasRetainNullableEvidence(t *testing.T) {
	t.Parallel()
	registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	detail := huma.SchemaFromType(registry, reflect.TypeFor[AdapterDetail]())
	detail.Extensions = map[string]any{"$defs": registry.Map()}
	schema := compileFunctionSchema(t, detail)
	const fields = `"status":"available","source":"s","since":"t","evidence_at":"t"`
	functionSchemaAccepts(t, schema, `{"adapter_id":"demo","health":{`+fields+`}}`, true)
	functionSchemaAccepts(
		t,
		schema,
		`{"adapter_id":"demo","health":{`+fields+`,"source_observed_at":null,"runtime":null}}`,
		true,
	)
	functionSchemaAccepts(
		t,
		schema,
		`{"adapter_id":"demo","health":{`+fields+`,"runtime":{"status":"active","software_name":"demo","software_version":"1","last_heartbeat_at":null,"lease_expires_at":"t"}}}`,
		true,
	)
	functionSchemaAccepts(t, schema, `{"adapter_id":"demo","health":{`+fields+`,"runtime":[]}}`, false)
	functionSchemaAccepts(t, schema, `{"adapter_id":"demo","health":{`+fields+`,"runtime":{"extra":true}}}`, false)

	history := huma.SchemaFromType(
		huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer),
		reflect.TypeFor[HealthTransition](),
	)
	functionSchemaAccepts(
		t,
		compileFunctionSchema(t, history),
		`{"status":"available","source":"s","source_observed_at":null,"observed_at":"t"}`,
		true,
	)
}
