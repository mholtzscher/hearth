package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// automationProblemBody is the transport problem document one failed automation
// request returns. The conditions tests inspect the code and the optional
// committed-Skip history reference.
type automationProblemBody struct {
	Title      string `json:"title"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
	Code       string `json:"code"`
	HistoryID  string `json:"history_id"`
	HistoryURL string `json:"history_url"`
}

func decodeAutomationProblem(t *testing.T, response *httptest.ResponseRecorder) automationProblemBody {
	t.Helper()
	var problem automationProblemBody
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem %s: %v", response.Body.String(), err)
	}
	return problem
}

// conditionalDefinitionDocument renders one valid strict definition whose single
// entity_state Condition requires /mode to equal "allowed" within five minutes.
// The string evidence keeps failure assertions free of incidental digits.
func conditionalDefinitionDocument(t *testing.T, conditionEntity devices.EntityID) string {
	t.Helper()
	triggerEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	actionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{
		"name": "Conditioned",
		"enabled": true,
		"triggers": [
			{"id":"trigger","kind":"observation","entity_id":%q,"dispositions":["applied"]}
		],
		"conditions": {
			"id":"mode-allowed","kind":"entity_state","entity_id":%q,"pointer":"/mode",
			"operator":"eq","operand":"allowed","max_age_seconds":300
		},
		"steps": [{"id":"step_0","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, string(triggerEntity), string(conditionEntity), string(actionEntity))
}

// apiStateSnapshot covers one Entity with fresh accepted State carrying the
// supplied JSON value, so manual Condition admission evaluates real evidence.
func apiStateSnapshot(t *testing.T, entityID devices.EntityID, value string) devices.EntityStateSnapshot {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.EntityStateSnapshot{
		Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{
			entityID: {
				EntityID: entityID,
				Exists:   true,
				State: &devices.State{
					EntityID:      entityID,
					Value:         devices.Value(value),
					ObservationID: observationID,
					ObservedAt:    time.Now().UTC().Add(-time.Second),
				},
			},
		},
	}
}

// newConditionedAutomation creates one conditional definition and returns its
// ID and the Condition Entity it requires.
func newConditionedAutomation(
	t *testing.T,
	router http.Handler,
) (string, devices.EntityID) {
	t.Helper()
	conditionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	created := performJSON(
		router,
		http.MethodPost,
		"/v1/automations",
		conditionalDefinitionDocument(t, conditionEntity),
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create conditioned automation status = %d: %s", created.Code, created.Body.String())
	}
	return decodeAutomation(t, created).ID, conditionEntity
}

// definitionWithRawConditions renders one otherwise valid definition whose
// conditions member is exactly the supplied raw JSON, so the strict Condition
// schema can be probed without a fully typed fixture.
func definitionWithRawConditions(t *testing.T, rawConditions string) string {
	t.Helper()
	triggerEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	actionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{
		"name": "Strict conditions",
		"enabled": true,
		"triggers": [
			{"id":"trigger","kind":"observation","entity_id":%q,"dispositions":["applied"]}
		],
		"conditions": %s,
		"steps": [{"id":"step_0","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, string(triggerEntity), rawConditions, string(actionEntity))
}

// The definition API must apply the strict recursive Condition schema: null,
// unknown members, contradictory families, empty groups, duplicate IDs,
// invalid operators, and missing required fields are all refused without
// creating an Automation.
func TestAutomationDefinitionConditionsStrictJSONIsRejected(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	leaf := fmt.Sprintf(
		`{"id":"leaf","kind":"entity_state","entity_id":%q,"pointer":"","operator":"eq","operand":1}`,
		string(entity),
	)

	for _, test := range []struct {
		name       string
		conditions string
	}{
		{"null conditions", `null`},
		{"unknown node member", fmt.Sprintf(`{"id":"root","kind":"all","children":[%s],"extra":true}`, leaf)},
		{
			"contradictory family fields",
			fmt.Sprintf(`{"id":"root","kind":"all","children":[%s],"child":%s}`, leaf, leaf),
		},
		{"empty children group", `{"id":"root","kind":"all","children":[]}`},
		{
			"duplicate condition IDs",
			fmt.Sprintf(`{"id":"root","kind":"any","children":[%s,%s]}`, leaf, leaf),
		},
		{
			"invalid operator",
			fmt.Sprintf(`{"id":"root","kind":"entity_state","entity_id":%q,"pointer":"","operator":"within","operand":1}`, string(entity)),
		},
		{
			"missing pointer",
			fmt.Sprintf(`{"id":"root","kind":"entity_state","entity_id":%q,"operator":"eq","operand":1}`, string(entity)),
		},
		{
			"missing operand",
			fmt.Sprintf(`{"id":"root","kind":"entity_state","entity_id":%q,"pointer":"","operator":"eq"}`, string(entity)),
		},
		{"not with a children array", fmt.Sprintf(`{"id":"root","kind":"not","children":[%s]}`, leaf)},
		{"over-age bound", fmt.Sprintf(`{"id":"root","kind":"entity_state","entity_id":%q,"pointer":"","operator":"eq","operand":1,"max_age_seconds":0}`, string(entity))},
	} {
		response := performJSON(
			router,
			http.MethodPost,
			"/v1/automations",
			definitionWithRawConditions(t, test.conditions),
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400: %s", test.name, response.Code, response.Body.String())
		}
	}

	// A rejected definition must not create an Automation.
	listing := performJSON(router, http.MethodGet, "/v1/automations", "")
	var page automationsapi.AutomationCollectionBody
	if err = json.Unmarshal(listing.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("rejected Condition definitions created Automations: %#v", page.Items)
	}
}

// An unconditioned definition must classify its decision not_configured whether
// or not the caller requested a bypass (the flag is derived from the mode), and
// must never fabricate a Condition snapshot.
func TestManualRunUnconditionedBypassDecisionDTOs(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, service := newAutomationHTTP(t, stub)
	created := decodeAutomation(t, performJSON(router, http.MethodPost, "/v1/automations", definitionDocument(t, 1)))

	for _, test := range []struct {
		name string
		body string
	}{
		{"omitted body", ""},
		{"explicit bypass", `{"bypass_conditions":true}`},
	} {
		response := performJSON(router, http.MethodPost, "/v1/automations/"+created.ID+"/runs", test.body)
		if response.Code != http.StatusAccepted {
			t.Fatalf("%s status = %d, want 202: %s", test.name, response.Code, response.Body.String())
		}
		var run automationsapi.AutomationRunBody
		if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		decision := run.ConditionDecision
		if decision.Mode != "not_configured" || decision.BypassRequested ||
			decision.Snapshot != nil || decision.Evaluation != nil {
			t.Fatalf("%s unconditioned decision = %#v", test.name, decision)
		}
		if run.Snapshot.Conditions != nil {
			t.Fatalf("%s unconditioned snapshot conditions = %#v", test.name, run.Snapshot.Conditions)
		}
		waitForAPI(t, service, created.ID, run.ID)
	}
}

// historyEntryIDs lists one Automation's retained Run and Skip IDs over HTTP.
func historyEntryIDs(t *testing.T, router http.Handler, automationID string) []string {
	t.Helper()
	response := performJSON(router, http.MethodGet, "/v1/automations/"+automationID+"/history", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list history status = %d: %s", response.Code, response.Body.String())
	}
	var page automationsapi.AutomationHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

// StartAutomationRunBody.UnmarshalJSON is the strict decoder Huma cannot fully
// supply. It must accept only exactly one JSON object with one optional literal
// boolean member, and it must never echo the payload in its error text.
func TestStartAutomationRunBodyUnmarshalIsStrict(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		body  string
		want  bool
		valid bool
	}{
		{name: "empty object applies", body: `{}`, want: false, valid: true},
		{name: "explicit true", body: `{"bypass_conditions":true}`, want: true, valid: true},
		{name: "explicit false", body: `{"bypass_conditions":false}`, want: false, valid: true},
		{name: "whitespace is tolerated", body: "{ \"bypass_conditions\" :  true }", want: true, valid: true},
		{name: "null body", body: `null`, valid: false},
		{name: "null bypass", body: `{"bypass_conditions":null}`, valid: false},
		{name: "number bypass", body: `{"bypass_conditions":1}`, valid: false},
		{name: "string bypass", body: `{"bypass_conditions":"true"}`, valid: false},
		{name: "capitalized bypass", body: `{"bypass_conditions":True}`, valid: false},
		{name: "unknown member", body: `{"other":false}`, valid: false},
		{name: "array body", body: `[]`, valid: false},
		{name: "scalar body", body: `false`, valid: false},
		{name: "trailing content", body: `{} {}`, valid: false},
	} {
		var body automationsapi.StartAutomationRunBody
		err := body.UnmarshalJSON([]byte(test.body))
		if test.valid {
			if err != nil {
				t.Fatalf("%s: unmarshal = %v, want success", test.name, err)
			}
			if body.BypassConditions != test.want {
				t.Fatalf("%s: bypass = %v, want %v", test.name, body.BypassConditions, test.want)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: unmarshal succeeded, want a fixed rejection", test.name)
		}
		if strings.ContainsAny(err.Error(), "{}\"[]`") {
			t.Fatalf("%s: error echoed the payload: %v", test.name, err)
		}
	}
}

// The optional manual bypass body is strict: omission, {}, and false apply
// Conditions, true bypasses them, and Huma validation stays enabled for every
// other shape.
func TestManualRunOptionalBypassBodyIsStrict(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, _ := newAutomationHTTP(t, stub)
	automationID, conditionEntity := newConditionedAutomation(t, router)
	stub.setEntityStateSnapshot(apiStateSnapshot(t, conditionEntity, `{"mode":"blocked-secret"}`))

	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{"omitted body applies conditions", "", http.StatusConflict},
		{"empty object applies conditions", `{}`, http.StatusConflict},
		{"explicit false applies conditions", `{"bypass_conditions":false}`, http.StatusConflict},
		{"explicit true bypasses conditions", `{"bypass_conditions":true}`, http.StatusAccepted},
		{"null body is rejected", `null`, http.StatusUnprocessableEntity},
		{"null bypass is rejected", `{"bypass_conditions":null}`, http.StatusUnprocessableEntity},
		{"non-boolean bypass is rejected", `{"bypass_conditions":1}`, http.StatusUnprocessableEntity},
		{"string bypass is rejected", `{"bypass_conditions":"true"}`, http.StatusUnprocessableEntity},
		{"unknown member is rejected", `{"bypass_conditions":true,"extra":1}`, http.StatusUnprocessableEntity},
		{"array body is rejected", `[]`, http.StatusUnprocessableEntity},
		{"scalar body is rejected", `true`, http.StatusUnprocessableEntity},
		{"trailing JSON is rejected", `{} {}`, http.StatusBadRequest},
	} {
		response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", test.body)
		if response.Code != test.status {
			t.Fatalf(
				"%s status = %d, want %d: %s",
				test.name, response.Code, test.status, response.Body.String(),
			)
		}
	}

	// A rejected body must not read State or write history; only the three
	// blocked Condition requests read State and only four outcomes commit.
	if reads := stub.snapshotRequests(); len(reads) != 3 {
		t.Fatalf("state reads = %d, want one per blocked Condition admission", len(reads))
	}
	if ids := historyEntryIDs(t, router, automationID); len(ids) != 4 {
		t.Fatalf("history ids = %v, want 4 committed outcomes", ids)
	}
}

// A manual bypass body must not exceed 1 KiB, and Huma must reject the oversized
// body before any admission work happens.
func TestManualRunBypassBodyIsBoundedToOneKiB(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, _ := newAutomationHTTP(t, stub)
	automationID, conditionEntity := newConditionedAutomation(t, router)
	stub.setEntityStateSnapshot(apiStateSnapshot(t, conditionEntity, `{"mode":"blocked-secret"}`))

	oversized := `{"bypass_conditions":true,"padding":"` + strings.Repeat("x", 2048) + `"}`
	response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", oversized)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized bypass body status = %d, want 413: %s", response.Code, response.Body.String())
	}
	if reads := stub.snapshotRequests(); len(reads) != 0 {
		t.Fatalf("oversized body read State: %v", reads)
	}
	if ids := historyEntryIDs(t, router, automationID); len(ids) != 0 {
		t.Fatalf("oversized body wrote history: %v", ids)
	}

	small := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs",
		`{"bypass_conditions":true}`)
	if small.Code != http.StatusAccepted {
		t.Fatalf("small bypass body status = %d, want 202: %s", small.Code, small.Body.String())
	}
}

// A committed manual Condition block must return 409 with the history reference
// that resolves to the retained Skip, and the problem document must not leak the
// evaluated State value or operand.
func TestManualRunConditionBlockReturnsResolvableHistoryReference(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, service := newAutomationHTTP(t, stub)
	automationID, conditionEntity := newConditionedAutomation(t, router)
	stub.setEntityStateSnapshot(apiStateSnapshot(t, conditionEntity, `{"mode":"blocked-secret"}`))

	response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("condition-blocked status = %d, want 409: %s", response.Code, response.Body.String())
	}
	problem := decodeAutomationProblem(t, response)
	if problem.Code != "conditions_false" {
		t.Fatalf("problem code = %q, want conditions_false", problem.Code)
	}
	wantURL := "/v1/automations/" + automationID + "/history/" + problem.HistoryID
	if problem.HistoryID == "" || problem.HistoryURL != wantURL {
		t.Fatalf("problem history reference = %q / %q, want %q", problem.HistoryID, problem.HistoryURL, wantURL)
	}
	if body := response.Body.String(); strings.Contains(body, "blocked-secret") ||
		strings.Contains(body, "allowed") || strings.Contains(body, `"operand"`) ||
		strings.Contains(body, `"selected_value"`) {
		t.Fatalf("problem document leaked evaluated evidence: %s", body)
	}

	// The returned reference must resolve to the committed Skip.
	entry := performJSON(router, http.MethodGet, problem.HistoryURL, "")
	if entry.Code != http.StatusOK {
		t.Fatalf("history reference status = %d, want 200: %s", entry.Code, entry.Body.String())
	}
	var history automationsapi.AutomationHistoryEntryBody
	if err := json.Unmarshal(entry.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.Kind != "skip" || history.Skip == nil {
		t.Fatalf("history reference resolved to %#v", history)
	}
	if history.Skip.ID != problem.HistoryID || history.Skip.Source != "manual" || history.Skip.Fact != nil {
		t.Fatalf("committed manual Skip = %#v", history.Skip)
	}
	if history.Skip.Reason != "conditions_false" ||
		history.Skip.ConditionDecision.Mode != "evaluated" ||
		history.Skip.ConditionDecision.Evaluation == nil ||
		history.Skip.ConditionDecision.Evaluation.Result != "false" {
		t.Fatalf("committed Skip decision = %#v", history.Skip.ConditionDecision)
	}
	if len(history.Skip.MatchedTriggers) != 0 {
		t.Fatalf("manual Skip carries matched triggers: %#v", history.Skip.MatchedTriggers)
	}
	if stub.executionCount() != 0 {
		t.Fatal("a Condition-blocked manual request executed a Command")
	}
	waitForNoActiveRuns(t, service, automationID)
}

// Unknown Conditions must map to the conditions_unknown problem code with the
// same committed-Skip history reference.
func TestManualRunConditionUnknownReturnsHistoryReference(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, _ := newAutomationHTTP(t, stub)
	automationID, conditionEntity := newConditionedAutomation(t, router)
	stub.setEntityStateSnapshot(devices.EntityStateSnapshot{
		Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{
			conditionEntity: {EntityID: conditionEntity, Exists: false},
		},
	})

	response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", "")
	if response.Code != http.StatusConflict {
		t.Fatalf("unknown-condition status = %d, want 409: %s", response.Code, response.Body.String())
	}
	problem := decodeAutomationProblem(t, response)
	if problem.Code != "conditions_unknown" || problem.HistoryID == "" {
		t.Fatalf("unknown-condition problem = %#v", problem)
	}
	entry := performJSON(router, http.MethodGet, problem.HistoryURL, "")
	if entry.Code != http.StatusOK {
		t.Fatalf("unknown history reference status = %d: %s", entry.Code, entry.Body.String())
	}
	var history automationsapi.AutomationHistoryEntryBody
	if err := json.Unmarshal(entry.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.Skip == nil || history.Skip.Reason != "conditions_unknown" ||
		history.Skip.ConditionDecision.Evaluation == nil ||
		history.Skip.ConditionDecision.Evaluation.Result != "unknown" {
		t.Fatalf("unknown committed Skip = %#v", history.Skip)
	}
	reasons := map[string]bool{}
	for _, node := range history.Skip.ConditionDecision.Evaluation.Nodes {
		if node.UnknownReason != nil {
			reasons[*node.UnknownReason] = true
		}
	}
	if !reasons["entity_missing"] {
		t.Fatalf("unknown leaf reasons = %v, want entity_missing", reasons)
	}
}

// An incomplete snapshot reaching the transaction must return safe 503
// condition_snapshot_unavailable and commit no fabricated Skip.
func TestManualRunSnapshotUnavailableIsSafe503(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, _ := newAutomationHTTP(t, stub)
	automationID, _ := newConditionedAutomation(t, router)
	// An empty snapshot never covers the required Condition Entity.
	stub.setEntityStateSnapshot(devices.EntityStateSnapshot{})

	response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("incomplete snapshot status = %d, want 503: %s", response.Code, response.Body.String())
	}
	problem := decodeAutomationProblem(t, response)
	if problem.Code != "condition_snapshot_unavailable" {
		t.Fatalf("incomplete snapshot code = %q, want condition_snapshot_unavailable", problem.Code)
	}
	if problem.HistoryID != "" || problem.HistoryURL != "" {
		t.Fatalf("incomplete snapshot fabricated a history reference: %#v", problem)
	}
	if ids := historyEntryIDs(t, router, automationID); len(ids) != 0 {
		t.Fatalf("incomplete snapshot wrote history: %v", ids)
	}
}

// A snapshot acquisition deadline or cancellation is not the definition-edit
// coverage race and keeps the ordinary safe 500 mapping.
func TestManualRunSnapshotDeadlineIsSafe500(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{"snapshot deadline", context.DeadlineExceeded},
		{"wrapped snapshot deadline", fmt.Errorf("snapshot read: %w", context.DeadlineExceeded)},
		{"snapshot cancellation", context.Canceled},
	} {
		stub := newAPIDevices()
		router, _, _ := newAutomationHTTP(t, stub)
		automationID, _ := newConditionedAutomation(t, router)
		stub.setEntityStateSnapshotError(test.err)

		response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", "")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d, want 500: %s", test.name, response.Code, response.Body.String())
		}
		problem := decodeAutomationProblem(t, response)
		if problem.Code == "condition_snapshot_unavailable" {
			t.Fatalf("%s incorrectly mapped to a coverage race: %#v", test.name, problem)
		}
		if problem.HistoryID != "" || problem.HistoryURL != "" {
			t.Fatalf("%s fabricated a history reference: %#v", test.name, problem)
		}
		if ids := historyEntryIDs(t, router, automationID); len(ids) != 0 {
			t.Fatalf("%s wrote history: %v", test.name, ids)
		}
	}
}

// An ordinary storage failure and unreadable stored State must both return the
// safe 500 problem without internal text and without a fabricated Skip.
func TestManualRunStorageFailureIsSafe500(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
	}{
		{"ordinary database failure", errors.New("sqlite: database is locked by another process")},
		{"corrupt stored state", devices.ErrEntityStateSnapshotCorrupt},
	} {
		stub := newAPIDevices()
		router, _, _ := newAutomationHTTP(t, stub)
		automationID, _ := newConditionedAutomation(t, router)
		stub.setEntityStateSnapshotError(test.err)

		response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", "")
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d, want 500: %s", test.name, response.Code, response.Body.String())
		}
		problem := decodeAutomationProblem(t, response)
		if problem.Code == "conditions_false" || problem.Code == "conditions_unknown" {
			t.Fatalf("%s fabricated a Condition outcome: %#v", test.name, problem)
		}
		if body := response.Body.String(); strings.Contains(body, "sqlite") ||
			strings.Contains(body, "locked") || strings.Contains(body, "snapshot contains") {
			t.Fatalf("%s leaked internal text: %s", test.name, body)
		}
		if ids := historyEntryIDs(t, router, automationID); len(ids) != 0 {
			t.Fatalf("%s wrote history: %v", test.name, ids)
		}
	}
}

// A bypassed manual Run must retain the bypass intent and configured snapshot in
// its decision, and its history summary must expose provenance without the tree.
func TestManualRunBypassDecisionAndSummaryDTOs(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, service := newAutomationHTTP(t, stub)
	automationID, _ := newConditionedAutomation(t, router)
	stub.setEntityStateSnapshotError(errors.New("bypass must not read State"))

	response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs",
		`{"bypass_conditions":true}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("bypass status = %d, want 202: %s", response.Code, response.Body.String())
	}
	if reads := stub.snapshotRequests(); len(reads) != 0 {
		t.Fatalf("bypass read State: %v", reads)
	}
	var run automationsapi.AutomationRunBody
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	decision := run.ConditionDecision
	if decision.Mode != "bypassed" || !decision.BypassRequested || decision.Evaluation != nil {
		t.Fatalf("bypassed decision = %#v", decision)
	}
	if decision.Snapshot == nil || decision.Snapshot.Kind != "entity_state" ||
		decision.Snapshot.ID != "mode-allowed" || decision.Snapshot.Operator != "eq" {
		t.Fatalf("bypassed snapshot = %#v", decision.Snapshot)
	}
	if decision.Snapshot.MaxAgeSeconds == nil || *decision.Snapshot.MaxAgeSeconds != 300 {
		t.Fatalf("bypassed snapshot age = %#v", decision.Snapshot.MaxAgeSeconds)
	}
	if run.Snapshot.Conditions == nil || run.Snapshot.Conditions.ID != decision.Snapshot.ID {
		t.Fatalf("run snapshot conditions = %#v", run.Snapshot.Conditions)
	}
	waitForAPI(t, service, automationID, run.ID)

	summary := historySummaryFor(t, router, automationID, run.ID)
	if summary.Source != "manual" || summary.ConditionMode != "bypassed" ||
		!summary.BypassRequested || summary.ConditionResult != nil {
		t.Fatalf("bypassed summary = %#v", summary)
	}
	// A summary must never duplicate the Condition tree or predicate values.
	listing := performJSON(router, http.MethodGet, "/v1/automations/"+automationID+"/history", "")
	if body := listing.Body.String(); strings.Contains(body, `"snapshot"`) ||
		strings.Contains(body, `"evaluation"`) || strings.Contains(body, "mode-allowed") {
		t.Fatalf("history summary leaked the Condition tree: %s", body)
	}
}

// An evaluated manual Run must carry its full evaluation, including a selected
// JSON null distinct from an absent selection.
func TestManualRunEvaluatedDecisionDTOsPreserveSelectedNull(t *testing.T) {
	t.Parallel()
	stub := newAPIDevices()
	router, _, _ := newAutomationHTTP(t, stub)

	conditionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	definition := conditionDefinitionWithOperand(t, conditionEntity, `"/level"`, "eq", "null")
	created := performJSON(router, http.MethodPost, "/v1/automations", definition)
	if created.Code != http.StatusCreated {
		t.Fatalf("create null-condition automation status = %d: %s", created.Code, created.Body.String())
	}
	automationID := decodeAutomation(t, created).ID
	stub.setEntityStateSnapshot(apiStateSnapshot(t, conditionEntity, `{"level":null}`))

	response := performJSON(router, http.MethodPost, "/v1/automations/"+automationID+"/runs", "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("null-condition run status = %d, want 202: %s", response.Code, response.Body.String())
	}
	var raw struct {
		ConditionDecision struct {
			Mode       string `json:"mode"`
			Evaluation struct {
				EvaluatedAt string `json:"evaluated_at"`
				Result      string `json:"result"`
				Nodes       []struct {
					SelectedValue json.RawMessage `json:"selected_value"`
					ObservationID *string         `json:"observation_id"`
					ObservedAt    *string         `json:"observed_at"`
				} `json:"nodes"`
			} `json:"evaluation"`
		} `json:"condition_decision"`
	}
	if decodeErr := json.Unmarshal(response.Body.Bytes(), &raw); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if raw.ConditionDecision.Mode != "evaluated" || raw.ConditionDecision.Evaluation.Result != "true" {
		t.Fatalf("null-condition decision = %#v", raw.ConditionDecision)
	}
	if !strings.HasSuffix(raw.ConditionDecision.Evaluation.EvaluatedAt, "Z") {
		t.Fatalf("evaluated_at = %q, want an RFC3339Nano UTC instant", raw.ConditionDecision.Evaluation.EvaluatedAt)
	}
	if len(raw.ConditionDecision.Evaluation.Nodes) != 1 {
		t.Fatalf("null-condition nodes = %#v", raw.ConditionDecision.Evaluation.Nodes)
	}
	leaf := raw.ConditionDecision.Evaluation.Nodes[0]
	if string(leaf.SelectedValue) != "null" {
		t.Fatalf("selected null encoded as %q, want null", leaf.SelectedValue)
	}
	if leaf.ObservationID == nil || *leaf.ObservationID == "" {
		t.Fatalf("selected value is missing its Observation identity: %#v", leaf)
	}
	if leaf.ObservedAt == nil || !strings.HasSuffix(*leaf.ObservedAt, "Z") {
		t.Fatalf("observed_at = %v, want an RFC3339Nano UTC instant", leaf.ObservedAt)
	}
}

// conditionDefinitionWithOperand renders one conditional definition with a
// caller-supplied pointer, operator, and operand.
func conditionDefinitionWithOperand(
	t *testing.T,
	conditionEntity devices.EntityID,
	pointer, operator, operand string,
) string {
	t.Helper()
	triggerEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	actionEntity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`{
		"name": "Conditioned",
		"enabled": true,
		"triggers": [
			{"id":"trigger","kind":"observation","entity_id":%q,"dispositions":["applied"]}
		],
		"conditions": {
			"id":"leaf","kind":"entity_state","entity_id":%q,"pointer":%s,
			"operator":%q,"operand":%s
		},
		"steps": [{"id":"step_0","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, string(triggerEntity), string(conditionEntity), pointer, operator, operand, string(actionEntity))
}

// historySummaryFor returns the listing projection for one retained entry.
func historySummaryFor(
	t *testing.T,
	router http.Handler,
	automationID string,
	entryID string,
) automationsapi.AutomationHistorySummaryBody {
	t.Helper()
	response := performJSON(router, http.MethodGet, "/v1/automations/"+automationID+"/history", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list history status = %d: %s", response.Code, response.Body.String())
	}
	var page automationsapi.AutomationHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == entryID {
			return item
		}
	}
	t.Fatalf("history %s does not contain entry %s", automationID, entryID)
	return automationsapi.AutomationHistorySummaryBody{}
}

// waitForNoActiveRuns drains any admitted Run so a test cannot leak a worker
// into the shared service.
func waitForNoActiveRuns(t *testing.T, service *automations.Service, automationID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListHistory(ctx, automations.ListHistoryParams{
			AutomationID: automations.AutomationID(automationID),
			Limit:        50,
		})
		if err != nil {
			t.Fatal(err)
		}
		active := false
		for _, item := range page.Items {
			if item.Kind == automations.HistoryRun && item.Status == automations.RunRunning {
				active = true
			}
		}
		if !active {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Run for %s did not finish: %v", automationID, ctx.Err())
		case <-ticker.C:
		}
	}
}
