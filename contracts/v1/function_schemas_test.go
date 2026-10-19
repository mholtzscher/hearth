package v1_test

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

const functionUUID = "01890f47-7a6b-7c4d-8e9f-0123456789ab"

// The spec's wire alternatives are exercised through the public embedded validator;
// dynamic schema semantics and cross-field identity equality belong to consumers.
func TestFunctionSchemasEnforceRequiredAndStrictOuterFields(t *testing.T) {
	t.Parallel()
	for _, schema := range []string{
		contractsv1.AdapterFunctionRegistrationRequestSchemaID,
		contractsv1.AdapterFunctionRegistrationResponseSchemaID,
		contractsv1.AdapterFunctionInvokeRequestSchemaID,
		contractsv1.AdapterFunctionInvokeResponseSchemaID,
	} {
		t.Run(schema, func(t *testing.T) {
			t.Parallel()
			envelope := functionEnvelope(schema)
			assertFunctionEnvelope(t, schema, envelope, true)
			for key := range envelope {
				t.Run("envelope/"+key, func(t *testing.T) {
					t.Parallel()
					mutated := maps.Clone(envelope)
					delete(mutated, key)
					assertFunctionEnvelope(t, schema, mutated, false)
					mutated[key] = nil
					assertFunctionEnvelope(t, schema, mutated, false)
				})
			}
			data := envelope["data"].(map[string]any)
			for key := range data {
				t.Run("data/"+key, func(t *testing.T) {
					t.Parallel()
					mutated := maps.Clone(data)
					delete(mutated, key)
					copyEnvelope := maps.Clone(envelope)
					copyEnvelope["data"] = mutated
					assertFunctionEnvelope(t, schema, copyEnvelope, false)
					mutated[key] = nil
					assertFunctionEnvelope(t, schema, copyEnvelope, false)
				})
			}
			for _, key := range []string{"adapter_id", "runtime_id", "unexpected"} {
				mutated := maps.Clone(envelope)
				mutated[key] = "forbidden"
				assertFunctionEnvelope(t, schema, mutated, false)
				mutated = maps.Clone(envelope)
				mutatedData := maps.Clone(data)
				mutatedData[key] = "forbidden"
				mutated["data"] = mutatedData
				assertFunctionEnvelope(t, schema, mutated, false)
			}
		})
	}
}

func TestFunctionRegistrationDescriptorBounds(t *testing.T) {
	t.Parallel()
	schema := contractsv1.AdapterFunctionRegistrationRequestSchemaID
	base := functionDescriptor()
	for field, values := range map[string][]any{
		"key":           {"", "has.dot", "*", strings.Repeat("a", 64)},
		"description":   {"", " \t\n", "\u00a0\u2000\u3000", strings.Repeat("界", 2001)},
		"input_schema":  {false, []any{}, "object"},
		"output_schema": {false, []any{}, "object"},
		"timeout_ms":    {99, 60001, 100.5, "100", json.Number("100.00000000000000001")},
		"unknown":       {true},
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			for _, value := range values {
				descriptor := maps.Clone(base)
				descriptor[field] = value
				assertFunctionDescriptor(t, descriptor, false)
			}
		})
	}
	for field := range base {
		descriptor := maps.Clone(base)
		delete(descriptor, field)
		assertFunctionDescriptor(t, descriptor, false)
		descriptor[field] = nil
		assertFunctionDescriptor(t, descriptor, false)
	}
	for _, timeout := range []int{100, 60000} {
		descriptor := maps.Clone(base)
		descriptor["timeout_ms"] = timeout
		descriptor["key"] = strings.Repeat("k", 63)
		descriptor["description"] = " " + strings.Repeat("界", 1998) + " "
		assertFunctionDescriptor(t, descriptor, true)
	}
	for _, count := range []int{0, 1, 64, 65} {
		functions := make([]any, count)
		for index := range functions {
			descriptor := maps.Clone(base)
			// Distinct keys avoid relying on downstream duplicate-key policy.
			descriptor["key"] = strings.Repeat("a", index/2+1) + string(rune('a'+index%2))
			functions[index] = descriptor
		}
		envelope := functionEnvelope(schema)
		envelope["data"].(map[string]any)["functions"] = functions
		assertFunctionEnvelope(t, schema, envelope, count >= 1 && count <= 64)
	}
}

func TestFunctionSchemasEnforceIdentityAndObjectRoots(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		schema, field string
		invalid       []any
	}{
		{contractsv1.AdapterFunctionRegistrationRequestSchemaID, "catalog_revision", []any{"afi_" + functionUUID, "afc_01890f47-7a6b-4c4d-8e9f-0123456789ab"}},
		{contractsv1.AdapterFunctionInvokeRequestSchemaID, "invocation_id", []any{"afc_" + functionUUID, "afi_01890f47-7a6b-7c4d-7e9f-0123456789ab"}},
		{contractsv1.AdapterFunctionInvokeRequestSchemaID, "catalog_revision", []any{"run_" + functionUUID}},
		{contractsv1.AdapterFunctionInvokeRequestSchemaID, "function_key", []any{"bad.key", "", "UPPER", strings.Repeat("a", 64)}},
		{contractsv1.AdapterFunctionInvokeRequestSchemaID, "arguments", []any{[]any{}, false, "object", 1}},
		{contractsv1.AdapterFunctionInvokeRequestSchemaID, "deadline", []any{"2026-10-18T12:00:00+00:00", "not-a-time", "2026-02-30T12:00:00Z"}},
		{contractsv1.AdapterFunctionInvokeResponseSchemaID, "invocation_id", []any{"cmd_" + functionUUID}},
		{contractsv1.AdapterFunctionInvokeResponseSchemaID, "result", []any{[]any{}, false, "object", 1}},
	} {
		for _, invalid := range tt.invalid {
			envelope := functionEnvelope(tt.schema)
			envelope["data"].(map[string]any)[tt.field] = invalid
			assertFunctionEnvelope(t, tt.schema, envelope, false)
		}
	}
	for schema, field := range map[string]string{
		contractsv1.AdapterFunctionInvokeRequestSchemaID:  "arguments",
		contractsv1.AdapterFunctionInvokeResponseSchemaID: "result",
	} {
		envelope := functionEnvelope(schema)
		envelope["data"].(map[string]any)[field] = map[string]any{}
		assertFunctionEnvelope(t, schema, envelope, true)
	}
	for _, schema := range []string{contractsv1.AdapterFunctionRegistrationRequestSchemaID, contractsv1.AdapterFunctionInvokeRequestSchemaID, contractsv1.AdapterFunctionRegistrationResponseSchemaID, contractsv1.AdapterFunctionInvokeResponseSchemaID} {
		for _, field := range []string{"id", "correlation_id", "causation_id", "schema"} {
			envelope := functionEnvelope(schema)
			if _, present := envelope[field]; !present {
				continue
			}
			envelope[field] = "cmd_" + functionUUID
			assertFunctionEnvelope(t, schema, envelope, false)
		}
	}
}

func TestFunctionResponsesDisallowContradictoryEvidence(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"failed", "not_executed", "unknown"} {
		for _, reason := range []any{"adapter_busy", "vendor.custom-failure_1", strings.Repeat("a", 128), "", "Upper", ".leading", "has space", strings.Repeat("a", 129), nil} {
			envelope := functionEnvelope(contractsv1.AdapterFunctionInvokeResponseSchemaID)
			data := map[string]any{"invocation_id": "afi_" + functionUUID, "status": status, "reason_code": reason}
			envelope["data"] = data
			valid := reason == "adapter_busy" || reason == "vendor.custom-failure_1" ||
				reason == strings.Repeat("a", 128)
			assertFunctionEnvelope(t, contractsv1.AdapterFunctionInvokeResponseSchemaID, envelope, valid)
			data["result"] = map[string]any{}
			assertFunctionEnvelope(t, contractsv1.AdapterFunctionInvokeResponseSchemaID, envelope, false)
		}
		envelope := functionEnvelope(contractsv1.AdapterFunctionInvokeResponseSchemaID)
		envelope["data"] = map[string]any{"invocation_id": "afi_" + functionUUID, "status": status}
		assertFunctionEnvelope(t, contractsv1.AdapterFunctionInvokeResponseSchemaID, envelope, false)
	}
	for _, tt := range []struct {
		schema string
		data   map[string]any
		valid  bool
	}{
		{contractsv1.AdapterFunctionRegistrationResponseSchemaID, map[string]any{"status": "rejected", "reason_code": "catalog_conflict"}, true},
		{contractsv1.AdapterFunctionRegistrationResponseSchemaID, map[string]any{"status": "rejected"}, false},
		{contractsv1.AdapterFunctionRegistrationResponseSchemaID, map[string]any{"status": "rejected", "reason_code": "bad code"}, false},
		{contractsv1.AdapterFunctionRegistrationResponseSchemaID, map[string]any{"status": "rejected", "reason_code": "catalog_conflict", "catalog_revision": "afc_" + functionUUID}, false},
		{contractsv1.AdapterFunctionRegistrationResponseSchemaID, map[string]any{"status": "accepted", "catalog_revision": "afc_" + functionUUID, "reason_code": "catalog_conflict"}, false},
		{contractsv1.AdapterFunctionInvokeResponseSchemaID, map[string]any{"invocation_id": "afi_" + functionUUID, "status": "succeeded", "result": map[string]any{}, "reason_code": "error"}, false},
		{contractsv1.AdapterFunctionInvokeResponseSchemaID, map[string]any{"invocation_id": "afi_" + functionUUID, "status": "running", "reason_code": "error"}, false},
	} {
		envelope := functionEnvelope(tt.schema)
		envelope["data"] = tt.data
		assertFunctionEnvelope(t, tt.schema, envelope, tt.valid)
	}
}

func assertFunctionDescriptor(t *testing.T, descriptor map[string]any, valid bool) {
	t.Helper()
	schema := contractsv1.AdapterFunctionRegistrationRequestSchemaID
	envelope := functionEnvelope(schema)
	envelope["data"].(map[string]any)["functions"] = []any{descriptor}
	assertFunctionEnvelope(t, schema, envelope, valid)
}

func assertFunctionEnvelope(t *testing.T, schema string, envelope map[string]any, valid bool) {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if validationErr := validator.Validate(schema, payload); (validationErr == nil) != valid {
		t.Fatalf("valid=%v, payload=%s, error=%v", valid, payload, validationErr)
	}
}

func functionDescriptor() map[string]any {
	return map[string]any{
		"key":         "refresh_entity",
		"description": "Refresh upstream data.",
		"timeout_ms":  10000,
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"vendor.field": map[string]any{"type": "string"}},
		},
		"output_schema": map[string]any{"type": "object", "additionalProperties": true},
	}
}

func functionEnvelope(schema string) map[string]any {
	envelope := map[string]any{
		"id": "rep_" + functionUUID, "schema": schema,
		"emitted_at": "2026-10-18T12:00:00.123456789Z", "correlation_id": "cor_" + functionUUID,
	}
	switch schema {
	case contractsv1.AdapterFunctionRegistrationRequestSchemaID:
		envelope["id"] = "afc_" + functionUUID
		envelope["data"] = map[string]any{
			"catalog_revision": "afc_" + functionUUID,
			"functions":        []any{functionDescriptor()},
		}
	case contractsv1.AdapterFunctionRegistrationResponseSchemaID:
		envelope["causation_id"] = "afc_" + functionUUID
		envelope["data"] = map[string]any{"status": "accepted", "catalog_revision": "afc_" + functionUUID}
	case contractsv1.AdapterFunctionInvokeRequestSchemaID:
		envelope["id"] = "afi_" + functionUUID
		envelope["data"] = map[string]any{
			"invocation_id":    "afi_" + functionUUID,
			"catalog_revision": "afc_" + functionUUID,
			"function_key":     "refresh_entity",
			"arguments": map[string]any{
				"vendor_field": json.Number("9007199254740993"), "nested": map[string]any{"free": true},
			},
			"deadline": "2026-10-18T12:01:00.123456789Z",
		}
	case contractsv1.AdapterFunctionInvokeResponseSchemaID:
		envelope["causation_id"] = "afi_" + functionUUID
		envelope["data"] = map[string]any{
			"invocation_id": "afi_" + functionUUID,
			"status":        "succeeded",
			"result":        map[string]any{"vendor_data": []any{true, nil}},
		}
	}
	return envelope
}
