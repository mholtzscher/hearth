package api

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The MCP tools describe their arguments with flat JSON structs instead of the
// Huma request types, whose path and query tags do not describe a JSON Schema.
// Decoding validates what the schema cannot express here: a canonical ID and a
// bounded page size are rejected as invalid tool input, so no handler runs for
// them. Each rejection leads its error text with the field's stable failure
// code, such as invalid_entity_id, because the SDK publishes no structured
// content for a rejected call. A non-object parameters document is rejected by
// the schema, and a well-shaped but unsupported operation name reaches the
// domain handler, which reports invalid_command; neither is a decode rejection.
// Each tool then translates its arguments into the shared Huma request.

const (
	// mcpDefaultPageLimit matches the Huma default for an omitted limit.
	mcpDefaultPageLimit = 50
	// mcpMinPageLimit and mcpMaxPageLimit match the Huma page size range.
	mcpMinPageLimit = 1
	mcpMaxPageLimit = 200
)

// mcpPageLimit is a page size argument bounded to the range every Huma list
// query accepts. Decoding rejects an out-of-range value; the zero value means
// the argument was omitted.
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

// mcpDecodeFailure wraps one decode-time malformed scalar in its stable failure
// code. The SDK rejects the call before the handler runs and renders the error
// text as the isError result with no structured content, so the code must lead
// the text for a client to branch on.
func mcpDecodeFailure(code mcpFailureCode, err error) error {
	return fmt.Errorf("%s: %w", code, err)
}

func mcpPageLimitValue(value int) (mcpPageLimit, error) {
	if value < mcpMinPageLimit || value > mcpMaxPageLimit {
		return 0, fmt.Errorf("limit must be between %d and %d", mcpMinPageLimit, mcpMaxPageLimit)
	}
	return mcpPageLimit(value), nil
}

// mcpPageSize returns the effective page size for one MCP limit argument,
// applying the default Huma and MCP share.
func mcpPageSize(limit mcpPageLimit) int {
	if limit == 0 {
		return mcpDefaultPageLimit
	}
	return int(limit)
}

// mcpPageInput carries the opaque cursor and bounded page size every paginated
// tool accepts. MCP defines no cursor format: the cursor is forwarded to services
// uninterpreted, exactly as a Huma handler forwards it.
type mcpPageInput struct {
	Limit  mcpPageLimit `json:"limit,omitempty"  jsonschema:"page size from 1 to 200; defaults to 50"`
	Cursor string       `json:"cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_cursor"`
}

func mcpIDValue(data []byte) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	return value, nil
}

// mcpEntityID is a canonical Hearth Entity ID argument.
type mcpEntityID string

func (id *mcpEntityID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidEntityID, err)
	}
	parsed, err := devices.ParseEntityID(value)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidEntityID, err)
	}
	*id = mcpEntityID(parsed)
	return nil
}

// mcpDeviceID is a canonical Hearth Device ID argument.
type mcpDeviceID string

func (id *mcpDeviceID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidDeviceID, err)
	}
	parsed, err := devices.ParseDeviceID(value)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidDeviceID, err)
	}
	*id = mcpDeviceID(parsed)
	return nil
}

// mcpCommandID is a canonical Hearth Command ID argument.
type mcpCommandID string

func (id *mcpCommandID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidCommandID, err)
	}
	parsed, err := devices.ParseCommandID(value)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidCommandID, err)
	}
	*id = mcpCommandID(parsed)
	return nil
}

// mcpAdapterID is a subject-safe Adapter ID argument.
type mcpAdapterID string

func (id *mcpAdapterID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return mcpDecodeFailure(mcpFailureInvalidAdapterID, err)
	}
	if !validAdapterID(value) {
		return mcpDecodeFailure(
			mcpFailureInvalidAdapterID, errors.New("adapter_id must be a subject-safe slug"),
		)
	}
	*id = mcpAdapterID(value)
	return nil
}

// mcpOptionalDeviceID returns the string form of an optional Device ID filter.
// An explicit empty string is rejected while decoding as a malformed ID rather
// than treated as absent.
func mcpOptionalDeviceID(id *mcpDeviceID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

// mcpOptionalEntityID returns the string form of an optional Entity ID filter.
// An explicit empty string is rejected while decoding as a malformed ID rather
// than treated as absent.
func mcpOptionalEntityID(id *mcpEntityID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

// The tool argument structs below are MCP-only: they describe the JSON Schema
// clients see and translate mechanically into the shared Huma request type.
type mcpListEntitiesInput struct {
	mcpPageInput

	DeviceID *mcpDeviceID `json:"device_id,omitempty" jsonschema:"canonical Hearth Device ID; omit to list every Device"`
}

type mcpGetEntityInput struct {
	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

type mcpUpdateEntityInput struct {
	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
	Enabled  bool        `json:"enabled"   jsonschema:"whether the Entity accepts Commands"`
}

type mcpExecuteEntityCommandInput struct {
	EntityID   mcpEntityID    `json:"entity_id"  jsonschema:"canonical Hearth Entity ID"`
	Operation  string         `json:"operation"  jsonschema:"Entity type operation name, such as set"`
	Parameters map[string]any `json:"parameters" jsonschema:"operation parameters as a JSON object"`
}

type mcpListEntityCommandsInput struct {
	mcpPageInput

	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

type mcpListDevicesInput struct {
	mcpPageInput
}

type mcpGetDeviceInput struct {
	DeviceID     mcpDeviceID  `json:"device_id"               jsonschema:"canonical Hearth Device ID"`
	EntityLimit  mcpPageLimit `json:"entity_limit,omitempty"  jsonschema:"embedded Entity page size from 1 to 200; defaults to 50"`
	EntityCursor string       `json:"entity_cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_entity_cursor"`
}

type mcpGetCommandInput struct {
	CommandID mcpCommandID `json:"command_id" jsonschema:"canonical Hearth Command ID"`
}

type mcpListCommandsInput struct {
	mcpPageInput

	EntityID *mcpEntityID `json:"entity_id,omitempty" jsonschema:"canonical Hearth Entity ID; omit to list every Entity's Commands"`
	Status   string       `json:"status,omitempty"    jsonschema:"Command lifecycle status filter, such as satisfied"`
}

type mcpListAdaptersInput struct {
	mcpPageInput
}

type mcpGetAdapterInput struct {
	AdapterID mcpAdapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}

type mcpListAdapterHealthHistoryInput struct {
	mcpPageInput

	AdapterID mcpAdapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}

type mcpListEntityAvailabilityHistoryInput struct {
	mcpPageInput

	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

type mcpListEntityStateHistoryInput struct {
	mcpPageInput

	EntityID    mcpEntityID `json:"entity_id"             jsonschema:"canonical Hearth Entity ID"`
	Disposition string      `json:"disposition,omitempty" jsonschema:"State history filter: state-updates, all, applied, unchanged, or rejected"`
}

type mcpListEntityEventsInput struct {
	mcpPageInput

	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}
