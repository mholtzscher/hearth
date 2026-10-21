package httpapi

import "github.com/mholtzscher/hearth/internal/modules/notifications"

func attemptBody(a notifications.Attempt) AttemptBody {
	status := string(a.Outcome)
	if status == "" {
		status = "running"
	}
	return AttemptBody{ID: string(a.ID), DestinationID: string(a.DestinationID), DestinationName: a.DestinationName,
		Title: a.Message.Title, Message: a.Message.Text, Status: status, StartedAt: a.StartedAt.UTC(),
		Deadline: a.Deadline.UTC(), CompletedAt: a.CompletedAt, ReasonCode: a.ReasonCode, Receipt: a.Receipt}
}
