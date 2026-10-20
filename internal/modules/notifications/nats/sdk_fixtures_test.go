package nats_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func sendInput(t *testing.T, id string) notifications.SendInput {
	t.Helper()
	attemptID, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return notifications.SendInput{
		AttemptID:     attemptID,
		DestinationID: notifications.DestinationID(id),
		Message:       notifications.Message{Title: "Freezer", Text: "Too warm"},
	}
}
