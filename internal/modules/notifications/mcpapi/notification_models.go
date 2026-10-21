package mcpapi

import "time"

type NotificationSummary struct {
	NotificationID  string     `json:"notification_id"`
	DestinationID   string     `json:"destination_id"`
	DestinationName string     `json:"destination_name"`
	Title           string     `json:"title"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	ReasonCode      string     `json:"reason_code,omitempty"`
}
