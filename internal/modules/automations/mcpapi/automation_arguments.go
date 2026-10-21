package mcpapi

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// The argument types below mirror the Devices MCP custom scalars: a dedicated
// type with an UnmarshalJSON decoder rejects a malformed scalar while the SDK
// decodes tool arguments, so no automation handler or service call ever sees one.
// The stable scalar failure code prefixes every message,
// because the SDK renders a decoding failure as the tool result text and that
// text is the only place the code crosses the MCP boundary.

// argumentString decodes one string-shaped argument. A non-string value is
// rejected with the stable code and argument name of the scalar being decoded,
// because the SDK renders the decoder error as prose the client cannot branch
// on otherwise.
func argumentString(data []byte, code, argument string) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("%s: %s must be a JSON string", code, argument)
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
		return errors.New("invalid_automation_id: automation_id is not canonical")
	}
	*id = automationID(parsed)
	return nil
}

// expectedRevision is a required optimistic-concurrency revision argument.
type expectedRevision int64

func (revision *expectedRevision) UnmarshalJSON(data []byte) error {
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("invalid_revision: expected_revision must be an integer")
	}
	if value < 1 {
		return errors.New("invalid_revision: expected_revision must be at least 1")
	}
	*revision = expectedRevision(value)
	return nil
}
