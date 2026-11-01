package mcpapi

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func argumentString(data []byte, code, argument string) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", &mcptools.ToolError{Code: code, Message: argument + " must be a JSON string"}
	}
	return value, nil
}

// automationID is a canonical Hearth Automation ID argument.
type automationID string

func (id *automationID) UnmarshalJSON(data []byte) error {
	value, err := argumentString(data, "invalid_automation_id", "automation_id")
	if err != nil {
		return err
	}
	parsed, err := automations.ParseAutomationID(value)
	if err != nil {
		return &mcptools.ToolError{Code: "invalid_automation_id", Message: "automation_id is not canonical"}
	}
	*id = automationID(parsed)
	return nil
}

// expectedRevision is a required optimistic-concurrency revision argument.
type expectedRevision int64

func (revision *expectedRevision) UnmarshalJSON(data []byte) error {
	value, err := mcptools.DecodeInteger(data)
	if err != nil {
		return &mcptools.ToolError{Code: "invalid_revision", Message: "expected_revision must be an integer"}
	}
	if value < 1 {
		return &mcptools.ToolError{Code: "invalid_revision", Message: "expected_revision must be at least 1"}
	}
	*revision = expectedRevision(value)
	return nil
}
