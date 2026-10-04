package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type scheduleBranchDevices struct {
	*blockingAutomationDevices

	reads atomic.Int64
}

func (seam *scheduleBranchDevices) GetEntityStateSnapshot(
	context.Context,
	[]devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	seam.reads.Add(1)
	return devices.EntityStateSnapshot{}, errors.New("scheduled Trigger-only branch must not read State")
}

// This protects the app's calendar-worker to HTTP integration. A UTC instant
// matching Chicago's 07:00 must produce schedule history through the assembled
// handler, with no device evidence or empty Entity fields. Multiple matched IDs
// must reach branch selection, whose evidence contains only the leaf's intersection.
func TestRuntimeScheduleWorkerHistoryThroughHTTP(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	location, err := (Config{HouseholdTimezone: "America/Chicago"}).LoadHouseholdTimezone()
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 2, 11, 59, 0, 0, time.UTC).UnixNano())
	dependencies := automations.Dependencies{
		Now: func() time.Time { return time.Unix(0, clock.Load()).UTC() }, HouseholdLocation: location,
	}
	seam := &scheduleBranchDevices{blockingAutomationDevices: newBlockingAutomationDevices()}
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(openOrderingDatabase(t), dependencies), seam, dependencies,
	)
	t.Cleanup(func() {
		close(seam.release)
		if drainErr := service.Drain(ctx); drainErr != nil {
			t.Error(drainErr)
		}
	})
	handler, _ := newHTTPHandler(&stubDevices{}, service, &stubAgent{}, &testReadiness{}, &stubDevices{}, service,
		newMCPServer(&stubDevices{}, service, nil))
	const definition = `{"name":"Morning","enabled":true,"triggers":[
		{"id":"morning","kind":"cron","expression":" * 7 * * fri "},
		{"id":"minute","kind":"cron","expression":"* * * * *"},
		{"id":"midnight","kind":"cron","expression":"0 0 * * *"}],
		"steps":[{"id":"route","kind":"if",
			"conditions":{"id":"source","kind":"trigger","trigger_ids":["minute","midnight"]},
			"then":[{"id":"on","entity_id":"ent_01920000-0000-7000-8000-000000000004","operation":"set","parameters":{"value":true}}]}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/automations", strings.NewReader(definition))
	request.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("runtime create = %d: %s", created.Code, created.Body.String())
	}
	var record struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	worker, err := startScheduleScheduling(ctx, slog.New(slog.DiscardHandler), service, dependencies.Now,
		func() (<-chan time.Time, func()) { return ticks, func() {} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := worker.Stop(ctx); stopErr != nil {
			t.Error(stopErr)
		}
	})
	clock.Store(time.Date(2026, 10, 2, 12, 0, 20, 0, time.UTC).UnixNano())
	select {
	case ticks <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar worker did not receive tick")
	}
	select {
	case <-seam.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar worker did not execute admitted Run")
	}
	clock.Store(time.Date(2026, 10, 2, 12, 1, 20, 0, time.UTC).UnixNano())
	select {
	case ticks <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("calendar worker did not receive second tick")
	}
	assertRuntimeScheduleHistory(t, handler, "/v1/automations/"+record.ID+"/history")
	if seam.reads.Load() != 0 {
		t.Fatal("scheduled Trigger-only branch read State")
	}
}

func assertRuntimeScheduleHistory(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	var history struct {
		Items []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Source string `json:"source"`
		} `json:"items"`
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		response := appRequest(handler, path)
		if response.Code != http.StatusOK {
			return false, fmt.Errorf("history = %d: %s", response.Code, response.Body.String())
		}
		if decodeErr := json.Unmarshal(response.Body.Bytes(), &history); decodeErr != nil {
			return false, decodeErr
		}
		return len(history.Items) == 2, nil
	})
	for _, item := range history.Items {
		if item.Source != "schedule" {
			t.Fatalf("history source = %q", item.Source)
		}
		assertRuntimeScheduleDetail(t, appRequest(handler, path+"/"+item.ID), item.Kind)
	}
}

func assertRuntimeScheduleDetail(t *testing.T, response *httptest.ResponseRecorder, kind string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("history detail = %d: %s", response.Code, response.Body.String())
	}
	// The envelope also has kind and $schema strings, so decode its selected payload separately.
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(envelope[kind], &body); err != nil {
		t.Fatal(err)
	}
	if body["source"] != "schedule" {
		t.Fatalf("detail source = %v", body)
	}
	for _, field := range []string{"fact", "held_state"} {
		if _, present := body[field]; present {
			t.Fatalf("schedule detail contains %s", field)
		}
	}
	var triggers []any
	wantTriggers := 2
	if kind == "run" {
		var run branchingHistoryRun
		if err := json.Unmarshal(envelope[kind], &run); err != nil {
			t.Fatal(err)
		}
		assertBranchingTriggerEvidence(t, run, "schedule", []string{"morning", "minute"}, []string{"minute"})
		triggers = body["snapshot"].(map[string]any)["triggers"].([]any)
		wantTriggers = 3 // The immutable definition also contains the unmatched midnight Trigger.
	} else {
		if body["reason"] != "automation_busy" {
			t.Fatalf("Skip reason = %v", body["reason"])
		}
		triggers = body["matched_triggers"].([]any)
	}
	if len(triggers) != wantTriggers {
		t.Fatalf("Triggers = %v, want %d", triggers, wantTriggers)
	}
	trigger := triggers[0].(map[string]any)
	if len(trigger) != 3 || trigger["expression"] != "* 7 * * fri" {
		t.Fatalf("cron output = %v", trigger)
	}
}

// stubAutomations is the narrow HTTP seam the runtime handler is assembled
// with. Every definition or history operation panics: the app tests here assert
// transport registration and the admission readiness gate, never automation
// behavior, which the automations module owns and tests through its own Huma
// registration.
type stubAutomations struct {
	admissionClosed bool
}

func (*stubAutomations) CreateAutomation(
	context.Context,
	automations.Definition,
) (automations.Record, error) {
	panic("unexpected CreateAutomation call")
}

func (*stubAutomations) GetAutomation(
	context.Context,
	automations.AutomationID,
) (automations.Record, error) {
	panic("unexpected GetAutomation call")
}

func (*stubAutomations) ListAutomations(
	context.Context,
	automations.ListAutomationsParams,
) (automations.Page[automations.Record], error) {
	panic("unexpected ListAutomations call")
}

func (*stubAutomations) ReplaceAutomation(
	context.Context,
	automations.AutomationID,
	int64,
	automations.Definition,
) (automations.Record, error) {
	panic("unexpected ReplaceAutomation call")
}

func (*stubAutomations) DeleteAutomation(context.Context, automations.AutomationID, int64) error {
	panic("unexpected DeleteAutomation call")
}

func (*stubAutomations) StartManualRun(
	context.Context,
	automations.ManualRunInput,
) (automations.Run, error) {
	panic("unexpected StartManualRun call")
}

func (*stubAutomations) GetHistoryEntry(
	context.Context,
	automations.AutomationID,
	string,
) (automations.HistoryEntry, error) {
	panic("unexpected GetHistoryEntry call")
}

func (*stubAutomations) ListHistory(
	context.Context,
	automations.ListHistoryParams,
) (automations.Page[automations.HistorySummary], error) {
	panic("unexpected ListHistory call")
}

func (stub *stubAutomations) AdmissionOpen() bool {
	return stub == nil || !stub.admissionClosed
}

// TestRuntimeExposesAutomationOperations protects the app-level HTTP surface:
// the runtime must register all eight automation operations from section 8.4 as
// one Huma-backed group so the published OpenAPI document carries them. It fails
// if registration is dropped or an operation is renamed.
func TestRuntimeExposesAutomationOperations(t *testing.T) {
	t.Parallel()
	handler, _ := newHTTPHandler(
		&stubDevices{},
		&stubAutomations{},
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		&stubAutomations{},
		newMCPServer(&stubDevices{}, &stubAutomations{}, nil),
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, body = %s", response.Code, response.Body.String())
	}
	var document struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}

	expected := []struct {
		method      string
		path        string
		operationID string
	}{
		{"post", "/v1/automations", "create-automation"},
		{"get", "/v1/automations", "list-automations"},
		{"get", "/v1/automations/{automation_id}", "get-automation"},
		{"put", "/v1/automations/{automation_id}", "replace-automation"},
		{"delete", "/v1/automations/{automation_id}", "delete-automation"},
		{"post", "/v1/automations/{automation_id}/runs", "start-automation-run"},
		{"get", "/v1/automations/{automation_id}/history", "list-automation-history"},
		{"get", "/v1/automations/{automation_id}/history/{entry_id}", "get-automation-history-entry"},
	}
	for _, want := range expected {
		pathItem, ok := document.Paths[want.path]
		if !ok {
			t.Fatalf("OpenAPI is missing automation path %q", want.path)
		}
		operation, ok := pathItem[want.method]
		if !ok || operation.OperationID != want.operationID {
			t.Fatalf(
				"OpenAPI %s %s = %#v, want operation %q",
				want.method, want.path, operation, want.operationID,
			)
		}
		if len(operation.Tags) != 1 || operation.Tags[0] != "Automations" {
			t.Fatalf(
				"OpenAPI %s %s tags = %v, want [Automations]",
				want.method, want.path, operation.Tags,
			)
		}
	}
	for _, schema := range []string{
		"AutomationDefinition",
		"AutomationBody",
		"AutomationCollectionBody",
		"AutomationConditionBody",
		"AutomationConditionDecisionBody",
		"AutomationSkipBody",
	} {
		if _, ok := document.Components.Schemas[schema]; !ok {
			t.Errorf("OpenAPI is missing automation schema %q", schema)
		}
	}
}

// TestRuntimeOpenAPIPublishesManualBypassAndConditionContract protects A15 at
// the assembled runtime: the manual Run operation must publish the optional
// closed bypass body with an optional boolean member, and the documented 409
// must carry the optional committed-Skip history reference. It fails if the
// manual body becomes required, accepts unknown members, or is not boolean.
func TestRuntimeOpenAPIPublishesManualBypassAndConditionContract(t *testing.T) {
	t.Parallel()
	handler, _ := newHTTPHandler(
		&stubDevices{},
		&stubAutomations{},
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		&stubAutomations{},
		newMCPServer(&stubDevices{}, &stubAutomations{}, nil),
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d, body = %s", response.Code, response.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}

	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI document has no paths")
	}
	runPath, ok := paths["/v1/automations/{automation_id}/runs"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI is missing the manual Run path")
	}
	operation, ok := runPath["post"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI is missing the manual Run operation")
	}
	assertRuntimeManualRunBypassBody(t, operation)
	assertRuntimeConflictHistoryReference(t, operation)
}

// assertRuntimeManualRunBypassBody checks the assembled runtime publishes an
// optional, closed, non-nullable manual bypass body with one boolean member.
func assertRuntimeManualRunBypassBody(t *testing.T, operation map[string]any) {
	t.Helper()
	requestBody, ok := operation["requestBody"].(map[string]any)
	if !ok {
		t.Fatal("manual Run publishes no request body schema")
	}
	if required, present := requestBody["required"]; present && required != false {
		t.Fatalf("manual Run request body required = %v, want optional", required)
	}
	content, ok := requestBody["content"].(map[string]any)
	if !ok {
		t.Fatal("manual Run request body has no content")
	}
	mediaType, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatal("manual Run request body has no JSON content type")
	}
	bodySchema, ok := mediaType["schema"].(map[string]any)
	if !ok {
		t.Fatal("manual Run request body has no JSON schema")
	}
	if bodySchema["type"] != "object" {
		t.Fatalf("manual Run body type = %v, want object", bodySchema["type"])
	}
	if additional, present := bodySchema["additionalProperties"]; !present || additional != false {
		t.Fatalf("manual Run body additionalProperties = %v, want false", additional)
	}
	properties, ok := bodySchema["properties"].(map[string]any)
	if !ok || len(properties) != 1 {
		t.Fatalf("manual Run body properties = %v, want only bypass_conditions", properties)
	}
	bypass, ok := properties["bypass_conditions"].(map[string]any)
	if !ok || bypass["type"] != "boolean" {
		t.Fatalf("bypass_conditions schema = %v, want boolean", properties["bypass_conditions"])
	}
	if nullable, present := bodySchema["nullable"]; present && nullable == true {
		t.Fatalf("manual Run body is nullable: %v", bodySchema)
	}
}

// assertRuntimeConflictHistoryReference checks the assembled runtime documents
// the optional committed-Skip history reference on the manual Run 409 response.
func assertRuntimeConflictHistoryReference(t *testing.T, operation map[string]any) {
	t.Helper()
	responses, ok := operation["responses"].(map[string]any)
	if !ok {
		t.Fatal("manual Run operation has no responses")
	}
	conflict, ok := responses["409"].(map[string]any)
	if !ok {
		t.Fatal("manual Run operation documents no 409")
	}
	mediaType, ok := conflict["content"].(map[string]any)["application/problem+json"].(map[string]any)
	if !ok {
		t.Fatal("manual Run 409 has no problem content type")
	}
	problem, ok := mediaType["schema"].(map[string]any)
	if !ok {
		t.Fatal("manual Run 409 has no problem schema")
	}
	properties, ok := problem["properties"].(map[string]any)
	if !ok {
		t.Fatal("manual Run 409 problem schema has no properties")
	}
	for _, member := range []string{"history_id", "history_url"} {
		if _, published := properties[member]; !published {
			t.Fatalf("manual Run 409 problem schema is missing %q", member)
		}
	}
}

// TestRuntimeReadinessRequiresOpenAutomationAdmission protects A13 at the
// transport surface: readiness fails when automation admission has closed even
// though every broker and persistence dependency is healthy, and health stays
// green. It fails if a closed gate still reports ready.
func TestRuntimeReadinessRequiresOpenAutomationAdmission(t *testing.T) {
	t.Parallel()
	admission := &stubAutomations{}
	handler, _ := newHTTPHandler(
		&stubDevices{},
		&stubAutomations{},
		&stubAgent{},
		&testReadiness{},
		&stubDevices{},
		admission,
		newMCPServer(&stubDevices{}, &stubAutomations{}, nil),
	)
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body = %s", response.Code, response.Body.String())
	}
	admission.admissionClosed = true
	if response := appRequest(handler, "/readyz"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with closed automation admission = %d", response.Code)
	}
	if response := appRequest(handler, "/healthz"); response.Code != http.StatusOK {
		t.Fatalf("healthz with closed automation admission = %d", response.Code)
	}
}
