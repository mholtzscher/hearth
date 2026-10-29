//go:build integration

package automations_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	automationshttp "github.com/mholtzscher/hearth/internal/modules/automations/httpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestManualHTTPConditionCorruptionKeepsOneDiagnosticOwner(t *testing.T) {
	t.Parallel()
	writer, logger := newAutomationLogSink()
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, dependencies)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, newEntityID(t), admissionConditionTree(newEntityID(t), "30"),
	))
	scripted.setEntityStateSnapshotError(devices.ErrEntityStateSnapshotCorrupt)
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Owner evidence", "1"))
	automationshttp.Register(api, service, logger)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/automations/"+string(record.ID)+"/runs", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	errors := 0
	for _, entry := range writer.records(t) {
		if entry["level"] == "ERROR" {
			errors++
			if entry["event"] != "automation.condition_state_corrupt" {
				t.Errorf("unexpected owner: %v", entry)
			}
		}
	}
	if errors != 1 {
		t.Fatalf("Error diagnostics=%d, want one service-owned record: %s", errors, writer.output())
	}
}
