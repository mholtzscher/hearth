package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func performJSON(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeAutomation(t *testing.T, response *httptest.ResponseRecorder) AutomationBody {
	t.Helper()
	var body AutomationBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode automation body %s: %v", response.Body.String(), err)
	}
	return body
}

func assertHeldDefinition(t *testing.T, definition AutomationDefinitionBody) {
	t.Helper()
	if len(definition.Triggers) != 2 {
		t.Fatalf("held triggers = %#v", definition.Triggers)
	}
	boolean, numeric := definition.Triggers[0], definition.Triggers[1]
	if boolean.Kind != "held_state" || boolean.ForSeconds == nil || *boolean.ForSeconds != 60 ||
		len(boolean.Comparisons) != 1 || string(boolean.Comparisons[0].Operand) != "true" {
		t.Errorf("boolean held predicate = %#v", boolean)
	}
	if numeric.Kind != "held_state" || numeric.ForSeconds == nil || *numeric.ForSeconds != 3600 ||
		len(numeric.Comparisons) != 1 || string(numeric.Comparisons[0].Operand) != "21.5" {
		t.Errorf("numeric held predicate = %#v", numeric)
	}
}

// automationOpenAPIDocument is the published-document subset the replacement
// request-body tests inspect.
type automationOpenAPIDocument struct {
	Paths      map[string]map[string]automationOpenAPIOperation `json:"paths"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

type automationOpenAPIOperation struct {
	RequestBody automationOpenAPIRequestBody `json:"requestBody"`
}

type automationOpenAPIRequestBody struct {
	Required bool `json:"required"`
	Content  map[string]struct {
		Schema struct {
			Ref string `json:"$ref"`
		} `json:"schema"`
	} `json:"content"`
}

// openAutomationAPIDocument marshals the generated OpenAPI document, so
// assertions observe what callers actually receive after schema pruning.
func openAutomationAPIDocument(t *testing.T, openapi huma.API) automationOpenAPIDocument {
	t.Helper()
	raw, err := json.Marshal(openapi.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var document automationOpenAPIDocument
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

// requestBodyRef returns the JSON schema reference one operation publishes for
// its required application/json request body.
func requestBodyRef(t *testing.T, document automationOpenAPIDocument, path, method string) string {
	t.Helper()
	body := document.Paths[path][method].RequestBody
	if !body.Required {
		t.Fatalf("%s %s request body is not required", method, path)
	}
	ref := body.Content["application/json"].Schema.Ref
	if ref == "" {
		t.Fatalf("%s %s request body has no JSON schema reference", method, path)
	}
	return ref
}

// assertReplacementEnvelopeSchema checks required fields, revision bounds,
// unknown-field rejection, and the strict nested definition reference.
func assertReplacementEnvelopeSchema(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("AutomationReplacement component is not published")
	}
	var envelope struct {
		Type                 string                     `json:"type"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Type != "object" {
		t.Fatalf("replacement envelope type = %q, want object", envelope.Type)
	}
	if envelope.AdditionalProperties == nil || *envelope.AdditionalProperties {
		t.Fatalf("replacement envelope allows unknown members: %v", envelope.AdditionalProperties)
	}
	for _, member := range []string{"expected_revision", "definition"} {
		if !slices.Contains(envelope.Required, member) {
			t.Fatalf("replacement envelope does not require %q: %v", member, envelope.Required)
		}
	}
	var revision struct {
		Type    string   `json:"type"`
		Minimum *float64 `json:"minimum"`
	}
	if err := json.Unmarshal(envelope.Properties["expected_revision"], &revision); err != nil {
		t.Fatal(err)
	}
	if revision.Type != "integer" || revision.Minimum == nil || *revision.Minimum != 1 {
		t.Fatalf("expected_revision schema = %#v, want integer minimum 1", revision)
	}
	var nested struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(envelope.Properties["definition"], &nested); err != nil {
		t.Fatal(err)
	}
	if nested.Ref != "#/components/schemas/AutomationDefinition" {
		t.Fatalf("definition member schema = %q, want the strict definition component", nested.Ref)
	}
}
