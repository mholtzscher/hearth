package api

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	mcpDefaultPageLimit = 50
	mcpMinPageLimit     = 1
	mcpMaxPageLimit     = 200
)

type mcpPageLimit int

func (limit *mcpPageLimit) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return mcpDecodeFailure(mcpFailureInvalidLimit, err)
	}
	checked, err := mcpPageLimitValue(value)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidLimit, err)
	}
	*limit = checked
	return nil
}

func mcpDecodeFailure(code mcpFailureCode, err error) error { return fmt.Errorf("%s: %w", code, err) }

func mcpPageLimitValue(value int) (mcpPageLimit, error) {
	if value < mcpMinPageLimit || value > mcpMaxPageLimit {
		return 0, fmt.Errorf("limit must be between %d and %d", mcpMinPageLimit, mcpMaxPageLimit)
	}
	return mcpPageLimit(value), nil
}

func mcpPageSize(limit mcpPageLimit) int {
	if limit == 0 {
		return mcpDefaultPageLimit
	}
	return int(limit)
}

type mcpPageInput struct {
	Limit  mcpPageLimit `json:"limit,omitempty"  jsonschema:"page size from 1 to 200; defaults to 50"`
	Cursor string       `json:"cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_cursor"`
}

type mcpAdapterID string

func (id *mcpAdapterID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return mcpDecodeFailure(mcpFailureInvalidAdapterID, err)
	}
	if !validAdapterID(value) {
		return mcpDecodeFailure(mcpFailureInvalidAdapterID, errors.New("adapter_id must be a subject-safe slug"))
	}
	*id = mcpAdapterID(value)
	return nil
}

type mcpListAdaptersInput struct{ mcpPageInput }
type mcpGetAdapterInput struct {
	AdapterID mcpAdapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}
type mcpListAdapterHealthHistoryInput struct {
	mcpPageInput

	AdapterID mcpAdapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}
