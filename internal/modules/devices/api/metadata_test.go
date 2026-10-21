package api //nolint:testpackage // HTTP integration tests reuse real SQLite transport fixtures.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func metadataRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", mergePatchContentType)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestNamingResponseSchemasRequireNullableOverrides(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	response := performRequest(router, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI = %d", response.Code)
	}
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"EntityBody", "DeviceBody", "DeviceDetailBody"} {
		schema := decodeMetadataSchema(t, document.Components.Schemas[name])
		if !slices.Contains(schema.Required, "adapter_name") || !slices.Contains(schema.Required, "name_override") {
			t.Fatalf("%s required = %#v", name, schema.Required)
		}
		override := decodeMetadataSchema(t, schema.Properties["name_override"])
		var types []string
		if err := json.Unmarshal(override.Type, &types); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(types, "string") || !slices.Contains(types, "null") {
			t.Fatalf("%s name_override types = %#v", name, types)
		}
	}
}
