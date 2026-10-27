package httpapi_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// Script only the external notification seam; real Automation execution and
// SQLite own the evidence read through the HTTP boundary.
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
	executions := contractExecutionsOfKind(t, retained["executions"].([]any), "notification")
	if len(executions) != 1 {
		t.Fatalf("typed evidence = %v", retained)
	}
	notification := executions[0]
	evidence := notification["evidence"].(map[string]any)
	expectedPhase := "failed"
	if outcome == notifications.Accepted {
		expectedPhase = "succeeded"
	}
	if notification["step_id"] != "notify" || notification["sequence"] != json.Number("1") ||
		notification["phase"] != expectedPhase || evidence["outcome"] != string(outcome) ||
		evidence["attempt_id"] == "" || notification["completed_at"] == nil {
		t.Fatalf("notification evidence = %v", notification)
	}
	if reason == "" {
		if _, found := evidence["reason_code"]; found {
			t.Fatal("accepted attempt invented reason")
		}
	} else if evidence["reason_code"] != reason {
		t.Fatalf("reason lost: %v", notification)
	}
}
