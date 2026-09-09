package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type automationCursor struct {
	Version  int    `json:"v"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
}
type automationRunCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	AutomationID string `json:"automation_id"`
	StartedAt    string `json:"started_at"`
	ID           string `json:"id"`
}

var errAutomationCursor = errors.New("invalid automation cursor")

func encodeAutomationCursor(cursor any) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decodeAutomationCursor(value string, cursor any) error {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || value == "" || base64.RawURLEncoding.EncodeToString(raw) != value {
		return errAutomationCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(cursor); err != nil {
		return errAutomationCursor
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errAutomationCursor
	}
	// Canonical JSON also rejects duplicate members, alternate spellings and omissions.
	canonical, err := encodeAutomationCursor(cursor)
	if err != nil || canonical != value {
		return errAutomationCursor
	}
	return nil
}
func automationListPosition(value string) (*automations.AutomationID, error) {
	var cursor automationCursor
	if err := decodeAutomationCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != 1 || cursor.Resource != "automations" {
		return nil, errAutomationCursor
	}
	id, err := automations.ParseAutomationID(cursor.ID)
	return &id, err
}
func automationRunListPosition(value, filter string) (*time.Time, *automations.AutomationRunID, error) {
	var cursor automationRunCursor
	if err := decodeAutomationCursor(value, &cursor); err != nil {
		return nil, nil, err
	}
	if cursor.Version != 1 || cursor.Resource != "automation_runs" || cursor.AutomationID != filter {
		return nil, nil, errAutomationCursor
	}
	id, err := automations.ParseAutomationRunID(cursor.ID)
	if err != nil {
		return nil, nil, errAutomationCursor
	}
	startedAt, err := time.Parse(time.RFC3339Nano, cursor.StartedAt)
	if err != nil || startedAt.IsZero() || startedAt.UTC().Format(time.RFC3339Nano) != cursor.StartedAt {
		return nil, nil, errAutomationCursor
	}
	return &startedAt, &id, nil
}
