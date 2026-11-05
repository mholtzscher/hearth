//go:build integration

package httpapi_test

import "testing"

func TestFunctionHTTPDelayRunHasOnlyDelayExecution(t *testing.T) {
	t.Parallel()
	router, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
	body := writeDelayDefinition(
		t,
		router,
		session,
		"HTTP",
		"",
		delayDocument(`{"id":"wait","kind":"delay","duration_ms":1}`),
	)
	id := body["id"].(string)
	started := startDelayRun(t, router, session, "HTTP", id)
	runID := started["id"].(string)
	waitForAPI(t, service, id, runID)
	entry := pageObject(t, restJSON(t, router, "/v1/automations/"+id+"/history/"+runID))
	run := entry["run"].(map[string]any)
	if run["status"] != "succeeded" || len(run["executions"].([]any)) != 1 ||
		run["executions"].([]any)[0].(map[string]any)["kind"] != "delay" {
		t.Fatalf("Delay Run lacks its native execution: %v", run)
	}
}
