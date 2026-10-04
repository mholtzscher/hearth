package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestHeldStateObservationProducesHistoryThroughCore protects A9's end-to-end
// contract: an accepted synthetic Observation starts a held Trigger, the
// app-owned scheduler admits it after its deadline, and the existing history API
// reports held-state evidence instead of a device-Fact summary. The recorded
// Trigger must select its branch and produce a verified Command. It fails if the
// Observation relay, hold persistence, scheduler, held admission, or history
// projection is disconnected.
//
//nolint:paralleltest // A full Core and broker are timing-sensitive under concurrent slice tests.
func TestHeldStateObservationProducesHistoryThroughCore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	httpAddress, stopCore, runErrors := startDeviceFactsCore(
		ctx, t, server.ClientURL(), filepath.Join(t.TempDir(), "hearth.db"),
	)
	defer stopDeviceFactsCore(t, stopCore, runErrors)
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)

	device := startSliceAdapter(ctx, t, server, httpAddress)
	defer func() { device.stop(); _ = device.session.Close() }()
	automationID := createHeldStateSliceAutomation(ctx, t, httpAddress, device.powerEntityID)
	if err := device.publish(ctx); err != nil {
		t.Fatal(err)
	}

	historyID := waitForHeldStateHistory(ctx, t, httpAddress, automationID)
	assertHeldStateHistoryEvidence(ctx, t, httpAddress, automationID, historyID, device.powerEntityID)
}

func createHeldStateSliceAutomation(
	ctx context.Context,
	t *testing.T,
	httpAddress, entityID string,
) string {
	t.Helper()
	definition := fmt.Sprintf(`{
		"name": "Turn off light after it stays on",
		"enabled": true,
		"triggers": [{"id":"light_on","kind":"held_state","entity_id":%q,
			"comparisons":[{"value_pointer":"","operator":"eq","operand":true}],
			"for_seconds":1}],
		"steps": [{"id":"route","kind":"if",
			"conditions":{"id":"held-source","kind":"trigger","trigger_ids":["light_on"]},
			"then":[{"kind":"command", "id":"turn_off","entity_id":%q,"operation":"set","parameters":{"value":false}}]}]
	}`, entityID, entityID)
	response := sliceRequest(ctx, t, http.MethodPost, httpAddress, "/v1/automations",
		bytes.NewBufferString(definition))
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create held-state automation status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created held-state automation has no identity")
	}
	return created.ID
}

func waitForHeldStateHistory(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID string,
) string {
	t.Helper()
	var historyID string
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
			"/v1/automations/"+automationID+"/history", nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false, fmt.Errorf("history status = %d", response.StatusCode)
		}
		var page struct {
			Items []struct {
				ID     string          `json:"id"`
				Kind   string          `json:"kind"`
				Status string          `json:"status"`
				Cause  conditionsCause `json:"cause"`
			} `json:"items"`
		}
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			return false, err
		}
		if len(page.Items) > 1 {
			return false, fmt.Errorf("held-state history contains %d outcomes, want exactly one", len(page.Items))
		}
		if len(page.Items) == 0 {
			return false, nil
		}
		item := page.Items[0]
		if item.Kind != "run" || item.Cause.Kind != "held_state" || item.Status == "running" {
			return false, nil
		}
		historyID = item.ID
		return true, nil
	})
	if historyID == "" {
		t.Fatal("held-state Run never reached terminal history")
	}
	return historyID
}

func assertHeldStateHistoryEvidence(
	ctx context.Context,
	t *testing.T,
	httpAddress, automationID, historyID, entityID string,
) {
	t.Helper()
	response := sliceRequest(ctx, t, http.MethodGet, httpAddress,
		"/v1/automations/"+automationID+"/history/"+historyID, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("held-state history detail status = %d, body = %s",
			response.StatusCode, readSliceBody(t, response))
	}
	var entry struct {
		Kind string `json:"kind"`
		Run  *struct {
			branchingHistoryRun
		} `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "run" || entry.Run == nil {
		t.Fatalf("history detail = %#v, want Run", entry)
	}
	run := entry.Run
	if run.Cause.Kind != "held_state" || run.Status != "succeeded" {
		t.Fatalf("held-state Run cause/status = %q/%q", run.Cause.Kind, run.Status)
	}
	assertBranchingTriggerEvidence(t, run.branchingHistoryRun, "held_state", []string{"light_on"}, []string{"light_on"})
	if run.Cause.Fact != nil || run.Cause.Evidence == nil {
		t.Fatalf("held-state evidence = fact %s, held_state %#v; want only held-state evidence",
			run.Cause.Fact, run.Cause.Evidence)
	}
	evidence := run.Cause.Evidence
	if evidence.TriggerID != "light_on" || evidence.StartedAt.IsZero() || evidence.DueAt.IsZero() {
		t.Fatalf("held-state evidence = %#v", evidence)
	}
	if got := evidence.DueAt.Sub(evidence.StartedAt); got != time.Second {
		t.Fatalf("held-state evidence duration = %s, want 1s", got)
	}
	if len(run.Steps) != 1 || run.Steps[0].Status != "satisfied" || run.Steps[0].VerifiedCommandID == nil {
		t.Fatalf("held command = %#v", run.Steps)
	}
	assertTerminalLinkedCommand(ctx, t, httpAddress, *run.Steps[0].VerifiedCommandID, entityID, false)
}
