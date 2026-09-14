package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// cursorVersion is the only accepted cursor document version.
const cursorVersion = 1

// automationCursor positions an ID-ascending Automation page.
type automationCursor struct {
	Version  int    `json:"v"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
}

// historyCursor positions a newest-first history page within one Automation.
type historyCursor struct {
	Version      int    `json:"v"`
	Resource     string `json:"resource"`
	AutomationID string `json:"automation_id"`
	RecordedAt   string `json:"recorded_at"`
	ID           string `json:"id"`
}

func encodeAutomationsCursor(id automations.AutomationID) (string, error) {
	return encodeCursor(automationCursor{Version: cursorVersion, Resource: "automations", ID: string(id)})
}

func decodeAutomationsCursor(value string) (*automations.AutomationID, error) {
	var cursor automationCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "automations" {
		return nil, errors.New("invalid automation cursor scope")
	}
	id, err := automations.ParseAutomationID(cursor.ID)
	if err != nil {
		return nil, fmt.Errorf("invalid automation cursor ID: %w", err)
	}
	return &id, nil
}

func encodeHistoryCursor(
	automationID automations.AutomationID,
	recordedAt time.Time,
	id string,
) (string, error) {
	return encodeCursor(historyCursor{
		Version:      cursorVersion,
		Resource:     "automation_history",
		AutomationID: string(automationID),
		RecordedAt:   recordedAt.UTC().Format(time.RFC3339Nano),
		ID:           id,
	})
}

func decodeHistoryCursor(
	value string,
	automationID automations.AutomationID,
) (*time.Time, *string, error) {
	var cursor historyCursor
	if err := decodeCursor(value, &cursor); err != nil {
		return nil, nil, err
	}
	if cursor.Version != cursorVersion || cursor.Resource != "automation_history" ||
		cursor.AutomationID != string(automationID) {
		return nil, nil, errors.New("invalid history cursor scope")
	}
	recordedAt, err := time.Parse(time.RFC3339Nano, cursor.RecordedAt)
	if err != nil || recordedAt.IsZero() || cursor.RecordedAt != recordedAt.UTC().Format(time.RFC3339Nano) {
		return nil, nil, errors.New("invalid history cursor timestamp")
	}
	if cursor.ID == "" {
		return nil, nil, errors.New("invalid history cursor ID")
	}
	return &recordedAt, &cursor.ID, nil
}

func encodeCursor(cursor any) (string, error) {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value string, cursor any) error {
	if value == "" {
		return errors.New("cursor is empty")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("decode cursor: %w", err)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		return errors.New("cursor encoding is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(cursor); decodeErr != nil {
		return fmt.Errorf("decode cursor document: %w", decodeErr)
	}
	if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
		return errors.New("cursor contains trailing JSON")
	}
	return nil
}
