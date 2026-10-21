package httpapi

import "time"

// AttemptBody deliberately excludes the private adapter, key and runtime route.
type AttemptBody struct {
	ID              string     `json:"id"`
	DestinationID   string     `json:"destination_id"`
	DestinationName string     `json:"destination_name"`
	Title           string     `json:"title"`
	Message         string     `json:"message"`
	Status          string     `json:"status"                enum:"running,accepted,rejected,not_sent,unknown"`
	StartedAt       time.Time  `json:"started_at"`
	Deadline        time.Time  `json:"deadline"`
	CompletedAt     *time.Time `json:"completed_at"`
	ReasonCode      string     `json:"reason_code,omitempty"`
	Receipt         string     `json:"receipt,omitempty"`
}
