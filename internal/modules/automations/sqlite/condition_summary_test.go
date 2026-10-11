package sqlite_test

import (
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestHistorySummaryValidatesColumnsWithoutDecodingDecision(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, assignment string
	}{
		{"missing evaluated result", "condition_mode = 'evaluated', condition_result = NULL"},
		{"unexpected unevaluated result", "condition_mode = 'not_configured', condition_result = 'true'"},
		{"unrecorded bypass", "condition_mode = 'bypassed', condition_bypassed = 0"},
		{"unexpected bypass flag", "condition_mode = 'not_configured', condition_bypassed = 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			automationID, runID := newAutomationIDString(t), newRunIDString(t)
			mustExec(t, database, insertSummaryRunSQL,
				runID, automationID, migrationTimestamp,
				newFactIDString(t), string(newEntityID(t)), newObservationIDString(t), migrationTimestamp,
				`{"name":"unreadable snapshot"}`, "device_fact", migrationTimestamp, migrationTimestamp,
				`{"unreadable":"decision"}`,
			)
			params := automations.ListHistoryParams{AutomationID: automations.AutomationID(automationID), Limit: 10}
			page, err := repository.ListHistory(t.Context(), params)
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("summary decoded a full document: %+v, %v", page, err)
			}
			if _, valid := page.Items[0].ConditionSummary.(automations.NotConfiguredSummary); !valid {
				t.Fatalf("summary = %#v, want not_configured", page.Items[0].ConditionSummary)
			}
			mustExec(t, database, "UPDATE automation_history SET "+test.assignment+" WHERE id = ?", runID)
			if _, err = repository.ListHistory(t.Context(), params); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Fatalf("contradictory Condition summary: %v", err)
			}
		})
	}
}
