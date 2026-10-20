package api_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

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

func (stub *apiNotifications) GetAttempt(_ context.Context, id notifications.AttemptID) (notifications.Attempt, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.attempt.ID != id {
		return notifications.Attempt{}, notifications.ErrAttemptNotFound
	}
	return stub.attempt, nil
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
