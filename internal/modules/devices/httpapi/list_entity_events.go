package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntityEvents(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-events", Method: http.MethodGet,
		Path: "/entities/{entity_id}/events", Summary: "List an Entity's Entity Event history",
		Tags:   []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.listEntityEvents)
}

type ListEntityEventsInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

type ListEntityEventsOutput struct {
	Body EntityEventCollectionBody
}

func (handler *handler) listEntityEvents(
	ctx context.Context,
	input *ListEntityEventsInput,
) (*ListEntityEventsOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
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

type EntityEventCollectionBody struct {
	Items      []EntityEventBody `json:"items"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

func entityEventHistoryParams(
	entityID devices.EntityID,
	input *ListEntityEventsInput,
) (devices.ListEntityEventsParams, error) {
	params := devices.ListEntityEventsParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor == "" {
		return params, nil
	}
	before, err := apicursor.DecodeEntityEventCursor(input.Cursor, entityID)
	if err != nil {
		return devices.ListEntityEventsParams{}, huma.NewError(http.StatusBadRequest, "invalid cursor")
	}
	params.BeforeReceiveOrder = before
	return params, nil
}

func entityEventHistoryError(err error) error {
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return huma.NewError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return huma.NewError(http.StatusNotFound, "entity not found")
	default:
		return internalAPIError(err)
	}
}

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
	cursor, err := apicursor.EncodeEntityEventCursor(entityID, page.Items[len(page.Items)-1].ReceiveOrder)
	if err != nil {
		return EntityEventCollectionBody{}, internalAPIError(err)
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
