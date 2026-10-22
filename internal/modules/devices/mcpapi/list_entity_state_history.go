//nolint:goconst // Literal wire keys keep the fixed success contracts readable.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListEntityStateHistory(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntityStateHistoryInput, Page[StateHistoryEntry]]{
			Name:          "list_entity_state_history",
			Description:   "Read retained accepted State values and rejected Observation diagnostics.",
			SuccessSchema: stateHistoryPageSchema(),
			Handler:       h.listEntityStateHistory,
		},
	)
}

type ListEntityStateHistoryInput struct {
	PageInput

	EntityID    entityID `json:"entity_id"             doc:"canonical Hearth Entity ID"`
	Disposition string   `json:"disposition,omitempty" doc:"State history filter: state-updates, all, applied, unchanged, or rejected"`
}

func (handler *handler) listEntityStateHistory(
	ctx context.Context,
	input ListEntityStateHistoryInput,
) (Page[StateHistoryEntry], error) {
	entityID := devices.EntityID(input.EntityID)
	filter := devices.EntityStateHistoryFilter(input.Disposition)
	if filter == "" {
		filter = devices.EntityStateHistoryFilterUpdates
	}
	params := devices.ListEntityStateHistoryParams{EntityID: entityID, Filter: filter, Limit: pageSize(input.Limit)}
	if input.Cursor != "" {
		before, cursorErr := apicursor.DecodeEntityStateHistoryCursor(input.Cursor, entityID, filter)
		if cursorErr != nil {
			return Page[StateHistoryEntry]{}, invalidRequestFailure("invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListEntityStateHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return Page[StateHistoryEntry]{}, invalidRequestFailure("invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return Page[StateHistoryEntry]{}, toolError(failureEntityNotFound, "entity not found")
	case err != nil:
		return Page[StateHistoryEntry]{}, mcptools.InternalToolError(err)
	}
	body := Page[StateHistoryEntry]{Items: make([]StateHistoryEntry, len(page.Items))}
	for index, entry := range page.Items {
		mapped, mappingErr := stateHistoryEntry(entry)
		if mappingErr != nil {
			return Page[StateHistoryEntry]{}, mcptools.InternalToolError(mappingErr)
		}
		body.Items[index] = mapped
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntityStateHistoryCursor(
			entityID,
			filter,
			page.Items[len(page.Items)-1].ReceiveOrder,
		)
		if cursorErr != nil {
			return Page[StateHistoryEntry]{}, mcptools.InternalToolError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return body, nil
}

type StateHistoryEntry struct {
	ObservationID     string          `json:"observation_id"`
	Value             json.RawMessage `json:"value,omitempty"`
	Disposition       string          `json:"disposition"`
	RejectionCode     *string         `json:"rejection_code,omitempty"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	ObservedAt        string          `json:"observed_at"`
}

func stateHistoryEntry(entry devices.EntityStateHistoryEntry) (StateHistoryEntry, error) {
	body := StateHistoryEntry{
		ObservationID:     string(entry.ObservationID),
		Disposition:       string(entry.Disposition),
		AdapterReceivedAt: formatTime(entry.AdapterReceivedAt),
		ObservedAt:        formatTime(entry.ObservedAt),
	}
	if entry.Disposition != devices.DispositionRejected {
		var value json.RawMessage
		if err := decodeJSON(entry.Value, &value); err != nil {
			return StateHistoryEntry{}, err
		}
		body.Value = value
	}
	if entry.Rejection != nil {
		code := string(*entry.Rejection)
		body.RejectionCode = &code
	}
	if entry.SourceUpdatedAt != nil {
		sourceUpdatedAt := formatTime(*entry.SourceUpdatedAt)
		body.SourceUpdatedAt = &sourceUpdatedAt
	}
	return body, nil
}

func stateHistoryPageSchema() *huma.Schema {
	accepted := recordSchema(map[string]*huma.Schema{
		"observation_id":      stringSchema(),
		"disposition":         {Enum: []any{"applied", "unchanged"}},
		"observed_at":         stringSchema(),
		"adapter_received_at": stringSchema(),
		"source_updated_at":   stringSchema(),
		"value":               {},
	}, "observation_id", "disposition", "observed_at", "adapter_received_at", "value")
	rejected := recordSchema(map[string]*huma.Schema{
		"observation_id":      stringSchema(),
		"disposition":         {Const: "rejected"},
		"observed_at":         stringSchema(),
		"adapter_received_at": stringSchema(),
		"source_updated_at":   stringSchema(),
		"rejection_code":      stringSchema(),
	}, "observation_id", "disposition", "observed_at", "adapter_received_at", "rejection_code")
	return pageSchema(&huma.Schema{OneOf: []*huma.Schema{accepted, rejected}})
}
