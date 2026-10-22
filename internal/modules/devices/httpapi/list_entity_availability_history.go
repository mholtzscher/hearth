package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntityAvailabilityHistory(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entity-availability-history", Method: http.MethodGet,
		Path: "/entities/{entity_id}/availability/history", Summary: "List an Entity's availability history",
		Tags:   []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.listEntityAvailabilityHistory)
}

type ListEntityAvailabilityHistoryInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
	Limit    int    `                                                  query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor   string `                                                  query:"cursor"`
}

type ListEntityAvailabilityHistoryOutput struct {
	Body HealthTransitionCollectionBody
}

func (handler *handler) listEntityAvailabilityHistory(
	ctx context.Context,
	input *ListEntityAvailabilityHistoryInput,
) (*ListEntityAvailabilityHistoryOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "entity_id must be a canonical Hearth Entity ID")
	}
	params := devices.ListEntityAvailabilityParams{EntityID: entityID, Limit: input.Limit}
	if input.Cursor != "" {
		before, cursorErr := apicursor.DecodeEntityAvailabilityCursor(input.Cursor, entityID)
		if cursorErr != nil {
			return nil, huma.NewError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListEntityAvailabilityHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return nil, huma.NewError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return nil, huma.NewError(http.StatusNotFound, "entity not found")
	case err != nil:
		return nil, internalAPIError(err)
	}
	body := HealthTransitionCollectionBody{Items: make([]HealthTransitionBody, len(page.Items))}
	for index, transition := range page.Items {
		body.Items[index] = healthTransitionBody(transition)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntityAvailabilityCursor(
			entityID,
			page.Items[len(page.Items)-1].ReceiveOrder,
		)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListEntityAvailabilityHistoryOutput{Body: body}, nil
}

type HealthTransitionBody struct {
	Status           string            `json:"status"`
	Source           string            `json:"source"`
	Reason           *HealthReasonBody `json:"reason,omitempty"`
	SourceObservedAt *string           `json:"source_observed_at,omitempty"`
	ObservedAt       string            `json:"observed_at"`
}

type HealthTransitionCollectionBody struct {
	Items      []HealthTransitionBody `json:"items"`
	NextCursor *string                `json:"next_cursor,omitempty"`
}

func healthTransitionBody(transition devices.HealthTransition) HealthTransitionBody {
	body := HealthTransitionBody{
		Status: transition.Status, Source: transition.Source,
		Reason: healthReasonBody(transition.Reason), ObservedAt: formatTime(transition.ObservedAt),
	}
	if transition.SourceObservedAt != nil {
		value := formatTime(*transition.SourceObservedAt)
		body.SourceObservedAt = &value
	}
	return body
}
