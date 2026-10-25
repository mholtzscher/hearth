package httpapi //nolint:testpackage // Exercise transport envelope guards and canonical schema relocation independently of persistence.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type valueContractService struct {
	Automations

	writes int
}

func (service *valueContractService) CreateAutomation(
	_ context.Context,
	definition automations.Definition,
) (automations.Record, error) {
	service.writes++
	return automations.Record{
		ID:         "aut_01980000-0000-7000-8000-000000000001",
		Revision:   1,
		Definition: definition,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}, nil
}

func TestValueBindingsHTTPV3OnlySafeDiagnosticsAndEnvelopeLimit(t *testing.T) {
	t.Parallel()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Values", "test"))
	service := &valueContractService{}
	Register(api, service)
	const prefix = `{"name":"Values","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[`
	for _, test := range []struct {
		step   string
		status int
		path   string
	}{
		{`{"id":"set","kind":"set_variables","assignments":{"x":{"literal":9007199254740993}}}`, 201, ""},
		{`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`, 201, ""},
		{`{"id":"wait","kind":"delay","duration_ms":1}`, 400, ""},
		{`{"id":"wait","kind":"delay","duration_ms":{"expr":"1"}}`, 400, ""},
		{`{"id":"send","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001","title":{"literal":""},"message":{"expr":"let private_source = 'private_value'; private_source"}}`, 400, "/steps/0/message"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(prefix+test.step+`]}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("status %d want %d: %s", response.Code, test.status, response.Body.String())
		}
		if test.path != "" &&
			(!strings.Contains(response.Body.String(), test.path) || strings.Contains(response.Body.String(), "private_source") || strings.Contains(response.Body.String(), "private_value")) {
			t.Fatalf("unsafe or unlocated diagnostics: %s", response.Body.String())
		}
	}
	for _, size := range []int{automationRequestMaxBytes + 1, automationRequestMaxBytes + 2} {
		request := httptest.NewRequest(http.MethodPost, "/automations", strings.NewReader(strings.Repeat(" ", size)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%d bytes: status %d", size, response.Code)
		}
	}
	if service.writes != 2 {
		t.Fatalf("invalid requests reached service: %d", service.writes)
	}
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "automation-definition:v2") ||
		!strings.Contains(string(raw), "automation-response:run:v3") {
		t.Fatal("schema refs did not move to v3")
	}
}
