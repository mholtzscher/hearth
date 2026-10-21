package mcpapi

import (
	"context"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntityEvents(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntityEventsInput, Page[EntityEvent]]{
			Name:        "list_entity_events",
			Description: "Read retained Entity Event occurrences and rejection diagnostics.",
			ExactOutput: true,
			Handler:     h.listEntityEvents,
		},
	)
}

type ListEntityEventsInput struct {
	pageInput

	EntityID entityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

type EntityEvent struct {
	EventID       string  `json:"event_id"`
	EntityID      string  `json:"entity_id"`
	Name          string  `json:"name"`
	Disposition   string  `json:"disposition"`
	EmittedAt     string  `json:"emitted_at"`
	ReceivedAt    string  `json:"received_at"`
	RecordedAt    string  `json:"recorded_at"`
	RejectionCode *string `json:"rejection_code,omitempty"`
}

func (h *handler) listEntityEvents(ctx context.Context, input ListEntityEventsInput) (Page[EntityEvent], error) {
	id := devices.EntityID(input.EntityID)
	params := devices.ListEntityEventsParams{EntityID: id, Limit: pageSize(input.Limit)}
	if input.Cursor != "" {
		before, err := apicursor.DecodeEntityEventCursor(input.Cursor, id)
		if err != nil {
			return Page[EntityEvent]{}, invalidRequestFailure("invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := h.devices.ListEntityEvents(ctx, params)
	if err != nil {
		return Page[EntityEvent]{}, readFailure(err)
	}
	result := Page[EntityEvent]{Items: make([]EntityEvent, len(page.Items))}
	for i, entry := range page.Items {
		item := EntityEvent{
			EventID:     string(entry.EventID),
			EntityID:    string(entry.EntityID),
			Name:        string(entry.Name),
			Disposition: string(entry.Disposition),
			EmittedAt:   formatTime(entry.EmittedAt),
			ReceivedAt:  formatTime(entry.ReceivedAt),
			RecordedAt:  formatTime(entry.RecordedAt),
		}
		if entry.Rejection != nil {
			value := string(*entry.Rejection)
			item.RejectionCode = &value
		}
		result.Items[i] = item
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntityEventCursor(id, page.Items[len(page.Items)-1].ReceiveOrder)
		if cursorErr != nil {
			return Page[EntityEvent]{}, mcptools.InternalToolError(cursorErr)
		}
		result.NextCursor = &cursor
	}
	return result, nil
}

func (input *ListEntityEventsInput) UnmarshalJSON(data []byte) error {
	type arguments ListEntityEventsInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = ListEntityEventsInput(decoded)
	return nil
}
