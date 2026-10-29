package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/httpapi"
)

// httpScenario keeps recursive contract scenarios entirely at the HTTP boundary.
type httpScenario struct {
	router http.Handler
	api    huma.API
}
type httpScenarioResult struct {
	Failed  bool
	Body    any
	RawBody string
}
type httpOperationContract struct {
	Name             string
	DefinitionSchema any
	ResponseSchema   any
}
type httpOperationCatalog struct{ Operations []*httpOperationContract }

func newHTTPScenario(t *testing.T, service automationsapi.Automations) *httpScenario {
	t.Helper()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	automationsapi.Register(huma.NewGroup(api, "/v1"), service, nil)
	return &httpScenario{router: router, api: api}
}

func (client *httpScenario) contracts() (*httpOperationCatalog, error) {
	raw, err := json.Marshal(client.api.OpenAPI())
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	catalog := &httpOperationCatalog{}
	for name, schema := range map[string]string{
		"create-automation": "AutomationBody", "replace-automation": "AutomationBody", "get-automation": "AutomationBody",
		"start-automation-run": "AutomationRunBody", "get-automation-history-entry": "AutomationHistoryEntryBody",
		"list-automation-history": "AutomationHistoryCollectionBody",
	} {
		catalog.Operations = append(
			catalog.Operations,
			&httpOperationContract{
				Name:             name,
				DefinitionSchema: schemas["AutomationDefinition"],
				ResponseSchema:   schemas[schema],
			},
		)
	}
	return catalog, nil
}

func callHTTPScenario(t *testing.T, client *httpScenario, operation string, arguments any) *httpScenarioResult {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]json.RawMessage
	if err = json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	var id, entry string
	_ = json.Unmarshal(args["automation_id"], &id)
	_ = json.Unmarshal(args["entry_id"], &entry)
	path, method, body := "/v1/automations", http.MethodGet, ""
	switch operation {
	case "create-automation":
		method, body = http.MethodPost, string(args["definition"])
	case "replace-automation":
		method, path, body = http.MethodPut, path+"/"+id, fmt.Sprintf(
			`{"expected_revision":%s,"definition":%s}`,
			args["expected_revision"],
			args["definition"],
		)
	case "get-automation":
		path += "/" + id
	case "start-automation-run":
		method, path, body = http.MethodPost, path+"/"+id+"/runs", "{}"
		if bypass, ok := args["bypass_conditions"]; ok {
			body = `{"bypass_conditions":` + string(bypass) + `}`
		}
	case "get-automation-history-entry":
		path += "/" + id + "/history/" + entry
	case "list-automation-history":
		path += "/" + id + "/history"
	case "list-automations":
	default:
		t.Fatalf("unknown HTTP scenario operation %s", operation)
	}
	var query []string
	for _, key := range []string{"limit", "cursor"} {
		if value, ok := args[key]; ok {
			query = append(query, key+"="+strings.Trim(string(value), `"`))
		}
	}
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	response := performJSON(client.router, method, path, body)
	var value any
	if err = json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if object, ok := value.(map[string]any); ok {
		delete(object, "$schema")
	}
	// Keep the original response bytes for exact numbers, but remove Huma's schema link.
	var exact map[string]json.RawMessage
	if err = json.Unmarshal(response.Body.Bytes(), &exact); err != nil {
		t.Fatal(err)
	}
	delete(exact, "$schema")
	encoded, err := json.Marshal(exact)
	if err != nil {
		t.Fatal(err)
	}
	return &httpScenarioResult{Failed: response.Code >= 400, Body: value, RawBody: string(encoded)}
}

func httpScenarioErrorText(t *testing.T, result *httpScenarioResult) string {
	t.Helper()
	if !result.Failed {
		t.Fatalf("expected HTTP failure: %v", result.Body)
	}
	problem := result.Body.(map[string]any)
	code, _ := problem["code"].(string)
	return code + ": " + fmt.Sprint(problem["detail"])
}
func decodeHTTPScenarioInto[T any](t *testing.T, result *httpScenarioResult) T {
	t.Helper()
	raw, err := json.Marshal(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
