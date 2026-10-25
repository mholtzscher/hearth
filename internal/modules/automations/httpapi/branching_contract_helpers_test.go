package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const branchExactNumber = "9007199254740993"

func nestedBranchDocument(t *testing.T, entity devices.EntityID) string {
	t.Helper()
	command := fmt.Sprintf(
		`{"kind":"command", "id":"selected","entity_id":%q,"operation":"set","parameters":{"literal":{"value":%s}}}`,
		entity,
		branchExactNumber,
	)
	return fmt.Sprintf(
		`{"name":"Nested","enabled":true,"triggers":[{"id":"warm","kind":"cron","expression":"* * * * *"}],"steps":[
		{"id":"route","kind":"choose","branches":[
			{"id":"automatic","conditions":{"id":"matched","kind":"trigger","trigger_ids":["warm"]},"steps":[{"kind":"command", "id":"unselected","entity_id":%q,"operation":"set","parameters":{"literal":{}}}]},
			{"id":"manual","conditions":{"id":"not-matched","kind":"not","child":{"id":"matched","kind":"trigger","trigger_ids":["warm"]}},"steps":[
				{"id":"level","kind":"if","conditions":{"id":"exact","kind":"entity_state","entity_id":%q,"value_pointer":"","operator":"eq","operand":%s},"then":[%s]}]}
		]}]}`,
		entity,
		entity,
		branchExactNumber,
		command,
	)
}

// Decode the protocol's JSON text, not the SDK client's float64 StructuredContent.
func exactHTTPScenarioBody(t *testing.T, result *httpScenarioResult) map[string]any {
	t.Helper()
	if result.Failed {
		t.Fatal(httpScenarioErrorText(t, result))
	}
	return exactJSONObject(t, result.RawBody)
}

func exactJSONObject(t *testing.T, document string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewBufferString(document))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		t.Fatal(err)
	}
	delete(object, "$schema")
	return object
}

func assertNestedExactDefinition(t *testing.T, body map[string]any) {
	t.Helper()
	definition := body["definition"].(map[string]any)
	route := definition["steps"].([]any)[0].(map[string]any)
	branches := route["branches"].([]any)
	level := branches[1].(map[string]any)["steps"].([]any)[0].(map[string]any)
	condition := level["conditions"].(map[string]any)
	command := level["then"].([]any)[0].(map[string]any)
	parameters := command["parameters"].(map[string]any)["literal"].(map[string]any)
	if condition["operand"] != json.Number(branchExactNumber) || parameters["value"] != json.Number(branchExactNumber) {
		t.Fatalf("rounded operand/parameters: %v / %v", condition, parameters)
	}
	if command["kind"] != "command" {
		t.Fatalf("command kind = %v", command["kind"])
	}
	for _, field := range []string{"conditions", "then", "branches"} {
		if _, present := command[field]; present {
			t.Fatalf("command contains %s: %v", field, command)
		}
	}
	if _, present := level["else"]; present {
		t.Fatal("omitted else was emitted")
	}
	if _, present := route["default"]; present {
		t.Fatal("omitted default was emitted")
	}
	if _, present := route["entity_id"]; present {
		t.Fatal("branch contains command fields")
	}
}

func assertPublishedBranchSchema(t *testing.T, document map[string]any, uri, fragment, valid string) {
	t.Helper()
	schema := compilePublishedSchema(t, document, uri+fragment)
	if err := schema.Validate(exactJSONObject(t, valid)); err != nil {
		t.Fatalf("published schema rejects nested definition: %v", err)
	}
	bad := strings.Replace(valid, `"kind":"if"`, `"kind":"if","else":[]`, 1)
	if err := schema.Validate(exactJSONObject(t, bad)); err == nil {
		t.Fatal("published schema accepts empty recursive arm")
	}
	bad = strings.Replace(
		valid,
		`"steps":[`,
		`"conditions":{"id":"invalid","kind":"trigger","trigger_ids":["warm"]},"steps":[`,
		1,
	)
	if err := schema.Validate(exactJSONObject(t, bad)); err == nil {
		t.Fatal("published admission schema accepts trigger leaf")
	}
}
