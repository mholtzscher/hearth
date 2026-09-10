package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// EntityDeviceEventBody is one retained first-seen Device Event for an Entity,
// newest-first by receive order. Adapter, runtime, and correlation IDs, the
// fingerprint, the raw envelope, and receive order stay private: the body only
// shows whether Core recorded a report and why Core rejected it.
type EntityDeviceEventBody struct {
	EventID       string  `json:"event_id"`
	EntityID      string  `json:"entity_id"`
	Name          string  `json:"name"`
	Disposition   string  `json:"disposition"`
	RejectionCode *string `json:"rejection_code,omitempty"`
	EmittedAt     string  `json:"emitted_at"`
	ReceivedAt    string  `json:"received_at"`
	RecordedAt    string  `json:"recorded_at"`
}

// EntityDeviceEventCollectionBody is one keyset page of an Entity's retained
// Device Event history. NextCursor is omitted on the final page.
type EntityDeviceEventCollectionBody struct {
	Items      []EntityDeviceEventBody `json:"items"`
	NextCursor *string                 `json:"next_cursor,omitempty"`
}

// ListEntityDeviceEventsInput selects one keyset page of an Entity's retained
// Device Event history. Event history has no disposition filter: accepted and
// rejected records always appear together.
type ListEntityDeviceEventsInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

// ListEntityDeviceEventsOutput carries one page of Device Event history entries.
type ListEntityDeviceEventsOutput struct {
	Body EntityDeviceEventCollectionBody
}

// ListEntityDeviceEvents returns one keyset page of an Entity's retained Device
// Event history, newest-first by receive order. An existing Entity with no
// events returns an empty page, while an unknown parent returns 404.
func (handler *Handler) ListEntityDeviceEvents(
	ctx context.Context,
	input *ListEntityDeviceEventsInput,
) (*ListEntityDeviceEventsOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params, err := deviceEventHistoryParams(entityID, input)
	if err != nil {
		return nil, err
	}
	page, err := handler.devices.ListEntityDeviceEvents(ctx, params)
	if err != nil {
		return nil, deviceEventHistoryError(err)
	}
	body, err := deviceEventHistoryPageBody(entityID, page)
	if err != nil {
		return nil, err
	}
	return &ListEntityDeviceEventsOutput{Body: body}, nil
}

// deviceEventHistoryParams binds the request to the service page parameters,
// rejecting a cursor from another resource or Entity before any read.
func deviceEventHistoryParams(
	entityID devices.EntityID,
	input *ListEntityDeviceEventsInput,
) (devices.ListEntityDeviceEventsParams, error) {
	params := devices.ListEntityDeviceEventsParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor == "" {
		return params, nil
	}
	before, err := decodeEntityDeviceEventCursor(input.Cursor, entityID)
	if err != nil {
		return devices.ListEntityDeviceEventsParams{}, apiError(http.StatusBadRequest, "invalid cursor")
	}
	params.BeforeReceiveOrder = before
	return params, nil
}

// deviceEventHistoryError maps a validated history read failure to its HTTP
// problem. The caller only reaches it with a non-nil error.
func deviceEventHistoryError(err error) error {
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return apiError(http.StatusNotFound, "entity not found")
	default:
		return apiError(http.StatusInternalServerError, "internal error")
	}
}

// deviceEventHistoryPageBody maps one page and continues it only when Core
// reported more rows than the page, so the final page carries no cursor.
func deviceEventHistoryPageBody(
	entityID devices.EntityID,
	page devices.Page[devices.DeviceEventHistoryEntry],
) (EntityDeviceEventCollectionBody, error) {
	body := EntityDeviceEventCollectionBody{Items: make([]EntityDeviceEventBody, len(page.Items))}
	for index, entry := range page.Items {
		body.Items[index] = entityDeviceEventBody(entry)
	}
	if !page.HasMore || len(page.Items) == 0 {
		return body, nil
	}
	cursor, err := encodeEntityDeviceEventCursor(entityID, page.Items[len(page.Items)-1].ReceiveOrder)
	if err != nil {
		return EntityDeviceEventCollectionBody{}, apiError(http.StatusInternalServerError, "internal error")
	}
	body.NextCursor = &cursor
	return body, nil
}

func entityDeviceEventBody(entry devices.DeviceEventHistoryEntry) EntityDeviceEventBody {
	body := EntityDeviceEventBody{
		EventID:     string(entry.EventID),
		EntityID:    string(entry.EntityID),
		Name:        string(entry.Name),
		Disposition: string(entry.Disposition),
		EmittedAt:   formatTime(entry.EmittedAt),
		ReceivedAt:  formatTime(entry.ReceivedAt),
		RecordedAt:  formatTime(entry.RecordedAt),
	}
	if entry.Rejection != nil {
		code := string(*entry.Rejection)
		body.RejectionCode = &code
	}
	return body
}
