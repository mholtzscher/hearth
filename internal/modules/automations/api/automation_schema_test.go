package api //nolint:testpackage // Exercises unsupported JSON Schema keywords at the runtime publication boundary.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func decodeSchemaDocument(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}
func TestAutomationSchemaPublicationPreservesLocalReferences(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/automation-schema-local-refs.json")
	if err != nil {
		t.Fatal(err)
	}
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Schema publication", "1"))
	codec, err := automations.NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	Register(huma.NewGroup(api, "/v1"), nil, codec)
	publishAutomationDefinitionSchema(api, raw)
	response := automationRequest(router, http.MethodGet, "/openapi.json", "", "")
	requireAutomationStatus(t, response, http.StatusOK)
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	published := document.Components.Schemas["AutomationDefinition"]
	if !reflect.DeepEqual(decodeSchemaDocument(t, raw), decodeSchemaDocument(t, published)) {
		t.Fatalf("OpenAPI dropped or rounded canonical keywords: %s", published)
	}
}
