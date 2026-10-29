//go:build integration

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestAutomationHTTPStoredCorruptionIsInternal(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		statement string
		paths     []string
	}{
		{"definition", `UPDATE automation_definition_revisions SET definition_json = '{"private_stored_field":"private_stored_value"}'`, []string{"", "/{id}", "/{id}/history/{entry}"}},
		{"lifecycle", `UPDATE automation_history SET run_status = 'private_stored_value'`, []string{"/{id}/history", "/{id}/history/{entry}"}},
		{"fact", `UPDATE automation_history SET run_source = 'device_fact', fact_id = 'private_stored_value', fact_family = 'observation', fact_entity_id = 'private_stored_value', fact_variant = 'applied', fact_causation_id = 'private_stored_value', fact_emitted_at = recorded_at, fact_value_json = 'true'`, []string{"/{id}/history", "/{id}/history/{entry}"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationTestDatabase(t)
			repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
			definition, err := automations.DecodeDefinition(json.RawMessage(definitionDocument(t, 1)))
			if err != nil {
				t.Fatal(err)
			}
			record, err := repository.CreateAutomation(t.Context(), definition)
			if err != nil {
				t.Fatal(err)
			}
			entry, err := repository.AdmitManualRun(
				t.Context(),
				automations.ManualRunInput{AutomationID: record.ID},
				devices.EntityStateSnapshot{},
				time.Now().UTC(),
			)
			if err != nil {
				t.Fatal(err)
			}
			run := entry.(automations.Run)
			client := newHTTPScenario(
				t,
				automations.NewService(repository, newAPIDevices(), automations.Dependencies{}),
			)
			// Only this disposable fixture bypasses write guards to model a damaged store.
			for _, statement := range []string{"PRAGMA ignore_check_constraints = ON", "DROP TRIGGER automation_definition_revision_immutable", test.statement} {
				if _, err = database.ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			for _, suffix := range test.paths {
				path := "/v1/automations" + strings.NewReplacer("{id}", string(record.ID), "{entry}", string(run.ID)).
					Replace(suffix)
				response := performJSON(client.router, http.MethodGet, path, "")
				assertStoredHTTPFailure(t, response)
			}
		})
	}
}

func assertStoredHTTPFailure(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(response.Body.String(), "private_stored") {
		t.Errorf("stored cause leaked into response: %s", response.Body.String())
	}
	if response.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: %s", response.Code, response.Body.String())
	}
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	delete(problem, "$schema")
	want := map[string]any{
		"type": "about:blank", "title": "Internal Server Error", "status": float64(500),
		"code": "internal_error", "detail": "internal error",
	}
	if !reflect.DeepEqual(problem, want) {
		t.Errorf("unsafe or incorrect problem: %s", response.Body.String())
	}
}
