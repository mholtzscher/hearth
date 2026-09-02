package v1_test

import (
	"fmt"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

const testMappingID = "map_01890f47-7a6b-7c4d-8e9f-0123456789ab"

func TestOwnedMappingsRequestSchemaEnforcesPageBounds(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.OwnedMappingsRequestSchemaID]

	for _, limit := range []int{1, 200} {
		request := ownedMappingsRequestFixture(t)
		request["data"].(map[string]any)["limit"] = limit
		if err := schema.Validate(request); err != nil {
			t.Errorf("limit %d rejected: %v", limit, err)
		}
	}
	for _, limit := range []int{-1, 0, 201} {
		request := ownedMappingsRequestFixture(t)
		request["data"].(map[string]any)["limit"] = limit
		if err := schema.Validate(request); err == nil {
			t.Errorf("limit %d unexpectedly accepted", limit)
		}
	}

	request := ownedMappingsRequestFixture(t)
	if err := schema.Validate(request); err != nil {
		t.Fatalf("request using default limit rejected: %v", err)
	}
	data := request["data"].(map[string]any)
	data["cursor"] = strings.Repeat("a", 2048)
	if err := schema.Validate(request); err != nil {
		t.Fatalf("2048-character cursor rejected: %v", err)
	}
	for _, cursor := range []string{"", strings.Repeat("a", 2049), strings.Repeat("é", 2048)} {
		data["cursor"] = cursor
		if err := schema.Validate(request); err == nil {
			t.Errorf("cursor of length %d unexpectedly accepted", len(cursor))
		}
	}
	delete(data, "cursor")
	data["unexpected"] = true
	if err := schema.Validate(request); err == nil {
		t.Fatal("unknown request data property unexpectedly accepted")
	}
}

func TestOwnedMappingsRequestSchemaRequiresMappingEnvelope(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.OwnedMappingsRequestSchemaID]
	request := ownedMappingsRequestFixture(t)
	request["causation_id"] = testMappingID
	if err := schema.Validate(request); err != nil {
		t.Fatalf("map ID in common causation union rejected: %v", err)
	}

	for _, field := range []string{"id", "schema", "emitted_at", "correlation_id", "data"} {
		invalid := ownedMappingsRequestFixture(t)
		delete(invalid, field)
		if err := schema.Validate(invalid); err == nil {
			t.Errorf("request without %s unexpectedly accepted", field)
		}
	}
	request["id"] = "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	if err := schema.Validate(request); err == nil {
		t.Fatal("non-map request ID unexpectedly accepted")
	}
}

func TestOwnedMappingsResponseSchemaEnforcesItemBoundsAndShape(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.OwnedMappingsResponseSchemaID]
	for _, count := range []int{0, 200, 201} {
		response := ownedMappingsAcceptedFixture(t)
		response["data"].(map[string]any)["items"] = ownedMappingItems(count)
		err := schema.Validate(response)
		if count <= 200 && err != nil {
			t.Fatalf("%d items rejected: %v", count, err)
		}
		if count == 201 && err == nil {
			t.Fatal("201 items unexpectedly accepted")
		}
	}

	response := ownedMappingsAcceptedFixture(t)
	data := response["data"].(map[string]any)
	data["next_cursor"] = strings.Repeat("a", 2048)
	if err := schema.Validate(response); err != nil {
		t.Fatalf("accepted page with maximum cursor rejected: %v", err)
	}
	data["next_cursor"] = ""
	if err := schema.Validate(response); err == nil {
		t.Fatal("empty next cursor unexpectedly accepted")
	}
	for _, cursor := range []string{strings.Repeat("a", 2049), strings.Repeat("é", 2048)} {
		data["next_cursor"] = cursor
		if err := schema.Validate(response); err == nil {
			t.Fatalf("%d-byte next cursor unexpectedly accepted", len(cursor))
		}
	}

	response = ownedMappingsAcceptedFixture(t)
	item := response["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
	delete(item, "entity_key")
	if err := schema.Validate(response); err == nil {
		t.Fatal("mapping without entity_key unexpectedly accepted")
	}
	item["entity_key"] = "power"
	item["external_id"] = "not part of this read"
	if err := schema.Validate(response); err == nil {
		t.Fatal("unknown mapping property unexpectedly accepted")
	}
}

func TestOwnedMappingsResponseSchemaSeparatesAcceptedAndRejectedData(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.OwnedMappingsResponseSchemaID]

	for _, code := range []string{"runtime_fenced", "invalid_cursor"} {
		response := ownedMappingsRejectedFixture(t, code)
		if err := schema.Validate(response); err != nil {
			t.Errorf("%s rejection rejected: %v", code, err)
		}
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"accepted without items", func(data map[string]any) { delete(data, "items") }},
		{"accepted with error", func(data map[string]any) {
			data["error"] = map[string]any{"code": "runtime_fenced", "message": "runtime fenced"}
		}},
		{"rejected with items", func(data map[string]any) { data["items"] = []any{} }},
		{"rejected with cursor", func(data map[string]any) { data["next_cursor"] = "cursor" }},
		{"unsupported rejection", func(data map[string]any) {
			data["error"].(map[string]any)["code"] = "repository_failed"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var response map[string]any
			if strings.HasPrefix(test.name, "accepted") {
				response = ownedMappingsAcceptedFixture(t)
			} else {
				response = ownedMappingsRejectedFixture(t, "invalid_cursor")
			}
			test.mutate(response["data"].(map[string]any))
			if err := schema.Validate(response); err == nil {
				t.Fatal("invalid response unexpectedly accepted")
			}
		})
	}
}

func TestOwnedMappingsResponseSchemaRequiresMappingCausation(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.OwnedMappingsResponseSchemaID]
	response := ownedMappingsAcceptedFixture(t)
	if err := schema.Validate(response); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	delete(response, "causation_id")
	if err := schema.Validate(response); err == nil {
		t.Fatal("response without causation ID unexpectedly accepted")
	}
	response["causation_id"] = "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	if err := schema.Validate(response); err == nil {
		t.Fatal("response caused by non-map ID unexpectedly accepted")
	}
}

func ownedMappingsRequestFixture(t *testing.T) map[string]any {
	t.Helper()
	return decodeObject(t, `{
		"id":"`+testMappingID+`",
		"schema":"urn:hearth:schema:owned-mappings-request:v1",
		"emitted_at":"2026-09-01T12:00:00Z",
		"correlation_id":"`+testCorrelationID+`",
		"data":{}
	}`)
}

func ownedMappingsAcceptedFixture(t *testing.T) map[string]any {
	t.Helper()
	response := decodeObject(t, `{
		"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:owned-mappings-response:v1",
		"emitted_at":"2026-09-01T12:00:00Z",
		"correlation_id":"`+testCorrelationID+`",
		"causation_id":"`+testMappingID+`",
		"data":{"status":"accepted","items":[]}
	}`)
	response["data"].(map[string]any)["items"] = ownedMappingItems(1)
	return response
}

func ownedMappingsRejectedFixture(t *testing.T, code string) map[string]any {
	t.Helper()
	return decodeObject(t, `{
		"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:owned-mappings-response:v1",
		"emitted_at":"2026-09-01T12:00:00Z",
		"correlation_id":"`+testCorrelationID+`",
		"causation_id":"`+testMappingID+`",
		"data":{"status":"rejected","error":{"code":"`+code+`","message":"request rejected"}}
	}`)
}

func ownedMappingItems(count int) []any {
	items := make([]any, count)
	for index := range items {
		items[index] = map[string]any{
			"binding_key": fmt.Sprintf("binding-%d", index),
			"device_id":   fmt.Sprintf("dev_01890f47-7a6b-7c4d-8e9f-%012x", index+1),
			"entity_key":  fmt.Sprintf("entity-%d", index),
			"entity_id":   fmt.Sprintf("ent_01890f47-7a6b-7c4d-8e9f-%012x", index+1),
		}
	}
	return items
}
