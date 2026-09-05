package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// EntityStateHistoryBody is one retained first-seen Observation for an Entity,
// newest-first by receive order. Value is omitted for rejected rows.
type EntityStateHistoryBody struct {
	ObservationID     string  `json:"observation_id"`
	Value             any     `json:"value,omitempty"`
	Disposition       string  `json:"disposition"`
	RejectionCode     *string `json:"rejection_code,omitempty"`
	AdapterReceivedAt string  `json:"adapter_received_at"`
	SourceUpdatedAt   *string `json:"source_updated_at,omitempty"`
	ObservedAt        string  `json:"observed_at"`
}

// EntityStateHistoryCollectionBody is one keyset page of an Entity's retained
// State history. NextCursor is omitted on the final page.
type EntityStateHistoryCollectionBody struct {
	Items      []EntityStateHistoryBody `json:"items"`
	NextCursor *string                  `json:"next_cursor,omitempty"`
}

// ListEntityStateHistoryInput selects one keyset page of an Entity's retained
// State history. Disposition has no Huma enum tag so unsupported semantic
// filter values return HTTP 400 rather than structural-validation HTTP 422.
type ListEntityStateHistoryInput struct {
	EntityID    string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit       int    `                                                  query:"limit"       default:"50"            minimum:"1" maximum:"200"`
	Cursor      string `                                                  query:"cursor"`
	Disposition string `                                                  query:"disposition" default:"state-updates"`
}

// ListEntityStateHistoryOutput carries one page of State history entries.
type ListEntityStateHistoryOutput struct {
	Body EntityStateHistoryCollectionBody
}

// ListEntityStateHistory returns one keyset page of an Entity's retained
// State history, newest-first by receive order.
func (handler *Handler) ListEntityStateHistory(
	ctx context.Context,
	input *ListEntityStateHistoryInput,
) (*ListEntityStateHistoryOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	filter := devices.EntityStateHistoryFilter(input.Disposition)
	if filter == "" {
		filter = devices.EntityStateHistoryFilterUpdates
	}
	params := devices.ListEntityStateHistoryParams{EntityID: entityID, Filter: filter, Limit: input.Limit}
	if input.Cursor != "" {
		before, cursorErr := decodeEntityStateHistoryCursor(input.Cursor, entityID, filter)
		if cursorErr != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListEntityStateHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, apiError(http.StatusNotFound, "entity not found")
	case err != nil:
		return nil, apiError(http.StatusInternalServerError, "internal error")
	}
	body := EntityStateHistoryCollectionBody{Items: make([]EntityStateHistoryBody, len(page.Items))}
	for index, entry := range page.Items {
		mapped, mappingErr := entityStateHistoryBody(entry)
		if mappingErr != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.Items[index] = mapped
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeEntityStateHistoryCursor(
			entityID,
			filter,
			page.Items[len(page.Items)-1].ReceiveOrder,
		)
		if cursorErr != nil {
			return nil, apiError(http.StatusInternalServerError, "internal error")
		}
		body.NextCursor = &cursor
	}
	return &ListEntityStateHistoryOutput{Body: body}, nil
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
