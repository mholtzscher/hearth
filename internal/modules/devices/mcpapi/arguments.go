package mcpapi

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The tools describe their arguments with flat JSON structs.
// Decoding validates what the schema cannot express here: a canonical ID and a
// bounded page size are rejected as invalid tool input, so no handler runs for
// them. Each rejection leads its error text with the field's stable failure
// code, such as invalid_entity_id, because the SDK publishes no structured
// content for a rejected call. A non-object parameters document is rejected by
// the schema, and a well-shaped but unsupported operation name reaches the
// domain handler, which reports invalid_request; neither is a decode rejection.
// Each handler constructs its own domain query.

const (
	// defaultPageLimit matches the collection default for an omitted limit.
	defaultPageLimit = 50
	// minPageLimit and maxPageLimit define the collection page size range.
	minPageLimit = 1
	maxPageLimit = 200
)

// pageLimit is a page size argument bounded to the range every list
// query accepts. Decoding rejects an out-of-range value; the zero value means
// the argument was omitted.
type pageLimit int

func (limit *pageLimit) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return decodeFailure(failureInvalidLimit, err)
	}
	checked, err := pageLimitValue(value)
	if err != nil {
		return decodeFailure(failureInvalidLimit, err)
	}
	*limit = checked
	return nil
}

// decodeFailure wraps one decode-time malformed scalar in its stable failure
// code. The SDK rejects the call before the handler runs and renders the error
// text as the isError result with no structured content, so the code must lead
// the text for a client to branch on.
func decodeFailure(code failureCode, err error) error {
	return fmt.Errorf("%s: %w", code, err)
}

func pageLimitValue(value int) (pageLimit, error) {
	if value < minPageLimit || value > maxPageLimit {
		return 0, fmt.Errorf("limit must be between %d and %d", minPageLimit, maxPageLimit)
	}
	return pageLimit(value), nil
}

// pageSize returns the effective page size for one MCP limit argument,
// applying the default collection page size.
func pageSize(limit pageLimit) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return int(limit)
}

// pageInput carries the opaque cursor and bounded page size every paginated
// tool accepts. Each handler validates its endpoint scope before calling the service.
type pageInput struct {
	Limit  pageLimit `json:"limit,omitempty"  jsonschema:"page size from 1 to 200; defaults to 50"`
	Cursor string    `json:"cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_cursor"`
}

func idValue(data []byte) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	return value, nil
}

// entityID is a canonical Hearth Entity ID argument.
type entityID string

func (id *entityID) UnmarshalJSON(data []byte) error {
	value, err := idValue(data)
	if err != nil {
		return decodeFailure(failureInvalidEntityID, err)
	}
	parsed, err := devices.ParseEntityID(value)
	if err != nil {
		return decodeFailure(failureInvalidEntityID, err)
	}
	*id = entityID(parsed)
	return nil
}

// deviceID is a canonical Hearth Device ID argument.
type deviceID string

func (id *deviceID) UnmarshalJSON(data []byte) error {
	value, err := idValue(data)
	if err != nil {
		return decodeFailure(failureInvalidDeviceID, err)
	}
	parsed, err := devices.ParseDeviceID(value)
	if err != nil {
		return decodeFailure(failureInvalidDeviceID, err)
	}
	*id = deviceID(parsed)
	return nil
}

// commandID is a canonical Hearth Command ID argument.
type commandID string

func (id *commandID) UnmarshalJSON(data []byte) error {
	value, err := idValue(data)
	if err != nil {
		return decodeFailure(failureInvalidCommandID, err)
	}
	parsed, err := devices.ParseCommandID(value)
	if err != nil {
		return decodeFailure(failureInvalidCommandID, err)
	}
	*id = commandID(parsed)
	return nil
}
