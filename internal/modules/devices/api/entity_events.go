package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// EntityEventBody is one retained first-seen Entity Event for an Entity,
// newest-first by receive order. Adapter, runtime, and correlation IDs, the
// fingerprint, the raw envelope, and receive order stay private: the body only
// shows whether Core recorded a report and why Core rejected it.
type EntityEventBody struct {
	EventID       string  `json:"event_id"`
	EntityID      string  `json:"entity_id"`
	Name          string  `json:"name"`
	Disposition   string  `json:"disposition"`
	RejectionCode *string `json:"rejection_code,omitempty"`
	EmittedAt     string  `json:"emitted_at"`
	ReceivedAt    string  `json:"received_at"`
	RecordedAt    string  `json:"recorded_at"`
}

// EntityEventCollectionBody is one keyset page of an Entity's retained
// Entity Event history. NextCursor is omitted on the final page.
type EntityEventCollectionBody struct {
	Items      []EntityEventBody `json:"items"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

// ListEntityEventsInput selects one keyset page of an Entity's retained
// Entity Event history. Event history has no disposition filter: accepted and
// rejected records always appear together.
type ListEntityEventsInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

// ListEntityEventsOutput carries one page of Entity Event history entries.
type ListEntityEventsOutput struct {
	Body EntityEventCollectionBody
}

// ListEntityEvents returns one keyset page of an Entity's retained Entity
// Event history, newest-first by receive order. An existing Entity with no
// events returns an empty page, while an unknown parent returns 404.
func (handler *Handler) ListEntityEvents(
	ctx context.Context,
	input *ListEntityEventsInput,
) (*ListEntityEventsOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params, err := entityEventHistoryParams(entityID, input)
	if err != nil {
		return nil, err
	}
	page, err := handler.devices.ListEntityEvents(ctx, params)
	if err != nil {
		return nil, entityEventHistoryError(err)
	}
	body, err := entityEventHistoryPageBody(entityID, page)
	if err != nil {
		return nil, err
	}
	return &ListEntityEventsOutput{Body: body}, nil
}

// entityEventHistoryParams binds the request to the service page parameters,
// rejecting a cursor from another resource or Entity before any read.
func entityEventHistoryParams(
	entityID devices.EntityID,
	input *ListEntityEventsInput,
) (devices.ListEntityEventsParams, error) {
	params := devices.ListEntityEventsParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor == "" {
		return params, nil
	}
	before, err := decodeEntityEventCursor(input.Cursor, entityID)
	if err != nil {
		return devices.ListEntityEventsParams{}, apiError(http.StatusBadRequest, "invalid cursor")
	}
	params.BeforeReceiveOrder = before
	return params, nil
}

// entityEventHistoryError maps a validated history read failure to its HTTP
// problem. The caller only reaches it with a non-nil error.
func entityEventHistoryError(err error) error {
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return apiError(http.StatusNotFound, "entity not found")
	default:
		return apiError(http.StatusInternalServerError, "internal error")
	}
}

// entityEventHistoryPageBody maps one page and continues it only when Core
// reported more rows than the page, so the final page carries no cursor.
func entityEventHistoryPageBody(
	entityID devices.EntityID,
	page devices.Page[devices.EntityEventHistoryEntry],
) (EntityEventCollectionBody, error) {
	body := EntityEventCollectionBody{Items: make([]EntityEventBody, len(page.Items))}
	for index, entry := range page.Items {
		body.Items[index] = entityEventBody(entry)
	}
	if !page.HasMore || len(page.Items) == 0 {
		return body, nil
	}
	cursor, err := encodeEntityEventCursor(entityID, page.Items[len(page.Items)-1].ReceiveOrder)
	if err != nil {
		return EntityEventCollectionBody{}, apiError(http.StatusInternalServerError, "internal error")
	}
	body.NextCursor = &cursor
	return body, nil
}

func entityEventBody(entry devices.EntityEventHistoryEntry) EntityEventBody {
	body := EntityEventBody{
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
