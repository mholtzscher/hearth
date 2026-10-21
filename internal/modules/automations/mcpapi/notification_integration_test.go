//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestNotificationDefinitionAndDirectHistory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		outcome notifications.Outcome
		reason  string
	}{
		{notifications.Accepted, ""}, {notifications.Rejected, "provider_rejected"}, {notifications.NotSent, "destination_unavailable"}, {notifications.Unknown, "outcome_timeout"},
	} {
		t.Run(string(test.outcome), func(t *testing.T) {
			t.Parallel()
			destination, err := notifications.NewDestinationID()
			if err != nil {
				t.Fatal(err)
			}
			stub := &apiNotifications{destination: destination, outcome: test.outcome, reason: test.reason}
			service := newAutomationService(t, newAPIDevices(), automations.Dependencies{Notifications: stub})
			session := connectAutomationMCP(t, service)
			leaf := fmt.Sprintf(
				`{"id":"notify","kind":"notification","destination_id":%q,"title":"Warning","message":"Static message"}`,
				destination,
			)
			document := delayDocument(
				`{"id":"route","kind":"if","conditions":{"id":"manual","kind":"not","child":{"id":"tick-match","kind":"trigger","trigger_ids":["tick"]}},"then":[` + leaf + `]}`,
			)
			created := exactToolBody(
				t,
				callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
			)
			id := created["automation_id"].(string)
			exactToolBody(
				t,
				callAutomationTool(
					t,
					session,
					"replace_automation",
					json.RawMessage(
						fmt.Sprintf(`{"automation_id":%q,"expected_revision":1,"definition":%s}`, id, document),
					),
				),
			)
			read := exactToolBody(
				t,
				callAutomationTool(t, session, "get_automation", map[string]any{"automation_id": id}),
			)
			if canonicalJSON(t, read["definition"]) != canonicalJSON(t, exactJSONObject(t, document)) {
				t.Fatal("notification definition lost")
			}
			started := exactToolBody(
				t,
				callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id}),
			)
			runID := started["run_id"].(string)
			waitForAPI(t, service, id, runID)
			run := exactToolBody(
				t,
				callAutomationTool(
					t,
					session,
					"get_automation_history_entry",
					map[string]any{"automation_id": id, "entry_id": runID},
				),
			)
			assertNotificationEvidence(t, run, test.outcome, test.reason)
			invalid := exactJSONObject(t, document)
			invalid["steps"].([]any)[0].(map[string]any)["then"].([]any)[0].(map[string]any)["operation"] = "set"
			tools := delayTools(t, session)
			for _, tool := range []string{"create_automation", "replace_automation"} {
				schema := compileDelayContractSchema(t, inputSchemaDefinition(t, tool, tools[tool]), "")
				if schema.Validate(invalid) == nil {
					t.Fatal("discovery accepted mixed notification/Command")
				}
			}
			raw, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			if result := callAutomationTool(
				t,
				session,
				"create_automation",
				json.RawMessage(`{"definition":`+string(raw)+`}`),
			); !result.IsError {
				t.Fatal("accepted mixed notification/Command")
			}
		})
	}
}
