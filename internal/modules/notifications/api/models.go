// Package api owns the public HTTP and MCP notification contracts.
package api

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type DestinationBody struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

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

type DestinationCollectionBody struct {
	Items      []DestinationBody `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}
type AttemptCollectionBody struct {
	Items      []AttemptBody `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type SendBody struct {
	Title   string `json:"title,omitempty" maxLength:"256"`
	Message string `json:"message"         maxLength:"2000" minLength:"1"`
}

// UnmarshalJSON closes Go's null-string and invalid Unicode coercions as well as
// rejecting unknown members, including public attempt identities.
func (body *SendBody) UnmarshalJSON(data []byte) error {
	// Check raw escapes before encoding/json can replace unpaired surrogates.
	// Preserve the existing duplicate-member policy while validating Unicode.
	if !jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true)) {
		return errors.New("send body must contain valid JSON and Unicode")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return errors.New("send body must be a JSON object")
	}
	var decoded SendBody
	for key, value := range members {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("send fields cannot be null")
		}
		switch key {
		case "title":
			if err := json.Unmarshal(value, &decoded.Title); err != nil {
				return errors.New("title must be a string")
			}
		case "message":
			if err := json.Unmarshal(value, &decoded.Message); err != nil {
				return errors.New("message must be a string")
			}
		default:
			return errors.New("unknown send field")
		}
	}
	if _, ok := members["message"]; !ok {
		return errors.New("message is required")
	}
	*body = decoded
	return nil
}

func destinationBody(d notifications.Destination) DestinationBody {
	return DestinationBody{ID: string(d.ID), Name: d.Name, Available: d.Available}
}
func attemptBody(a notifications.Attempt) AttemptBody {
	status := string(a.Outcome)
	if status == "" {
		status = "running"
	}
	return AttemptBody{ID: string(a.ID), DestinationID: string(a.DestinationID), DestinationName: a.DestinationName,
		Title: a.Message.Title, Message: a.Message.Text, Status: status, StartedAt: a.StartedAt.UTC(),
		Deadline: a.Deadline.UTC(), CompletedAt: a.CompletedAt, ReasonCode: a.ReasonCode, Receipt: a.Receipt}
}
func nextCursor(cursor string) *string {
	if cursor == "" {
		return nil
	}
	return &cursor
}
