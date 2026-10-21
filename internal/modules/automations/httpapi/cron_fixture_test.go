package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func assertCronScheduleHistory(t *testing.T, router http.Handler, session *httpScenario, id string) {
	t.Helper()
	path := "/v1/automations/" + id
	history := restJSON(t, router, path+"/history")
	assertScenarioPage(
		t,
		"schedule history",
		callHTTPScenario(t, session, "list-automation-history", map[string]any{"automation_id": id}),
		history,
	)
	items := pageObject(t, history)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("schedule history = %v, want Run and busy Skip", items)
	}
	for _, item := range items {
		summary := pageObject(t, item)
		assertScheduleEvidenceAbsent(t, summary)
		entryID := summary["id"].(string)
		detail := restJSON(t, router, path+"/history/"+entryID)
		assertScenarioMatchesRoute(
			t,
			callHTTPScenario(
				t,
				session,
				"get-automation-history-entry",
				map[string]any{"automation_id": id, "entry_id": entryID},
			),
			detail,
		)
		kind := summary["kind"].(string)
		entry := pageObject(t, pageObject(t, detail)[kind])
		assertScheduleEvidenceAbsent(t, entry)
		if kind == "run" {
			if canonicalJSON(t, entry["matched_trigger_ids"]) != `["morning","quarter"]` {
				t.Fatalf("grouped IDs = %v", entry["matched_trigger_ids"])
			}
			assertCronDefinition(t, map[string]any{"definition": entry["snapshot"]}, "0,15 7 * * FRI")
		} else {
			if entry["reason"] != "automation_busy" {
				t.Fatalf("schedule Skip = %v", entry)
			}
			assertCronDefinition(
				t,
				map[string]any{"definition": map[string]any{"triggers": entry["matched_triggers"]}},
				"0,15 7 * * FRI",
			)
		}
	}
}

func assertScenarioMatchesRoute(t *testing.T, scenario *httpScenarioResult, route any) {
	t.Helper()
	if scenario.Failed {
		t.Fatal(httpScenarioErrorText(t, scenario))
	}
	if canonicalJSON(t, scenario.Body) != canonicalJSON(t, route) {
		t.Fatalf(
			"scenario response = %s, direct response = %s",
			canonicalJSON(t, scenario.Body),
			canonicalJSON(t, route),
		)
	}
}

func assertCronDefinition(t *testing.T, body map[string]any, expression string) {
	t.Helper()
	definition := pageObject(t, body["definition"])
	triggers := definition["triggers"].([]any)
	if len(triggers) != 2 {
		t.Fatalf("cron triggers = %v", triggers)
	}
	for index, item := range triggers {
		trigger := pageObject(t, item)
		want, wantID := expression, "morning"
		if index == 1 {
			want = "*/15 * * * *"
			wantID = "quarter"
		}
		if len(trigger) != 3 || trigger["id"] != wantID || trigger["kind"] != "cron" || trigger["expression"] != want {
			t.Fatalf("cron Trigger = %v, want exactly id, kind, expression %q", trigger, want)
		}
	}
}

func assertScheduleEvidenceAbsent(t *testing.T, body map[string]any) {
	t.Helper()
	cause, ok := body["cause"].(map[string]any)
	if !ok || cause["kind"] != "schedule" || len(cause) != 1 {
		t.Fatalf("schedule source = %v", body)
	}
	for _, field := range []string{"source", "fact", "held_state", "entity_id"} {
		if _, present := body[field]; present {
			t.Errorf("schedule history contains %s: %v", field, body)
		}
	}
}

func assertInvalidCronNoWrite(
	t *testing.T,
	router http.Handler,
	session *httpScenario,
	definition map[string]any,
	id, baseline string,
) {
	t.Helper()
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		path, payload := "/v1/automations", any(definition)
		if method == http.MethodPut {
			path += "/" + id
			payload = map[string]any{"expected_revision": 1, "definition": definition}
		}
		response := performJSON(router, method, path, canonicalJSON(t, payload))
		if response.Code != http.StatusUnprocessableEntity && response.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s = %d: %s", method, response.Code, response.Body.String())
		}
		var problem map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if problem["code"] != "invalid_automation" {
			t.Fatalf("invalid %s problem = %v", method, problem)
		}
	}
	for _, tool := range []string{"create-automation", "replace-automation"} {
		arguments := map[string]any{"definition": definition}
		if tool == "replace-automation" {
			arguments["automation_id"], arguments["expected_revision"] = id, 1
		}
		result := callHTTPScenario(t, session, tool, arguments)
		assertCronHTTPValidationError(t, result)
	}
	if got := canonicalJSON(t, restJSON(t, router, "/v1/automations/"+id)); got != baseline {
		t.Fatalf("rejected replacement changed definition: %s", got)
	}
	if ids := pageItemIDs(t, restJSON(t, router, "/v1/automations")); len(ids) != 1 || ids[0] != id {
		t.Fatalf("rejected create wrote definitions: %v", ids)
	}
	if ids := pageItemIDs(t, restJSON(t, router, "/v1/automations/"+id+"/history")); len(ids) != 0 {
		t.Fatalf("invalid input wrote history: %v", ids)
	}
}

func assertCronHTTPValidationError(t *testing.T, result *httpScenarioResult) {
	t.Helper()
	text := httpScenarioErrorText(t, result)
	if !result.Failed || !strings.HasPrefix(text, "invalid_automation: ") {
		t.Fatalf("HTTP error = %s, want invalid_automation", text)
	}
}
