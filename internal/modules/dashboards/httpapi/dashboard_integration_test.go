//go:build integration

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/httpapi"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func TestDashboardHTTP(t *testing.T) {
	t.Parallel()
	db := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "http.db"))
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("test", "1"))
	httpapi.Register(huma.NewGroup(api, "/v1"), dashboards.NewService(sqlite.NewRepository(db), nil), nil)
	body := []byte(
		`{"definition":{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"One","spec":{"root":"a","elements":{"a":{"type":"Note","props":{"text":"hello"}}}}}}`,
	)
	req := httptest.NewRequest(http.MethodPost, "/v1/dashboards", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != 201 {
		t.Fatalf("create %d %s", response.Code, response.Body.String())
	}
	var d dashboards.Dashboard
	if err := json.Unmarshal(response.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/dashboards/"+string(d.ID), nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("get %d %s", response.Code, response.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/v1/dashboards/"+string(d.ID)+"?expected_revision=2", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != 409 {
		t.Fatalf("conflict %d %s", response.Code, response.Body.String())
	}
	for _, target := range []string{"/v1/dashboards?limit=bad", "/v1/dashboards?limit=201"} {
		req = httptest.NewRequest(http.MethodGet, target, nil)
		response = httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid query %d %s", response.Code, response.Body.String())
		}
	}
	req = httptest.NewRequest(http.MethodDelete, "/v1/dashboards/"+string(d.ID)+"?expected_revision=bad", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision %d %s", response.Code, response.Body.String())
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"definition":{}}`, http.StatusBadRequest},
		{`{"definition":` + strings.Repeat(" ", 65536) + `{}}`, http.StatusRequestEntityTooLarge},
	} {
		req = httptest.NewRequest(http.MethodPost, "/v1/dashboards", strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("status %d, want %d: %s", response.Code, test.status, response.Body.String())
		}
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"definition":{}}`, http.StatusBadRequest},
		{`{"definition":` + string(d.Definition) + `}`, http.StatusBadRequest},
		{`{"expected_revision":0,"definition":` + string(d.Definition) + `}`, http.StatusBadRequest},
		{`{"expected_revision":1,"definition":` + string(d.Definition) + `,"unknown":true}`, http.StatusBadRequest},
		{`{"expected_revision":1,"definition":` + strings.Repeat(" ", 65536) + `{}}`, http.StatusRequestEntityTooLarge},
	} {
		req = httptest.NewRequest(http.MethodPut, "/v1/dashboards/"+string(d.ID), strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		response = httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("put status %d, want %d: %s", response.Code, test.status, response.Body.String())
		}
	}
	validReplacement := `{"expected_revision":1,"definition":` + string(d.Definition) + `}`
	req = httptest.NewRequest(http.MethodPut, "/v1/dashboards/"+string(d.ID), strings.NewReader(validReplacement))
	req.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("valid put %d %s", response.Code, response.Body.String())
	}
	if _, err := db.Exec(
		`UPDATE dashboards SET definition_json=? WHERE id=?`,
		`{"schema_version":1,"catalog_version":"future","title":123,"spec":{}}`,
		string(d.ID),
	); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/dashboards/"+string(d.ID), nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("stored corruption %d %s", response.Code, response.Body.String())
	}
}
