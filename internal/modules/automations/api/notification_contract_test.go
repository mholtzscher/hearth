package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// Script only the external notification seam; real Automation execution and
// SQLite own the evidence read through both public transports.
type apiNotifications struct {
	mu          sync.Mutex
	destination notifications.DestinationID
	outcome     notifications.Outcome
	reason      string
	attempt     notifications.Attempt
	lookupErr   error
}

func (stub *apiNotifications) GetDestination(
	_ context.Context,
	id notifications.DestinationID,
) (notifications.Destination, error) {
	if stub.lookupErr != nil {
		return notifications.Destination{}, stub.lookupErr
	}
	if id != stub.destination {
		return notifications.Destination{}, notifications.ErrDestinationNotFound
	}
	return notifications.Destination{ID: id, Name: "Household", Available: true}, nil
}
func (stub *apiNotifications) Send(_ context.Context, input notifications.SendInput) (notifications.Attempt, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	now := time.Now().UTC()
	stub.attempt = notifications.Attempt{
		ID:            input.AttemptID,
		DestinationID: input.DestinationID,
		Message:       input.Message,
		Outcome:       stub.outcome,
		ReasonCode:    stub.reason,
		CompletedAt:   &now,
	}
	return stub.attempt, nil
}

// Transport distinguishes an invalid reference from an inability to check it.
func TestNotificationDestinationLookupHTTPErrorStatus(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"missing destination", notifications.ErrDestinationNotFound, http.StatusBadRequest},
		{"storage outage", errors.New("storage unavailable"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &apiNotifications{lookupErr: test.err}
			router, _, _ := newAutomationHTTP(t, newAPIDevices(), automations.Dependencies{Notifications: stub})
			document := delayDocument(
				`{"id":"notify","kind":"notification","destination_id":"ndst_01980000-0000-7000-8000-000000000001","title":"","message":"Warning"}`,
			)
			response := performJSON(router, http.MethodPost, "/v1/automations", document)
			if response.Code != test.status {
				t.Fatalf("lookup status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
		})
	}
}
func (stub *apiNotifications) GetAttempt(_ context.Context, id notifications.AttemptID) (notifications.Attempt, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.attempt.ID != id {
		return notifications.Attempt{}, notifications.ErrAttemptNotFound
	}
	return stub.attempt, nil
}

func TestNotificationHTTPMCPDefinitionAndHistoryContract(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		outcome notifications.Outcome
		reason  string
	}{
		{notifications.Accepted, ""}, {notifications.Rejected, "provider_rejected"},
		{notifications.NotSent, "destination_unavailable"}, {notifications.Unknown, "outcome_timeout"},
	} {
		t.Run(string(test.outcome), func(t *testing.T) {
			t.Parallel()
			destination, err := notifications.NewDestinationID()
			if err != nil {
				t.Fatal(err)
			}
			stub := &apiNotifications{destination: destination, outcome: test.outcome, reason: test.reason}
			router, _, service := newAutomationHTTP(t, newAPIDevices(), automations.Dependencies{Notifications: stub})
			session := connectAutomationMCP(t, service)
			tools := delayTools(t, session)
			openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
			leaf := fmt.Sprintf(
				`{"id":"notify","kind":"notification","destination_id":%q,"title":"Warning","message":"Static message"}`,
				destination,
			)
			document := delayDocument(
				`{"id":"route","kind":"if","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"then":[` + leaf + `]}`,
			)
			body := writeDelayDefinition(t, router, session, "HTTP", "", document)
			id := body["id"].(string)
			body = writeDelayDefinition(t, router, session, "MCP", id, document)
			assertDelaySchemaValid(t, compileDelayContractSchema(t, tools["replace_automation"].OutputSchema, ""), body)
			if canonicalJSON(t, body["definition"]) != canonicalJSON(t, exactJSONObject(t, document)) {
				t.Fatal("notification definition lost in replacement")
			}
			assertDelayHTTPMatches(t, router, "/v1/automations/"+id, body)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationBody"),
				body,
			)
			run := startDelayRun(t, router, session, "MCP", id)
			if canonicalJSON(t, run["notifications"]) != "[]" {
				t.Fatalf("admission notifications = %v", run)
			}
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["start_automation_run"].OutputSchema, ""),
				run,
			)
			runID := run["id"].(string)
			waitForAPI(t, service, id, runID)
			entry := exactToolBody(
				t,
				callAutomationTool(
					t,
					session,
					"get_automation_history_entry",
					map[string]any{"automation_id": id, "entry_id": runID},
				),
			)
			assertDelayHTTPMatches(t, router, "/v1/automations/"+id+"/history/"+runID, entry)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, tools["get_automation_history_entry"].OutputSchema, ""),
				entry,
			)
			assertDelaySchemaValid(
				t,
				compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationHistoryEntryBody"),
				entry,
			)
			assertNotificationEvidence(t, entry["run"].(map[string]any), test.outcome, test.reason)
			// Discovery must reject mixing Command fields into this additive variant.
			invalid := exactJSONObject(t, document)
			invalid["steps"].([]any)[0].(map[string]any)["then"].([]any)[0].(map[string]any)["operation"] = "set"
			definitionSchema := compileDelayContractSchema(t, openapi, "#/components/schemas/AutomationDefinition")
			if definitionSchema.Validate(invalid) == nil {
				t.Fatal("discovery accepted mixed notification/Command")
			}
			raw, marshalErr := json.Marshal(invalid)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			response := performJSON(router, http.MethodPost, "/v1/automations", string(raw))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("mixed leaf = %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func assertNotificationEvidence(t *testing.T, retained map[string]any, outcome notifications.Outcome, reason string) {
	t.Helper()
	evidence := retained["notifications"].([]any)
	if len(evidence) != 1 || canonicalJSON(t, retained["steps"]) != "[]" {
		t.Fatalf("typed evidence = %v", retained)
	}
	notification := evidence[0].(map[string]any)
	expectedStatus := "failed"
	if outcome == notifications.Accepted {
		expectedStatus = "accepted"
	}
	if notification["step_id"] != "notify" || notification["position"] != json.Number("0") ||
		notification["status"] != expectedStatus || notification["outcome"] != string(outcome) ||
		notification["attempt_id"] == "" || notification["completed_at"] == nil {
		t.Fatalf("notification evidence = %v", notification)
	}
	if reason == "" {
		if _, found := notification["reason_code"]; found {
			t.Fatal("accepted attempt invented reason")
		}
	} else if notification["reason_code"] != reason {
		t.Fatalf("reason lost: %v", notification)
	}
}

// Discovery is an independent consumer contract: it must not advertise invented
// provider evidence or allow terminal Step status with missing/null completion.
func TestNotificationHistoryDiscoveryLifecycleShapes(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	tools := delayTools(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	schemas := []*jsonschema.Schema{
		compileDelayContractSchema(
			t,
			openapi,
			"#/components/schemas/AutomationRunBody/$defs/history/$defs/notificationExecution",
		),
		compileDelayContractSchema(
			t,
			tools["get_automation_history_entry"].OutputSchema,
			"#/anyOf/0/$defs/history/$defs/notificationExecution",
		),
	}
	for _, test := range []struct {
		name, fields string
		valid        bool
	}{
		{"running", `"status":"running","completed_at":null`, true},
		{"accepted", `"status":"accepted","completed_at":"2026-10-11T10:00:01Z","outcome":"accepted"`, true},
		{"pre-attempt failure", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","reason_code":"notification_admission_closed"`, true},
		{"interrupted", `"status":"interrupted","completed_at":"2026-10-11T10:00:01Z","reason_code":"core_restarted"`, true},
		{"running invents outcome", `"status":"running","completed_at":null,"outcome":"accepted"`, false},
		{"accepted without outcome", `"status":"accepted","completed_at":"2026-10-11T10:00:01Z"`, false},
		{"accepted unknown", `"status":"accepted","completed_at":"2026-10-11T10:00:01Z","outcome":"unknown","reason_code":"outcome_timeout"`, false},
		{"failed accepted", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","outcome":"accepted","reason_code":"provider_error"`, false},
		{"terminal null completion", `"status":"interrupted","completed_at":null,"reason_code":"core_restarted"`, false},
		{"interrupted invents provider evidence", `"status":"interrupted","completed_at":"2026-10-11T10:00:01Z","outcome":"unknown","reason_code":"core_restarted"`, false},
		{"provider failure lacks outcome", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","reason_code":"provider_error"`, false},
		{"unknown wrong reason", `"status":"failed","completed_at":"2026-10-11T10:00:01Z","outcome":"unknown","reason_code":"destination_unavailable"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := exactJSONObject(
				t,
				`{"step_id":"notify","position":0,"attempt_id":"ntf_01980000-0000-7000-8000-000000000001","started_at":"2026-10-11T10:00:00Z",`+test.fields+`}`,
			)
			for _, schema := range schemas {
				if err := schema.Validate(body); (err == nil) != test.valid {
					t.Fatalf("valid=%v, schema error=%v", test.valid, err)
				}
			}
		})
	}
}
