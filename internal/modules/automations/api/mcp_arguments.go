package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// The argument types below mirror the Devices MCP custom scalars: a dedicated
// type with an UnmarshalJSON decoder rejects a malformed scalar while the SDK
// decodes tool arguments, so no automation handler or service call ever sees
// one. The stable failure code the Huma handler would report prefixes every
// message, because the SDK renders a decoding failure as the tool result text.

// mcpArgumentString decodes one identifier-shaped JSON string argument.
func mcpArgumentString(data []byte) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", errors.New("argument must be a JSON string")
	}
	return value, nil
}

// mcpAutomationID is a canonical Hearth Automation ID argument.
type mcpAutomationID string

// UnmarshalJSON decodes and validates one Automation ID argument.
func (id *mcpAutomationID) UnmarshalJSON(data []byte) error {
	value, err := mcpArgumentString(data)
	if err != nil {
		return err
	}
	parsed, err := automations.ParseAutomationID(value)
	if err != nil {
		return errors.New("invalid_automation_id: automation_id is not canonical")
	}
	*id = mcpAutomationID(parsed)
	return nil
}

// mcpEntryID is a canonical Hearth Run or Skip ID argument.
type mcpEntryID string

// UnmarshalJSON decodes and validates one history entry ID argument.
func (id *mcpEntryID) UnmarshalJSON(data []byte) error {
	value, err := mcpArgumentString(data)
	if err != nil {
		return err
	}
	if !validEntryID(value) {
		return errors.New("invalid_entry_id: entry_id is not a canonical history ID")
	}
	*id = mcpEntryID(value)
	return nil
}

// mcpPageLimit is a page-size argument bounded to the range the Huma query
// parameter accepts. Decoding rejects an out-of-range value; the zero value
// means the argument was omitted.
type mcpPageLimit int

// UnmarshalJSON decodes and range-checks one page-size argument.
func (limit *mcpPageLimit) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("invalid_limit: limit must be an integer")
	}
	if value < 1 || value > mcpPageMaximumLimit {
		return errors.New("invalid_limit: limit must be between 1 and 200")
	}
	*limit = mcpPageLimit(value)
	return nil
}

// pageSize returns the effective page size for one limit argument, applying the
// default Huma and MCP share.
func (limit mcpPageLimit) pageSize() int {
	if limit == 0 {
		return mcpPageDefaultLimit
	}
	return int(limit)
}

// mcpExpectedRevision is a required optimistic-concurrency revision argument.
type mcpExpectedRevision int64

// UnmarshalJSON decodes and validates one expected revision argument.
func (revision *mcpExpectedRevision) UnmarshalJSON(data []byte) error {
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("invalid_revision: expected_revision must be an integer")
	}
	if value < 1 {
		return errors.New("invalid_revision: expected_revision must be at least 1")
	}
	*revision = mcpExpectedRevision(value)
	return nil
}

// mcpDefinitionArgument extracts the exact strict definition document from one
// raw tool request.
//
// The SDK's typed argument pipeline decodes every argument number through
// float64 before a handler sees it, so it cannot carry a value above 2^53
// unchanged. The untouched request bytes can: this reads the definition member
// as a [json.RawMessage] and hands the canonical strict decoder the same raw JSON
// the Huma request body carries, so the persisted document keeps its exact
// literals. A literal outside the float64 range is still rejected by the SDK's
// own argument validation before any handler runs.
func mcpDefinitionArgument(arguments json.RawMessage) (automations.Definition, error) {
	var envelope struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(arguments, &envelope); err != nil {
		return automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_definition", "definition must be a JSON object",
		)
	}
	return decodeDefinitionBody(envelope.Definition)
}
