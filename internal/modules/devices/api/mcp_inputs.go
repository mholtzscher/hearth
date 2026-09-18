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
// them. Each tool then translates its arguments into the shared Huma request.

const (
	// mcpDefaultPageLimit is the page size an omitted limit argument means,
	// matching the Huma query parameter default.
	mcpDefaultPageLimit = 50
	// mcpMinPageLimit and mcpMaxPageLimit bound every page size argument,
	// matching the Huma query parameter range.
	mcpMinPageLimit = 1
	mcpMaxPageLimit = 200
)

// mcpPageLimit is a page size argument bounded to the range every Huma list
// query accepts. Decoding rejects an out-of-range value; the zero value means
// the argument was omitted.
type mcpPageLimit int

// UnmarshalJSON decodes and range-checks one page size argument.
func (limit *mcpPageLimit) UnmarshalJSON(data []byte) error {
	var value int
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	checked, err := mcpPageLimitValue(value)
	if err != nil {
		return err
	}
	*limit = checked
	return nil
}

// mcpPageLimitValue validates one page size against the shared range.
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
// tool accepts. Embedding it gives each tool the same two arguments.
//
// MCP defines no cursor format: the cursor is forwarded to services
// uninterpreted, exactly as a Huma handler forwards it.
type mcpPageInput struct {
	Limit  mcpPageLimit `json:"limit,omitempty"  jsonschema:"page size from 1 to 200; defaults to 50"`
	Cursor string       `json:"cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_cursor"`
}

// mcpIDValue decodes one JSON string argument for an ID-typed field.
func mcpIDValue(data []byte) (string, error) {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", err
	}
	return value, nil
}

// mcpEntityID is a canonical Hearth Entity ID argument. Decoding rejects a
// malformed ID before the handler runs, so no handler sees one.
type mcpEntityID string

// UnmarshalJSON decodes and validates one Entity ID argument.
func (id *mcpEntityID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return err
	}
	parsed, err := devices.ParseEntityID(value)
	if err != nil {
		return err
	}
	*id = mcpEntityID(parsed)
	return nil
}

// mcpDeviceID is a canonical Hearth Device ID argument. Decoding rejects a
// malformed ID before the handler runs, so no handler sees one.
type mcpDeviceID string

// UnmarshalJSON decodes and validates one Device ID argument.
func (id *mcpDeviceID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return err
	}
	parsed, err := devices.ParseDeviceID(value)
	if err != nil {
		return err
	}
	*id = mcpDeviceID(parsed)
	return nil
}

// mcpCommandID is a canonical Hearth Command ID argument. Decoding rejects a
// malformed ID before the handler runs, so no handler sees one.
type mcpCommandID string

// UnmarshalJSON decodes and validates one Command ID argument.
func (id *mcpCommandID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return err
	}
	parsed, err := devices.ParseCommandID(value)
	if err != nil {
		return err
	}
	*id = mcpCommandID(parsed)
	return nil
}

// mcpAdapterID is a subject-safe Adapter ID argument. Decoding rejects anything
// that is not a subject-safe slug before the handler runs.
type mcpAdapterID string

// UnmarshalJSON decodes and validates one Adapter ID argument.
func (id *mcpAdapterID) UnmarshalJSON(data []byte) error {
	value, err := mcpIDValue(data)
	if err != nil {
		return err
	}
	if !validAdapterID(value) {
		return errors.New("adapter_id must be a subject-safe slug")
	}
	*id = mcpAdapterID(value)
	return nil
}

// mcpOptionalDeviceID returns the string form of an optional Device ID filter,
// or the empty string when the argument was omitted. An explicit empty string
// is rejected while decoding as a malformed ID rather than treated as absent.
func mcpOptionalDeviceID(id *mcpDeviceID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

// mcpOptionalEntityID returns the string form of an optional Entity ID filter,
// or the empty string when the argument was omitted. An explicit empty string
// is rejected while decoding as a malformed ID rather than treated as absent.
func mcpOptionalEntityID(id *mcpEntityID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

// The tool argument structs below are MCP-only: they describe the JSON Schema
// clients see and translate mechanically into the shared Huma request type.

// mcpListEntitiesInput lists one page of Entities, optionally scoped to a Device.
type mcpListEntitiesInput struct {
	mcpPageInput

	DeviceID *mcpDeviceID `json:"device_id,omitempty" jsonschema:"canonical Hearth Device ID; omit to list every Device"`
}

// mcpGetEntityInput names one Entity.
type mcpGetEntityInput struct {
	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

// mcpUpdateEntityInput names one Entity and the enabled flag to set.
type mcpUpdateEntityInput struct {
	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
	Enabled  bool        `json:"enabled"   jsonschema:"whether the Entity accepts Commands"`
}

// mcpExecuteEntityCommandInput names one Entity, one of its type's operations,
// and that operation's parameters.
type mcpExecuteEntityCommandInput struct {
	EntityID   mcpEntityID    `json:"entity_id"  jsonschema:"canonical Hearth Entity ID"`
	Operation  string         `json:"operation"  jsonschema:"Entity type operation name, such as set"`
	Parameters map[string]any `json:"parameters" jsonschema:"operation parameters as a JSON object"`
}

// mcpListEntityCommandsInput names one Entity and the Command history page.
type mcpListEntityCommandsInput struct {
	mcpPageInput

	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

// mcpListDevicesInput selects one page of Devices.
type mcpListDevicesInput struct {
	mcpPageInput
}

// mcpGetDeviceInput names one Device and the embedded Entity page.
type mcpGetDeviceInput struct {
	DeviceID     mcpDeviceID  `json:"device_id"               jsonschema:"canonical Hearth Device ID"`
	EntityLimit  mcpPageLimit `json:"entity_limit,omitempty"  jsonschema:"embedded Entity page size from 1 to 200; defaults to 50"`
	EntityCursor string       `json:"entity_cursor,omitempty" jsonschema:"opaque cursor from a previous page's next_entity_cursor"`
}

// mcpGetCommandInput names one Command record.
type mcpGetCommandInput struct {
	CommandID mcpCommandID `json:"command_id" jsonschema:"canonical Hearth Command ID"`
}

// mcpListCommandsInput selects one page of household Command history.
type mcpListCommandsInput struct {
	mcpPageInput

	EntityID *mcpEntityID `json:"entity_id,omitempty" jsonschema:"canonical Hearth Entity ID; omit to list every Entity's Commands"`
	Status   string       `json:"status,omitempty"    jsonschema:"Command lifecycle status filter, such as satisfied"`
}

// mcpListAdaptersInput selects one page of Adapters.
type mcpListAdaptersInput struct {
	mcpPageInput
}

// mcpGetAdapterInput names one Adapter.
type mcpGetAdapterInput struct {
	AdapterID mcpAdapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}

// mcpListAdapterHealthHistoryInput names one Adapter and its health history page.
type mcpListAdapterHealthHistoryInput struct {
	mcpPageInput

	AdapterID mcpAdapterID `json:"adapter_id" jsonschema:"subject-safe Adapter ID"`
}

// mcpListEntityAvailabilityHistoryInput names one Entity and its availability
// history page.
type mcpListEntityAvailabilityHistoryInput struct {
	mcpPageInput

	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

// mcpListEntityStateHistoryInput names one Entity and selects one State history
// page and disposition filter.
type mcpListEntityStateHistoryInput struct {
	mcpPageInput

	EntityID    mcpEntityID `json:"entity_id"             jsonschema:"canonical Hearth Entity ID"`
	Disposition string      `json:"disposition,omitempty" jsonschema:"State history filter: state-updates, all, applied, unchanged, or rejected"`
}

// mcpListEntityEventsInput names one Entity and its Entity Event history page.
type mcpListEntityEventsInput struct {
	mcpPageInput

	EntityID mcpEntityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}
