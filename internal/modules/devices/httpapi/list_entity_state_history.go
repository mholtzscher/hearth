package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntityStateHistory(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-state-history", Method: http.MethodGet,
		Path: "/entities/{entity_id}/state/history", Summary: "List an Entity's State history",
		Tags:   []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.listEntityStateHistory)
}

type ListEntityStateHistoryInput struct {
	EntityID    string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit       int    `                                                  query:"limit"       default:"50"            minimum:"1" maximum:"200"`
	Cursor      string `                                                  query:"cursor"`
	Disposition string `                                                  query:"disposition" default:"state-updates"`
}

type ListEntityStateHistoryOutput struct {
	Body EntityStateHistoryCollectionBody
}

func (handler *handler) listEntityStateHistory(
	ctx context.Context,
	input *ListEntityStateHistoryInput,
) (*ListEntityStateHistoryOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	filter := devices.EntityStateHistoryFilter(input.Disposition)
	if filter == "" {
		filter = devices.EntityStateHistoryFilterUpdates
	}
	params := devices.ListEntityStateHistoryParams{EntityID: entityID, Filter: filter, Limit: input.Limit}
	if input.Cursor != "" {
		before, cursorErr := apicursor.DecodeEntityStateHistoryCursor(input.Cursor, entityID, filter)
		if cursorErr != nil {
			return nil, huma.NewError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListEntityStateHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, huma.NewError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, huma.NewError(http.StatusNotFound, "entity not found")
	case err != nil:
		return nil, handler.internalAPIError(ctx, "list-entity-state-history", "service", err)
	}
	body := EntityStateHistoryCollectionBody{Items: make([]EntityStateHistoryBody, len(page.Items))}
	for index, entry := range page.Items {
		mapped, mappingErr := entityStateHistoryBody(entry)
		if mappingErr != nil {
			return nil, handler.internalAPIError(ctx, "list-entity-state-history", "mapping", mappingErr)
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
			return nil, handler.internalAPIError(ctx, "list-entity-state-history", "mapping", cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListEntityStateHistoryOutput{Body: body}, nil
}

type EntityStateHistoryBody struct {
	ObservationID     string  `json:"observation_id"`
	Value             any     `json:"value,omitempty"`
	Disposition       string  `json:"disposition"`
	RejectionCode     *string `json:"rejection_code,omitempty"`
	AdapterReceivedAt string  `json:"adapter_received_at"`
	SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
	ObservedAt        string  `json:"observed_at"`
}

type EntityStateHistoryCollectionBody struct {
	Items      []EntityStateHistoryBody `json:"items"`
	NextCursor *string                  `json:"next_cursor,omitempty"`
}

func entityStateHistoryBody(entry devices.EntityStateHistoryEntry) (EntityStateHistoryBody, error) {
	body := EntityStateHistoryBody{
		ObservationID:     string(entry.ObservationID),
		Disposition:       string(entry.Disposition),
		AdapterReceivedAt: formatTime(entry.AdapterReceivedAt),
		ObservedAt:        formatTime(entry.ObservedAt),
	}
	if entry.Disposition != devices.DispositionRejected {
		var value any
		if err := decodeJSON(entry.Value, &value); err != nil {
			return EntityStateHistoryBody{}, err
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
