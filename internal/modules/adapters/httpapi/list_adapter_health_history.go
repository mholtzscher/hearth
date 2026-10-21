package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/internal/apicursor"
)

func registerListAdapterHealthHistory(api huma.API, handler *handler) {
	const adaptersTag = "Adapters"
	huma.Register(api, huma.Operation{
		OperationID: "list-adapter-health-history", Method: http.MethodGet,
		Path: "/adapters/{adapter_id}/health/history", Summary: "List an Adapter's health history",
		Tags:   []string{adaptersTag},
		Errors: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.listAdapterHealthHistory)
}

type ListAdapterHealthHistoryInput struct {
	AdapterID string `path:"adapter_id" doc:"Subject-safe Adapter ID"`
	Limit     int    `                                                query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor    string `                                                query:"cursor"`
}

type ListAdapterHealthHistoryOutput struct {
	Body AdapterHealthTransitionCollectionBody
}

func (handler *handler) listAdapterHealthHistory(
	ctx context.Context,
	input *ListAdapterHealthHistoryInput,
) (*ListAdapterHealthHistoryOutput, error) {
	if !validAdapterID(input.AdapterID) {
		return nil, apiError(http.StatusBadRequest, "adapter_id must be a subject-safe slug")
	}
	params := adapters.ListAdapterHealthParams{AdapterID: input.AdapterID, Limit: input.Limit}
	if input.Cursor != "" {
		before, err := apicursor.DecodeHealth(input.Cursor, input.AdapterID)
		if err != nil {
			return nil, apiError(http.StatusBadRequest, "invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.adapters.ListAdapterHealthHistory(ctx, params)
	switch {
	case errors.Is(err, adapters.ErrInvalidPage):
		return nil, apiError(http.StatusBadRequest, "invalid page")
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return nil, apiError(http.StatusNotFound, "adapter not found")
	case err != nil:
		return nil, internalAPIError(err)
	}
	body := AdapterHealthTransitionCollectionBody{Items: make([]AdapterHealthTransitionBody, len(page.Items))}
	for index, transition := range page.Items {
		body.Items[index] = healthTransitionBody(transition)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeHealth(input.AdapterID, page.Items[len(page.Items)-1].ReceiveOrder)
		if cursorErr != nil {
			return nil, internalAPIError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListAdapterHealthHistoryOutput{Body: body}, nil
}

type AdapterHealthTransitionBody struct {
	Status           string                   `json:"status"`
	Source           string                   `json:"source"`
	Reason           *AdapterHealthReasonBody `json:"reason,omitempty"`
	SourceObservedAt *string                  `json:"source_observed_at,omitempty"`
	ObservedAt       string                   `json:"observed_at"`
}

type AdapterHealthTransitionCollectionBody struct {
	Items      []AdapterHealthTransitionBody `json:"items"`
	NextCursor *string                       `json:"next_cursor,omitempty"`
}

func healthTransitionBody(transition adapters.HealthTransition) AdapterHealthTransitionBody {
	body := AdapterHealthTransitionBody{
		Status: transition.Status, Source: transition.Source,
		Reason: healthReasonBody(transition.Reason), ObservedAt: formatTime(transition.ObservedAt),
	}
	if transition.SourceObservedAt != nil {
		value := formatTime(*transition.SourceObservedAt)
		body.SourceObservedAt = &value
	}
	return body
}
