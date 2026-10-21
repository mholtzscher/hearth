package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// restJSON reads one Huma route and returns its decoded JSON body, failing
// unless the route answered 200.
//
// Huma adds a $schema link to response bodies; comparisons here omit that link
// while metadata-specific tests assert schema publication separately.
func restJSON(t *testing.T, router http.Handler, path string) any {
	t.Helper()
	response := performJSON(router, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d: %s", path, response.Code, response.Body.String())
	}
	var body any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET %s body %s: %v", path, response.Body.String(), err)
	}
	if object, ok := body.(map[string]any); ok {
		delete(object, "$schema")
	}
	return body
}

// canonicalJSON sorts object keys when comparing decoded HTTP bodies.
func canonicalJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON value: %v", err)
	}
	return string(encoded)
}

// assertScenarioPage compares the scenario client's decoded HTTP page with a
// direct route response, including its continuation cursor.
func assertScenarioPage(t *testing.T, label string, tool *httpScenarioResult, route any) string {
	t.Helper()
	if tool.Failed {
		t.Fatalf("%s tool error = %q", label, httpScenarioErrorText(t, tool))
	}
	if got, want := canonicalJSON(t, tool.Body), canonicalJSON(t, route); got != want {
		t.Fatalf("%s tool body = %s, route body = %s, want equal", label, got, want)
	}
	routeCursor := pageCursor(t, route)
	if toolCursor := pageCursor(t, tool.Body); toolCursor != routeCursor {
		t.Fatalf(
			"%s tool next_cursor = %q, route next_cursor = %q, want equal",
			label, toolCursor, routeCursor,
		)
	}
	return routeCursor
}

// pageCursor returns one page's next_cursor, or "" when the page published none.
func pageCursor(t *testing.T, body any) string {
	t.Helper()
	cursor, _ := pageObject(t, body)["next_cursor"].(string)
	return cursor
}

// pageItemIDs returns one page's item IDs in order.
func pageItemIDs(t *testing.T, body any) []string {
	t.Helper()
	items, ok := pageObject(t, body)["items"].([]any)
	if !ok {
		t.Fatalf("page body = %#v, want an items array", body)
	}
	ids := make([]string, len(items))
	for index, item := range items {
		object, isObject := item.(map[string]any)
		if !isObject {
			t.Fatalf("page item %d = %#v, want a JSON object", index, item)
		}
		id, _ := object["id"].(string)
		if id == "" {
			t.Fatalf("page item %d = %#v, want an id", index, object)
		}
		ids[index] = id
	}
	return ids
}

func pageObject(t *testing.T, body any) map[string]any {
	t.Helper()
	object, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("page body = %#v, want a JSON object", body)
	}
	return object
}
