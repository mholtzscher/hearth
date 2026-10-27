package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
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
			"id":"mode-allowed","kind":"entity_state","entity_id":%q,"value_pointer":"/mode",
			"operator":"eq","operand":"allowed","max_age_seconds":300
		},
		"steps": [{"kind":"command", "id":"step_0","entity_id":%q,"operation":"set","parameters":{"value":true}}]
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
		"steps": [{"kind":"command", "id":"step_0","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, string(triggerEntity), rawConditions, string(actionEntity))
}

// historyEntryIDs lists one Automation's retained Run and Skip IDs over HTTP.
func historyEntryIDs(t *testing.T, router http.Handler, automationID string) []string {
	t.Helper()
	response := performJSON(router, http.MethodGet, "/v1/automations/"+automationID+"/history", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list history status = %d: %s", response.Code, response.Body.String())
	}
	var page AutomationHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids
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
			"id":"leaf","kind":"entity_state","entity_id":%q,"value_pointer":%s,
			"operator":%q,"operand":%s
		},
		"steps": [{"kind":"command", "id":"step_0","entity_id":%q,"operation":"set","parameters":{"value":true}}]
	}`, string(triggerEntity), string(conditionEntity), pointer, operator, operand, string(actionEntity))
}

// historySummaryFor returns the listing projection for one retained entry.
func historySummaryFor(
	t *testing.T,
	router http.Handler,
	automationID string,
	entryID string,
) AutomationHistorySummaryBody {
	t.Helper()
	response := performJSON(router, http.MethodGet, "/v1/automations/"+automationID+"/history", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list history status = %d: %s", response.Code, response.Body.String())
	}
	var page AutomationHistoryCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == entryID {
			return item
		}
	}
	t.Fatalf("history %s does not contain entry %s", automationID, entryID)
	return AutomationHistorySummaryBody{}
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
			if body, ok := item.Body.(automations.RunHistorySummary); ok && body.Status == automations.RunRunning {
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
