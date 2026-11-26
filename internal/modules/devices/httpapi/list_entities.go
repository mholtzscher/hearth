package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
)

func registerListEntities(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-entities", Method: http.MethodGet, Path: "/entities",
		Summary: "List Entities and their current State", Tags: []string{entitiesTag},
		Errors: []int{http.StatusBadRequest, http.StatusInternalServerError},
	}, handler.listEntities)
}

type ListEntitiesInput struct {
	Limit    int      `query:"limit"     default:"50" minimum:"1" maximum:"200"`
	Cursor   string   `query:"cursor"`
	DeviceID string   `query:"device_id"                                        doc:"Canonical Hearth Device ID"`
	Units    string   `query:"units"                                            doc:"Omit for household units; canonical returns canonical values"`
	LabelIDs []string `query:"label_id"                                         doc:"Comma-separated Label IDs. Entities must have every listed Label; an Entity inherits its Device's Area and tags. At most 8" explode:"false" nullable:"false"`
	Types    []string `query:"type"                                             doc:"Comma-separated Entity types. Entities must have one of the listed types. At most 16"                                       explode:"false" nullable:"false"`
}

type ListEntitiesOutput struct {
	Body EntityCollectionBody
}

type EntityCollectionBody struct {
	Items      []EntityBody `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

func (handler *handler) listEntities(ctx context.Context, input *ListEntitiesInput) (*ListEntitiesOutput, error) {
	units, err := devices.ParseReadUnits(input.Units)
	if err != nil {
		return nil, huma.NewError(http.StatusBadRequest, "invalid units")
	}
	var deviceID *devices.DeviceID
	if input.DeviceID != "" {
		parsed, parseErr := devices.ParseDeviceID(input.DeviceID)
		if parseErr != nil {
			return nil, huma.NewError(http.StatusBadRequest, "device_id must be a canonical Hearth Device ID")
		}
		deviceID = &parsed
	}
	selector, err := entitySelector(input.LabelIDs, input.Types)
	if err != nil {
		return nil, err
	}
	var afterID *devices.EntityID
	if input.Cursor != "" {
		parsed, cursorErr := apicursor.DecodeEntitiesCursor(input.Cursor, deviceID, selector)
		if cursorErr != nil {
			return nil, huma.NewError(http.StatusBadRequest, "invalid cursor")
		}
		afterID = parsed
	}
	page, err := handler.devices.ListEntities(ctx, devices.ListEntitiesParams{
		DeviceID: deviceID, Selector: selector, AfterID: afterID, Limit: input.Limit,
	})
	if errors.Is(err, devices.ErrInvalidPage) {
		return nil, huma.NewError(http.StatusBadRequest, "invalid page")
	}
	if errors.Is(err, devices.ErrInvalidEntitySelector) {
		return nil, huma.NewError(http.StatusBadRequest, "invalid label_id or type filter")
	}
	if errors.Is(err, devices.ErrLabelNotFound) {
		return nil, huma.NewError(http.StatusBadRequest, "label_id names an unknown Label")
	}
	if err != nil {
		return nil, handler.internalAPIError(ctx, "list-entities", "service", err)
	}
	body := EntityCollectionBody{Items: make([]EntityBody, len(page.Items))}
	for index, entity := range page.Items {
		mapped, mappingErr := handler.entityBody(entity, units)
		if mappingErr != nil {
			return nil, handler.internalAPIError(ctx, "list-entities", "mapping", mappingErr)
		}
		body.Items[index] = mapped
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeEntitiesCursor(
			page.Items[len(page.Items)-1].Entity.ID, deviceID, selector,
		)
		if cursorErr != nil {
			return nil, handler.internalAPIError(ctx, "list-entities", "mapping", cursorErr)
		}
		body.NextCursor = &cursor
	}
	return &ListEntitiesOutput{Body: body}, nil
}

func entitySelector(labelIDs, types []string) (devices.EntitySelector, error) {
	selector := devices.EntitySelector{}
	for _, value := range labelIDs {
		id, err := devices.ParseLabelID(value)
		if err != nil {
			return devices.EntitySelector{}, huma.NewError(
				http.StatusBadRequest,
				"label_id must be a canonical Hearth Label ID",
			)
		}
		selector.Labels = append(selector.Labels, id)
	}
	for _, value := range types {
		selector.Types = append(selector.Types, devices.EntityTypeID(value))
	}
	return selector, nil
}
