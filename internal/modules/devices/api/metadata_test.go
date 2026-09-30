package api //nolint:testpackage // HTTP integration tests reuse real SQLite transport fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func metadataRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
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

func TestMetadataHTTPStorageFailuresRollbackAndHideDetails(t *testing.T) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	before, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"devices", "entities"} {
		if _, triggerErr := fixture.database.Exec(
			"CREATE TRIGGER fail_" + table + " BEFORE UPDATE OF name_override ON " + table + " BEGIN SELECT RAISE(ABORT, 'private SQLite diagnostic'); END",
		); triggerErr != nil {
			t.Fatal(triggerErr)
		}
	}
	for _, path := range []string{"/v1/devices/" + string(before.Entity.DeviceID), "/v1/entities/" + string(fixture.power)} {
		body := `{"name_edit":{"override":"Must roll back"}}`
		if path == "/v1/entities/"+string(fixture.power) {
			body = `{"enabled":false,"name_edit":{"override":"Must roll back"}}`
		}
		response := metadataRequest(fixture.router, http.MethodPatch, path, body)
		if response.Code != http.StatusInternalServerError ||
			bytes.Contains(response.Body.Bytes(), []byte("private SQLite diagnostic")) {
			t.Fatalf("storage failure = %d: %s", response.Code, response.Body.String())
		}
	}
	after, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed HTTP mixed patch mutated Entity = %#v, %v", after, err)
	}
	aggregate, err := fixture.service.GetDevice(
		context.Background(),
		devices.GetDeviceParams{ID: before.Entity.DeviceID, EntityLimit: 100},
	)
	if err != nil || aggregate.Device.NameOverride != nil || aggregate.Device.Name != "Office buttons" {
		t.Fatalf("failed HTTP patch mutated Device = %#v, %v", aggregate, err)
	}
}

func TestMetadataHTTPRejectsMalformedPatchesWithoutMutation(t *testing.T) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	view, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil {
		t.Fatal(err)
	}
	entityPath := "/v1/entities/" + string(fixture.power)
	devicePath := "/v1/devices/" + string(view.Entity.DeviceID)
	for _, path := range []string{entityPath, devicePath} {
		for _, test := range []struct {
			body   string
			status int
		}{
			{`{}`, 400}, {`{"name_edit":null}`, 422}, {`{"name_edit":{}}`, 422},
			{`{"name_edit":{"override":false}}`, 422},
			{`{"name_edit":{"override":"   "}}`, 400},
			{`{"name_edit":{"override":"Kitchen\n"}}`, 400},
		} {
			response := metadataRequest(fixture.router, http.MethodPatch, path, test.body)
			if response.Code != test.status {
				t.Fatalf("%s %s = %d: %s", path, test.body, response.Code, response.Body.String())
			}
		}
	}
	for _, body := range []string{`{"enabled":null}`, `{"enabled":false,"name_edit":null}`, `{"enabled":false,"name_edit":{"override":" "}}`} {
		response := metadataRequest(fixture.router, http.MethodPatch, entityPath, body)
		if response.Code != 400 && response.Code != 422 {
			t.Fatalf("malformed mixed patch = %d: %s", response.Code, response.Body.String())
		}
	}
	after, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil || !reflect.DeepEqual(view, after) {
		t.Fatalf("malformed requests mutated Entity: %#v, %v", after, err)
	}
	for _, path := range []string{"/v1/entities/bad", "/v1/devices/bad"} {
		if response := metadataRequest(
			fixture.router,
			http.MethodPatch,
			path,
			`{"name_edit":{"override":null}}`,
		); response.Code != 400 {
			t.Fatalf("invalid ID = %d", response.Code)
		}
	}
	for _, path := range []string{"/v1/entities/" + string(apiEntityID), "/v1/devices/" + string(apiDeviceID)} {
		if response := metadataRequest(
			fixture.router,
			http.MethodPatch,
			path,
			`{"name_edit":{"override":null}}`,
		); response.Code != 404 {
			t.Fatalf("unknown ID = %d: %s", response.Code, response.Body.String())
		}
	}
}

func TestMetadataHTTPAndMCPReadsUseCurrentNaming(
	t *testing.T,
) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	view, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil {
		t.Fatal(err)
	}
	entityPath := "/v1/entities/" + string(fixture.power)
	devicePath := "/v1/devices/" + string(view.Entity.DeviceID)
	response := metadataRequest(
		fixture.router,
		http.MethodPatch,
		entityPath,
		`{"enabled":false,"name_edit":{"override":"  Kitchen  ceiling  "}}`,
	)
	if response.Code != 200 {
		t.Fatalf("mixed patch = %d: %s", response.Code, response.Body.String())
	}
	var entity EntityBody
	if err = json.Unmarshal(response.Body.Bytes(), &entity); err != nil {
		t.Fatal(err)
	}
	if entity.Enabled || entity.Name != "Kitchen  ceiling" || entity.AdapterName != "Power" ||
		entity.NameOverride == nil ||
		*entity.NameOverride != entity.Name {
		t.Fatalf("mixed PATCH body = %#v", entity)
	}
	duplicate := metadataRequest(
		fixture.router,
		http.MethodPatch,
		"/v1/entities/"+string(fixture.buttons),
		`{"name_edit":{"override":"Kitchen  ceiling"}}`,
	)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate Entity name = %d: %s", duplicate.Code, duplicate.Body.String())
	}
	response = metadataRequest(
		fixture.router,
		http.MethodPatch,
		devicePath,
		`{"name_edit":{"override":"Kitchen  ceiling"}}`,
	)
	if response.Code != 200 {
		t.Fatalf("Device patch = %d: %s", response.Code, response.Body.String())
	}
	var device DeviceBody
	if err = json.Unmarshal(response.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	if device.Name != entity.Name || device.AdapterName != "Office buttons" || device.NameOverride == nil {
		t.Fatalf("Device PATCH body = %#v", device)
	}
	// Read all HTTP and MCP list/detail paths after the same mutation.
	assertMetadataReadParity(t, fixture, entityPath, devicePath, view.Entity.DeviceID)
	for _, path := range []string{entityPath, devicePath} {
		response = metadataRequest(fixture.router, http.MethodPatch, path, `{"name_edit":{"override":null}}`)
		if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"name_override":null`)) {
			t.Fatalf("reset = %d: %s", response.Code, response.Body.String())
		}
	}
	after, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil || after.Entity.Enabled || after.Entity.Name != "Power" {
		t.Fatalf("name-only reset changed enablement = %#v, %v", after, err)
	}
}

func assertMetadataReadParity(
	t *testing.T,
	fixture entityEventAPIFixture,
	entityPath, devicePath string,
	deviceID devices.DeviceID,
) {
	t.Helper()
	for _, path := range []string{entityPath, devicePath, "/v1/entities", "/v1/devices"} {
		response := metadataRequest(fixture.router, http.MethodGet, path, "")
		if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"Kitchen  ceiling"`)) ||
			!bytes.Contains(response.Body.Bytes(), []byte(`"name_override":"Kitchen  ceiling"`)) ||
			!bytes.Contains(response.Body.Bytes(), []byte(`"adapter_name":`)) {
			t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body.String())
		}
	}
	session := mcpSession(t, fixture.service)
	for _, uri := range []string{"hearth://entity/" + string(fixture.power), "hearth://device/" + string(deviceID), "hearth://entities", "hearth://devices"} {
		result, readErr := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
		if readErr != nil {
			t.Fatal(readErr)
		}
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte("Kitchen  ceiling")) || !bytes.Contains(raw, []byte("adapter_name")) ||
			!bytes.Contains(raw, []byte("name_override")) {
			t.Fatalf("MCP %s = %s", uri, raw)
		}
	}
}
