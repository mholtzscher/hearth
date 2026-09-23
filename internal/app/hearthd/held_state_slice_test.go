package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// TestHeldStateObservationProducesHistoryThroughCore protects A9's end-to-end
// contract: an accepted synthetic Observation starts a held Trigger, the
// app-owned scheduler admits it after its deadline, and the existing history API
// reports held-state evidence instead of a device-Fact summary. It fails if the
// Observation relay, hold persistence, scheduler, held admission, or history
// projection is disconnected.
//
//nolint:paralleltest // A full Core and broker are timing-sensitive under concurrent slice tests.
func TestHeldStateObservationProducesHistoryThroughCore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	httpAddress := unusedLoopbackAddress(t)

	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(),
			SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
			Agent:      requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler))
	}()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)

	device := startSliceAdapter(ctx, t, server, httpAddress)
	defer device.stop()
	automationID := createHeldStateSliceAutomation(ctx, t, httpAddress, device.powerEntityID)
	if err := device.publish(ctx); err != nil {
		t.Fatal(err)
	}

	historyID := waitForHeldStateHistory(ctx, t, httpAddress, automationID)
	assertHeldStateHistoryEvidence(ctx, t, httpAddress, automationID, historyID)
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
		"steps": [{"id":"turn_off","entity_id":%q,"operation":"set","parameters":{"value":false}}]
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
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Status string `json:"status"`
				Source string `json:"source"`
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
		if item.Kind != "run" || item.Source != "held_state" || item.Status == "running" {
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
	httpAddress, automationID, historyID string,
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
			Source            string          `json:"source"`
			Status            string          `json:"status"`
			MatchedTriggerIDs []string        `json:"matched_trigger_ids"`
			Fact              json.RawMessage `json:"fact"`
			HeldState         *struct {
				TriggerID string    `json:"trigger_id"`
				StartedAt time.Time `json:"started_at"`
				DueAt     time.Time `json:"due_at"`
			} `json:"held_state"`
		} `json:"run"`
	}
	if err := json.NewDecoder(response.Body).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	if entry.Kind != "run" || entry.Run == nil {
		t.Fatalf("history detail = %#v, want Run", entry)
	}
	run := entry.Run
	if run.Source != "held_state" || run.Status != "succeeded" {
		t.Fatalf("held-state Run source/status = %q/%q", run.Source, run.Status)
	}
	if len(run.MatchedTriggerIDs) != 1 || run.MatchedTriggerIDs[0] != "light_on" {
		t.Fatalf("matched Trigger IDs = %v, want [light_on]", run.MatchedTriggerIDs)
	}
	if len(run.Fact) != 0 || run.HeldState == nil {
		t.Fatalf("held-state evidence = fact %s, held_state %#v; want only held-state evidence",
			run.Fact, run.HeldState)
	}
	evidence := run.HeldState
	if evidence.TriggerID != "light_on" || evidence.StartedAt.IsZero() || evidence.DueAt.IsZero() {
		t.Fatalf("held-state evidence = %#v", evidence)
	}
	if got := evidence.DueAt.Sub(evidence.StartedAt); got != time.Second {
		t.Fatalf("held-state evidence duration = %s, want 1s", got)
	}
}
