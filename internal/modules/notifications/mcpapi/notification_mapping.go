package mcpapi

import "github.com/mholtzscher/hearth/internal/modules/notifications"

func notificationSummary(a notifications.Attempt) NotificationSummary {
	status := string(a.Outcome)
	if status == "" {
		status = "running"
	}
	completedAt := a.CompletedAt
	if completedAt != nil {
		utc := completedAt.UTC()
		completedAt = &utc
	}
	return NotificationSummary{
		NotificationID:  string(a.ID),
		DestinationID:   string(a.DestinationID),
		DestinationName: a.DestinationName,
		Title:           a.Message.Title,
		Status:          status,
		StartedAt:       a.StartedAt.UTC(),
		CompletedAt:     completedAt,
		ReasonCode:      a.ReasonCode,
	}
}
