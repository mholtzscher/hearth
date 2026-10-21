package httpapi

import ()

type CommandRecordBody struct {
	ID                   string         `json:"id"`
	EntityID             string         `json:"entity_id"`
	Operation            string         `json:"operation"`
	Parameters           map[string]any `json:"parameters"`
	Status               string         `json:"status"`
	RequestedAt          string         `json:"requested_at"`
	DeadlineAt           string         `json:"deadline_at"`
	AcceptedAt           *string        `json:"accepted_at,omitempty"`
	CompletedAt          *string        `json:"completed_at,omitempty"`
	OutcomeObservationID *string        `json:"outcome_observation_id,omitempty"`
	FailureCode          *string        `json:"failure_code,omitempty"`
}

type CommandCollectionBody struct {
	Items      []CommandRecordBody `json:"items"`
	NextCursor *string             `json:"next_cursor,omitempty"`
}
