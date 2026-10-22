package mcpapi

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListEntityAvailabilityHistory(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntityAvailabilityHistoryInput, Page[HealthTransition]]{
			Name:        "list_entity_availability_history",
			Description: "Read retained Entity availability transitions.",
			Handler:     h.listEntityAvailabilityHistory,
		},
	)
}

type ListEntityAvailabilityHistoryInput struct {
	PageInput

	EntityID entityID `json:"entity_id" doc:"canonical Hearth Entity ID"`
}

func (handler *handler) listEntityAvailabilityHistory(
	ctx context.Context,
	input ListEntityAvailabilityHistoryInput,
) (Page[HealthTransition], error) {
	entityID := devices.EntityID(input.EntityID)
	params := devices.ListEntityAvailabilityParams{EntityID: entityID, Limit: pageSize(input.Limit)}
	if input.Cursor != "" {
		before, cursorErr := apicursor.DecodeEntityAvailabilityCursor(input.Cursor, entityID)
		if cursorErr != nil {
			return Page[HealthTransition]{}, invalidRequestFailure("invalid cursor")
		}
		params.BeforeReceiveOrder = before
	}
	page, err := handler.devices.ListEntityAvailabilityHistory(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return Page[HealthTransition]{}, invalidRequestFailure("invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return Page[HealthTransition]{}, toolError(failureEntityNotFound, "entity not found")
	case err != nil:
		return Page[HealthTransition]{}, mcptools.InternalToolError(err)
	}
	body := Page[HealthTransition]{Items: make([]HealthTransition, len(page.Items))}
	for index, transition := range page.Items {
		body.Items[index] = healthTransitionBody(transition)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntityAvailabilityCursor(
			entityID,
			page.Items[len(page.Items)-1].ReceiveOrder,
		)
		if cursorErr != nil {
			return Page[HealthTransition]{}, mcptools.InternalToolError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return body, nil
}

type HealthTransition struct {
	Status           string  `json:"status"`
	Source           string  `json:"source"`
	ReasonCode       string  `json:"reason_code,omitempty"`
	SourceObservedAt *string `json:"source_observed_at,omitempty" nullable:"true"`
	ObservedAt       string  `json:"observed_at"`
}

func healthTransitionBody(transition devices.HealthTransition) HealthTransition {
	body := HealthTransition{
		Status: transition.Status, Source: transition.Source,
		ReasonCode: reasonCode(transition.Reason), ObservedAt: formatTime(transition.ObservedAt),
	}
	if transition.SourceObservedAt != nil {
		value := formatTime(*transition.SourceObservedAt)
		body.SourceObservedAt = &value
	}
	return body
}

func reasonCode(reason *devices.HealthReason) string {
	if reason == nil {
		return ""
	}
	return reason.Code
}
