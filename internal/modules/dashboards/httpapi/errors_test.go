package httpapi_test //nolint:cyclop // Exercises the complete published operation matrix.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/httpapi"
)

type failedDashboards struct{ httpapi.Dashboards }

func (failedDashboards) GetDashboard(context.Context, dashboards.DashboardID) (dashboards.Dashboard, error) {
	return dashboards.Dashboard{}, errors.New("private database secret")
}

//nolint:gocognit // Tests diagnostics and the published error matrix together.
func TestUnexpectedHTTPFailureIsDiagnosedWithoutLeaking(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("test", "1"))
	httpapi.Register(huma.NewGroup(api, "/v1"), failedDashboards{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	req := httptest.NewRequest(http.MethodGet, "/v1/dashboards/dash_01900000-0000-7000-8000-000000000000", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("response %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(logs.String(), "dashboard.http_internal_failure") ||
		!strings.Contains(logs.String(), "get-dashboard") ||
		strings.Contains(logs.String(), "private") {
		t.Fatalf("unsafe diagnostic: %s", logs.String())
	}
	for path, methods := range map[string][]string{"/v1/dashboards": {"get", "post"}, "/v1/dashboards/{id}": {"get", "put", "delete"}} {
		item := api.OpenAPI().Paths[path]
		for _, method := range methods {
			var op *huma.Operation
			switch method {
			case "get":
				op = item.Get
			case "post":
				op = item.Post
			case "put":
				op = item.Put
			case "delete":
				op = item.Delete
			}
			if op == nil || op.Responses["500"] == nil || op.Responses["400"] == nil || op.Responses["422"] == nil {
				t.Fatalf("missing %s %s errors: %+v", method, path, op)
			}
			if (method == "post" || method == "put") && op.Responses["413"] == nil {
				t.Fatalf("missing size response: %s", method)
			}
		}
	}
	for _, parameter := range api.OpenAPI().Paths["/v1/dashboards/{id}"].Delete.Parameters {
		if parameter.Name == "expected_revision" {
			if !parameter.Required || parameter.Schema.Type != "integer" {
				t.Fatalf("revision schema: %+v", parameter)
			}
			return
		}
	}
	t.Fatal("missing expected revision parameter")
}
