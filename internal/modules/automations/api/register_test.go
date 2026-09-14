package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func newAutomationHTTP(t *testing.T, stub *apiDevices) (*echo.Echo, huma.API, *automations.Service) {
	t.Helper()
	service := newAutomationService(t, stub)
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	group := huma.NewGroup(openapi, "/v1")
	automationsapi.Register(group, service)
	return router, openapi, service
}

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

func decodeAutomation(t *testing.T, response *httptest.ResponseRecorder) automationsapi.AutomationBody {
	t.Helper()
	var body automationsapi.AutomationBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode automation body %s: %v", response.Body.String(), err)
	}
	return body
}

// HTTP CRUD must enforce strict bodies and revision checks against real SQLite.
func TestAutomationAPIRevisionedCRUDAndStrictDTOs(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())

	create := performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 2))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", create.Code, create.Body.String())
	}
	created := decodeAutomation(t, create)
	if created.Revision != 1 || created.Definition.Name != "Office light" {
		t.Fatalf("created automation = %#v", created)
	}
	if location := create.Header().Get("Location"); location != "/v1/automations/"+created.ID {
		t.Fatalf("create Location = %q", location)
	}

	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{"unknown field", `{"name":"x","enabled":true,"triggers":[],"steps":[],"extra":1}`, http.StatusBadRequest},
		{
			"omitted enabled",
			strings.Replace(definitionDocument(t, 1), `"enabled": true,`, "", 1),
			http.StatusBadRequest,
		},
		{"typed family mismatch", `{"name":"x","enabled":true,"triggers":[{"id":"t","kind":"observation","entity_id":"e","event_name":"press"}],"steps":[]}`, http.StatusBadRequest},
		{"invalid json", `{"name":`, http.StatusUnprocessableEntity},
	} {
		response := performJSON(router, http.MethodPost, "/v1/automations", test.body)
		if response.Code != test.status {
			t.Fatalf("%s status = %d, want %d: %s", test.name, response.Code, test.status, response.Body.String())
		}
	}

	if response := performJSON(
		router, http.MethodGet, "/v1/automations/not-an-id", "",
	); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed ID status = %d, want 400", response.Code)
	}
	if response := performJSON(
		router,
		http.MethodGet,
		"/v1/automations/aut_00000000-0000-7000-8000-000000000000",
		"",
	); response.Code != http.StatusNotFound {
		t.Fatalf("unknown ID status = %d, want 404", response.Code)
	}

	stale := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID,
		fmt.Sprintf(`{"expected_revision":7,"definition":%s}`, definitionDocument(t, 1)))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale replace status = %d, want 409: %s", stale.Code, stale.Body.String())
	}
	unknownField := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID,
		fmt.Sprintf(`{"expected_revision":1,"definition":%s,"unexpected":true}`, definitionDocument(t, 1)))
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("replace with unknown envelope field status = %d, want 400", unknownField.Code)
	}
	replaced := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID,
		fmt.Sprintf(`{"expected_revision":1,"definition":%s}`, definitionDocument(t, 1)))
	if replaced.Code != http.StatusOK {
		t.Fatalf("replace status = %d, body %s", replaced.Code, replaced.Body.String())
	}
	if updated := decodeAutomation(t, replaced); updated.Revision != 2 || len(updated.Definition.Steps) != 1 {
		t.Fatalf("replaced automation = %#v", updated)
	}

	if response := performJSON(
		router,
		http.MethodDelete,
		"/v1/automations/"+created.ID,
		"",
	); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete without expected_revision status = %d, want 422", response.Code)
	}
	if response := performJSON(
		router, http.MethodDelete, "/v1/automations/"+created.ID+"?expected_revision=1", "",
	); response.Code != http.StatusConflict {
		t.Fatalf("stale delete status = %d, want 409", response.Code)
	}
	if response := performJSON(
		router, http.MethodDelete, "/v1/automations/"+created.ID+"?expected_revision=2", "",
	); response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", response.Code)
	}
	if response := performJSON(
		router,
		http.MethodGet,
		"/v1/automations/"+created.ID,
		"",
	); response.Code != http.StatusNotFound {
		t.Fatalf("read after delete status = %d, want 404", response.Code)
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

// OpenAPI must document PUT's strict {expected_revision, definition} envelope
// while create continues to accept a bare definition.
func TestAutomationAPIReplacementRequestEnvelopeDocument(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := openAutomationAPIDocument(t, openapi)

	replaceRef := requestBodyRef(t, document, "/v1/automations/{automation_id}", "put")
	if replaceRef != "#/components/schemas/AutomationReplacement" {
		t.Fatalf("replacement request body schema = %q, want the replacement envelope", replaceRef)
	}
	createRef := requestBodyRef(t, document, "/v1/automations", "post")
	if createRef != "#/components/schemas/AutomationDefinition" {
		t.Fatalf("create request body schema = %q, want AutomationDefinition", createRef)
	}
	assertReplacementEnvelopeSchema(t, document.Components.Schemas["AutomationReplacement"])
}

// Replacement must accept the documented envelope and reject bare or revisionless bodies.
func TestAutomationAPIReplacementEnvelopeIsAccepted(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())
	created := decodeAutomation(t, performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1)))

	advertised := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID,
		fmt.Sprintf(`{"expected_revision":%d,"definition":%s}`, created.Revision, definitionDocument(t, 1)))
	if advertised.Code != http.StatusOK {
		t.Fatalf("advertised envelope status = %d, want 200: %s", advertised.Code, advertised.Body.String())
	}
	bare := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID, definitionDocument(t, 1))
	if bare.Code != http.StatusBadRequest {
		t.Fatalf("bare definition replacement status = %d, want 400: %s", bare.Code, bare.Body.String())
	}
	missing := performJSON(router, http.MethodPut, "/v1/automations/"+created.ID,
		fmt.Sprintf(`{"definition":%s}`, definitionDocument(t, 1)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing expected_revision status = %d, want 400: %s", missing.Code, missing.Body.String())
	}
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

// HTTP keyset pages must advance without overlap and reproduce the ordered set.
func TestAutomationAPIListIsKeysetStable(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())
	var created []string
	for index := range 3 {
		response := performJSON(router, http.MethodPost, "/v1/automations",
			strings.Replace(definitionDocument(t, 1), "Office light", fmt.Sprintf("Automation %d", index), 1))
		if response.Code != http.StatusCreated {
			t.Fatalf("create %d status = %d: %s", index, response.Code, response.Body.String())
		}
		created = append(created, decodeAutomation(t, response).ID)
	}

	var paged []string
	cursor := ""
	for range 10 {
		path := "/v1/automations?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		response := performJSON(router, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
		}
		var body automationsapi.AutomationCollectionBody
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, item := range body.Items {
			paged = append(paged, item.ID)
		}
		if body.NextCursor == nil {
			break
		}
		cursor = *body.NextCursor
	}
	if len(paged) != 3 {
		t.Fatalf("paged IDs = %#v, want 3", paged)
	}
	for index := range created {
		if paged[index] != created[index] {
			t.Fatalf("paged[%d] = %s, want %s", index, paged[index], created[index])
		}
	}
}

// Manual start returns 202 with a history Location, 409 when busy, and 503 when closed.
func TestAutomationAPIManualRunGates(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, service := newAutomationHTTP(t, stub)

	create := performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1))
	created := decodeAutomation(t, create)

	response := performJSON(router, http.MethodPost, "/v1/automations/"+created.ID+"/runs", "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("manual run status = %d: %s", response.Code, response.Body.String())
	}
	var run automationsapi.AutomationRunBody
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	wantLocation := "/v1/automations/" + created.ID + "/history/" + run.ID
	if location := response.Header().Get("Location"); location != wantLocation {
		t.Fatalf("run Location = %q, want %q", location, wantLocation)
	}
	waitForAPI(t, service, created.ID, run.ID)

	if response = performJSON(
		router, http.MethodGet, "/v1/automations/"+created.ID+"/history/"+run.ID, "",
	); response.Code != http.StatusOK {
		t.Fatalf("history entry status = %d: %s", response.Code, response.Body.String())
	}

	// A running Run makes the next manual start busy.
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	stub.block = gate
	stub.onExecute = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	if response = performJSON(
		router,
		http.MethodPost,
		"/v1/automations/"+created.ID+"/runs",
		"",
	); response.Code != http.StatusAccepted {
		t.Fatalf("second manual run status = %d: %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	<-started
	if response = performJSON(
		router,
		http.MethodPost,
		"/v1/automations/"+created.ID+"/runs",
		"",
	); response.Code != http.StatusConflict {
		t.Fatalf("busy manual run status = %d, want 409: %s", response.Code, response.Body.String())
	}
	service.StopAdmission()
	if response = performJSON(
		router,
		http.MethodPost,
		"/v1/automations/"+created.ID+"/runs",
		"",
	); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed admission status = %d, want 503: %s", response.Code, response.Body.String())
	}
	close(gate)
	waitForAPI(t, service, created.ID, run.ID)
}

// Deleted Automations retain queryable history; a mismatched parent must return 404.
func TestAutomationAPIHistoryRemainsQueryableAfterDeletion(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	created := decodeAutomation(t, performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1)))

	runResponse := performJSON(router, http.MethodPost, "/v1/automations/"+created.ID+"/runs", "")
	var run automationsapi.AutomationRunBody
	if err := json.Unmarshal(runResponse.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	waitForAPI(t, service, created.ID, run.ID)
	if response := performJSON(
		router, http.MethodDelete, "/v1/automations/"+created.ID+"?expected_revision=1", "",
	); response.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.Code)
	}
	history := performJSON(router, http.MethodGet, "/v1/automations/"+created.ID+"/history", "")
	if history.Code != http.StatusOK {
		t.Fatalf("history after deletion status = %d: %s", history.Code, history.Body.String())
	}
	var body automationsapi.AutomationHistoryCollectionBody
	if err := json.Unmarshal(history.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Status != string(automations.RunSucceeded) {
		t.Fatalf("retained history = %#v", body.Items)
	}
	if response := performJSON(
		router, http.MethodGet, "/v1/automations/"+created.ID+"/history/"+run.ID, "",
	); response.Code != http.StatusOK {
		t.Fatalf("retained entry status = %d", response.Code)
	}
	other := decodeAutomation(t, performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1)))
	if response := performJSON(
		router, http.MethodGet, "/v1/automations/"+other.ID+"/history/"+run.ID, "",
	); response.Code != http.StatusNotFound {
		t.Fatalf("parent mismatch status = %d, want 404", response.Code)
	}
}

// OpenAPI must preserve operation IDs and tags without exposing reserved Command identities.
func TestAutomationAPIOperationIDsAndTags(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	operations := map[string]*huma.Operation{}
	for _, item := range openapi.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{
			item.Get, item.Put, item.Post, item.Delete, item.Patch, item.Head, item.Options, item.Trace,
		} {
			if operation != nil {
				operations[operation.OperationID] = operation
			}
		}
	}
	want := []string{
		"create-automation", "list-automations", "get-automation", "replace-automation",
		"delete-automation", "start-automation-run", "list-automation-history", "get-automation-history-entry",
	}
	for _, id := range want {
		operation, found := operations[id]
		if !found {
			t.Fatalf("operation %q is not registered", id)
		}
		if len(operation.Tags) != 1 || operation.Tags[0] != "Automations" {
			t.Fatalf("operation %q tags = %#v", id, operation.Tags)
		}
	}
	definition, found := openapi.OpenAPI().Components.Schemas.Map()["AutomationDefinition"]
	if !found {
		t.Fatal("AutomationDefinition component is not published")
	}
	if definition.Extensions["additionalProperties"] != false {
		t.Fatalf("definition component is not strict: %#v", definition.Extensions["additionalProperties"])
	}
	stepSchema, found := openapi.OpenAPI().Components.Schemas.Map()["AutomationStepAttemptBody"]
	if !found {
		t.Fatal("AutomationStepAttemptBody schema is not published")
	}
	for _, reserved := range []string{"reserved_command_id", "reserved_correlation_id"} {
		if _, exposed := stepSchema.Properties[reserved]; exposed {
			t.Fatalf("response schema exposes %q", reserved)
		}
	}
	if _, verified := stepSchema.Properties["verified_command_id"]; !verified {
		t.Fatal("response schema is missing verified_command_id")
	}
}
