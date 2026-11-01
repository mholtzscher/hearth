package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/httpapi"
)

//nolint:gocognit // Covers both write routes and their complete request-size boundary matrix.
func TestDashboardWriteRequestSizeBoundary(t *testing.T) {
	t.Parallel()
	const definition = `{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"One","spec":{"root":"a","elements":{"a":{"type":"Note","props":{"text":"hello"}}}}}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, size := range []int{65535, 65536, 65537} {
			t.Run(method+"/"+strconv.Itoa(size), func(t *testing.T) {
				t.Parallel()
				service := &boundaryDashboards{}
				router := echo.New()
				api := humaecho.New(router, huma.DefaultConfig("test", "1"))
				httpapi.Register(huma.NewGroup(api, "/v1"), service, nil)
				body := `{"definition":` + definition + `}`
				path := "/v1/dashboards"
				status := http.StatusCreated
				if method == http.MethodPut {
					body = `{"expected_revision":1,"definition":` + definition + `}`
					path += "/dash_01900000-0000-7000-8000-000000000000"
					status = http.StatusOK
				}
				if size > 65536 {
					status = http.StatusRequestEntityTooLarge
				}
				body += strings.Repeat(" ", size-len(body))
				request := httptest.NewRequest(method, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != status {
					t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
				}
				if size <= 65536 && (service.definition == nil || service.definition.Title != "One") {
					t.Fatalf("service definition=%+v", service.definition)
				}
				if size > 65536 && service.definition != nil {
					t.Fatal("oversized request reached the service")
				}
			})
		}
	}
}

type boundaryDashboards struct {
	httpapi.Dashboards

	definition *dashboards.Definition
}

func (service *boundaryDashboards) CreateDashboard(
	_ context.Context, definition dashboards.Definition,
) (dashboards.Dashboard, error) {
	service.definition = &definition
	raw, err := json.Marshal(definition)
	return dashboards.Dashboard{Definition: raw}, err
}

func (service *boundaryDashboards) SaveDashboard(
	ctx context.Context, params dashboards.SaveDashboardParams,
) (dashboards.Dashboard, error) {
	return service.CreateDashboard(ctx, params.Definition)
}
