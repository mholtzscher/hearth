package apicursor

import (
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/platform/pagination"
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

func EncodeAutomations(id automations.AutomationID) (string, error) {
	return pagination.EncodeCursor(automationCursor{Version: cursorVersion, Resource: "automations", ID: string(id)})
}

func DecodeAutomations(value string) (*automations.AutomationID, error) {
	cursor, err := pagination.DecodeCursor[automationCursor](value)
	if err != nil {
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

func EncodeHistory(
	automationID automations.AutomationID,
	recordedAt time.Time,
	id string,
) (string, error) {
	return pagination.EncodeCursor(historyCursor{
		Version:      cursorVersion,
		Resource:     "automation_history",
		AutomationID: string(automationID),
		RecordedAt:   recordedAt.UTC().Format(time.RFC3339Nano),
		ID:           id,
	})
}

func DecodeHistory(
	value string,
	automationID automations.AutomationID,
) (*time.Time, *string, error) {
	cursor, err := pagination.DecodeCursor[historyCursor](value)
	if err != nil {
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
