//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

func TestAutomationHTTPServerPreconditionsAreInternal(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"devices", "notifications", "functions"} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(missing+"/"+method, func(t *testing.T) {
				t.Parallel()
				assertMissingValidationHTTP(t, missing, method)
			})
		}
	}
}

func assertMissingValidationHTTP(t *testing.T, missing, method string) {
	t.Helper()
	var validation automations.AutomationDevices = newAPIDevices()
	step := `{"id":"wait","kind":"delay","duration_ms":1}`
	switch missing {
	case "devices":
		validation = nil
	case "notifications":
		step = `{"id":"private-notify","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001","title":"","message":"private message"}`
	case "functions":
		step = `{"id":"private-function","kind":"function","adapter_id":"test-functions","function_key":"inspect","contract_fingerprint":"sha256-v1:` + strings.Repeat(
			"a",
			64,
		) + `","arguments":{}}`
	}
	definition := `{"name":"Private fixture","enabled":true,"triggers":[{"id":"tick","kind":"cron","expression":"* * * * *"}],"steps":[` + step + `]}`
	client := newHTTPScenario(
		t,
		automations.NewService(&preconditionWriteTrap{test: t}, validation, automations.Dependencies{}),
	)
	path, body := "/v1/automations", definition
	if method == http.MethodPut {
		path += "/aut_01980000-0000-7000-8000-000000000001"
		body = `{"expected_revision":1,"definition":` + definition + `}`
	}
	response := performJSON(client.router, method, path, body)
	assertServerHTTPProblem(t, response.Code, response.Body.Bytes())
}

type preconditionWriteTrap struct {
	automations.Repository

	test *testing.T
}

func (repo *preconditionWriteTrap) CreateAutomation(
	context.Context,
	automations.Definition,
) (automations.Record, error) {
	repo.test.Error("server precondition failure reached a definition write")
	return automations.Record{}, errors.New("unexpected definition write")
}

func (repo *preconditionWriteTrap) ReplaceAutomation(
	context.Context,
	automations.AutomationID,
	int64,
	automations.Definition,
) (automations.Record, error) {
	repo.test.Error("server precondition failure reached a definition write")
	return automations.Record{}, errors.New("unexpected definition write")
}

func TestAutomationHTTPZeroCoreAdmissionTimeIsInternal(t *testing.T) {
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
	service := automations.NewService(
		repository,
		newAPIDevices(),
		automations.Dependencies{Now: func() time.Time { return time.Time{} }},
	)
	client := newHTTPScenario(t, service)
	response := performJSON(client.router, http.MethodPost, fmt.Sprintf("/v1/automations/%s/runs", record.ID), "{}")
	assertServerHTTPProblem(t, response.Code, response.Body.Bytes())
	var count int
	if err = database.QueryRowContext(t.Context(), "SELECT count(*) FROM automation_history").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed precondition wrote %d history entries", count)
	}
}

func assertServerHTTPProblem(t *testing.T, status int, raw []byte) {
	t.Helper()
	var problem map[string]any
	if err := json.Unmarshal(raw, &problem); err != nil {
		t.Fatal(err)
	}
	delete(problem, "$schema")
	want := map[string]any{
		"type":   "about:blank",
		"title":  "Internal Server Error",
		"status": float64(500),
		"code":   "internal_error",
		"detail": "internal error",
	}
	if status != http.StatusInternalServerError || !reflect.DeepEqual(problem, want) {
		t.Errorf("server precondition status=%d problem=%s, want safe 500/internal_error", status, raw)
	}
}
