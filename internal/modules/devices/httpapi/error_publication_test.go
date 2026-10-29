package httpapi //nolint:testpackage // Module fixtures drive real Huma validation and publication.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestDeviceFrameworkProblemsMatchPublishedSchemas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, method, path, schemaPath, media, body string
		status                                      int
	}{
		{"Device limit", "GET", "/v1/devices?limit=0", "/v1/devices", "", "", 422},
		{"Entity limit", "GET", "/v1/entities?limit=0", "/v1/entities", "", "", 422},
		{"Command limit", "GET", "/v1/commands?limit=0", "/v1/commands", "", "", 422},
		{"Device size", "PATCH", "/v1/devices/" + string(apiDeviceID), "/v1/devices/{device_id}", mergePatchContentType, strings.Repeat(" ", 1024*1024+1), 413},
		{"Entity size", "PATCH", "/v1/entities/" + string(apiEntityID), "/v1/entities/{entity_id}", mergePatchContentType, strings.Repeat(" ", 1024*1024+1), 413},
		{"Command size", "POST", "/v1/entities/" + string(apiEntityID) + "/commands", "/v1/entities/{entity_id}/commands", "application/json", strings.Repeat(" ", 1024*1024+1), 413},
		{"Command media", "POST", "/v1/entities/" + string(apiEntityID) + "/commands", "/v1/entities/{entity_id}/commands", "text/plain", "{}", 415},
		{"Command JSON", "POST", "/v1/entities/" + string(apiEntityID) + "/commands", "/v1/entities/{entity_id}/commands", "application/json", "{", 400},
		{"Device media", "PATCH", "/v1/devices/" + string(apiDeviceID), "/v1/devices/{device_id}", "application/json", "{}", 415},
		{"Device JSON", "PATCH", "/v1/devices/" + string(apiDeviceID), "/v1/devices/{device_id}", mergePatchContentType, "{", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// Any service call panics: input must be rejected before dispatch.
			router, api := testAPI(t, &stubDevices{})
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.media)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			body, err := jsonschema.UnmarshalJSON(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			schema := deviceErrorSchema(t, api, test.schemaPath, test.method, test.status)
			if err = schema.Validate(body); err != nil {
				t.Fatalf("problem violates publication: %v", err)
			}
		})
	}
}

func deviceErrorSchema(t *testing.T, api huma.API, path, method string, status int) *jsonschema.Schema {
	t.Helper()
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://hearth.invalid/devices"
	if err = compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(fmt.Sprintf("%s#/paths/%s/%s/responses/%d/content/application~1problem+json/schema",
		location, strings.ReplaceAll(path, "/", "~1"), strings.ToLower(method), status))
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
