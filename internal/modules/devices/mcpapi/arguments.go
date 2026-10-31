package mcpapi

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// The tools describe their arguments with flat JSON structs.
// Decoding validates what the schema cannot express here: a canonical ID and a
// bounded page size are rejected as invalid tool input, so no handler runs for
// them. Each rejection carries the field's stable modeled failure code.
// JSONObject rejects non-object parameters. A well-shaped but unsupported
// operation name reaches the domain handler, which reports invalid_request.
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
	value, err := mcptools.DecodeInteger(data)
	if err != nil || value < minPageLimit || value > maxPageLimit {
		return decodeFailure(failureInvalidLimit)
	}
	*limit = pageLimit(value)
	return nil
}

// decodeFailure maps a malformed scalar without publishing submitted values.
func decodeFailure(code failureCode) error {
	message := "invalid tool argument"
	//exhaustive:ignore
	switch code {
	case failureInvalidLimit:
		message = "limit must be an integer between 1 and 200"
	case failureInvalidEntityID:
		message = "entity_id must be a canonical Hearth Entity ID"
	case failureInvalidDeviceID:
		message = "device_id must be a canonical Hearth Device ID"
	case failureInvalidCommandID:
		message = "command_id must be a canonical Hearth Command ID"
	}
	return toolError(code, message)
}

// pageSize returns the effective page size for one MCP limit argument,
// applying the default collection page size.
func pageSize(limit pageLimit) int {
	if limit == 0 {
		return defaultPageLimit
	}
	return int(limit)
}

// PageInput carries the opaque cursor and bounded page size every paginated
// tool accepts. Each handler validates its endpoint scope before calling the service.
type PageInput struct {
	Limit  pageLimit `json:"limit,omitempty"  doc:"page size from 1 to 200; defaults to 50"`
	Cursor string    `json:"cursor,omitempty" doc:"opaque cursor from a previous page's next_cursor"`
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
		return decodeFailure(failureInvalidEntityID)
	}
	parsed, err := devices.ParseEntityID(value)
	if err != nil {
		return decodeFailure(failureInvalidEntityID)
	}
	*id = entityID(parsed)
	return nil
}

// deviceID is a canonical Hearth Device ID argument.
type deviceID string

func (id *deviceID) UnmarshalJSON(data []byte) error {
	value, err := idValue(data)
	if err != nil {
		return decodeFailure(failureInvalidDeviceID)
	}
	parsed, err := devices.ParseDeviceID(value)
	if err != nil {
		return decodeFailure(failureInvalidDeviceID)
	}
	*id = deviceID(parsed)
	return nil
}

// commandID is a canonical Hearth Command ID argument.
type commandID string

func (id *commandID) UnmarshalJSON(data []byte) error {
	value, err := idValue(data)
	if err != nil {
		return decodeFailure(failureInvalidCommandID)
	}
	parsed, err := devices.ParseCommandID(value)
	if err != nil {
		return decodeFailure(failureInvalidCommandID)
	}
	*id = commandID(parsed)
	return nil
}
