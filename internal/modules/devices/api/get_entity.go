package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Devices interface {
	GetEntity(context.Context, devices.EntityID) (devices.EntityView, error)
	ExecuteCommand(
		context.Context,
		devices.EntityID,
		devices.OperationName,
		devices.CommandParameters,
	) (devices.CommandResult, error)
}

type Handler struct {
	devices Devices
}

type GetEntityInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
}

type GetEntityOutput struct {
	Body EntityBody
}

func Register(api huma.API, devices Devices) {
	handler := &Handler{devices: devices}
	huma.Register(api, huma.Operation{
		OperationID: "get-entity",
		Method:      http.MethodGet,
		Path:        "/{entity_id}",
		Summary:     "Get an Entity and its current State",
		Tags:        []string{"Entities"},
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError},
	}, handler.GetEntity)
	huma.Register(api, huma.Operation{
		OperationID: "execute-entity-command",
		Method:      http.MethodPost,
		Path:        "/{entity_id}/commands",
		Summary:     "Execute an Entity Command",
		Tags:        []string{"Entities"},
		Errors: []int{
			http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout, http.StatusInternalServerError,
		},
	}, handler.ExecuteCommand)
	removeAutoValidationResponses(api, "get-entity", "execute-entity-command")
}

func removeAutoValidationResponses(api huma.API, operationIDs ...string) {
	selected := make(map[string]struct{}, len(operationIDs))
	for _, operationID := range operationIDs {
		selected[operationID] = struct{}{}
	}
	for _, path := range api.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{path.Get, path.Post} {
			if operation == nil {
				continue
			}
			if _, ok := selected[operation.OperationID]; ok {
				// Huma defaults structural validation to 422. Hearth maps those
				// failures to its stable invalid_request HTTP 400 response.
				delete(operation.Responses, "422")
			}
		}
	}
}

func (handler *Handler) GetEntity(ctx context.Context, input *GetEntityInput) (*GetEntityOutput, error) {
	entityID, err := devices.ParseEntityID(input.EntityID)
	if err != nil {
		return nil, apiError(http.StatusBadRequest, "invalid_request", "entity_id must be a canonical Hearth Entity ID")
	}
	view, err := handler.devices.GetEntity(ctx, entityID)
	if errors.Is(err, devices.ErrEntityNotFound) {
		return nil, apiError(http.StatusNotFound, "entity_not_found", "entity not found")
	}
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal_error", "internal error")
	}
	body, err := entityBody(view)
	if err != nil {
		return nil, apiError(http.StatusInternalServerError, "internal_error", "internal error")
	}
	return &GetEntityOutput{Body: body}, nil
}

func entityBody(view devices.EntityView) (EntityBody, error) {
	var support map[string]any
	if err := decodeJSON(view.Entity.Support, &support); err != nil {
		return EntityBody{}, fmt.Errorf("decode entity support: %w", err)
	}
	body := EntityBody{
		ID:       string(view.Entity.ID),
		DeviceID: string(view.Entity.DeviceID),
		Name:     view.Entity.Name,
		Type:     string(view.Entity.TypeID),
		Support:  support,
	}
	if view.State == nil {
		return body, nil
	}
	var value any
	if err := decodeJSON(view.State.Value, &value); err != nil {
		return EntityBody{}, fmt.Errorf("decode entity state: %w", err)
	}
	state := &StateBody{
		Value:             value,
		ObservationID:     string(view.State.ObservationID),
		AdapterReceivedAt: formatTime(view.State.AdapterReceivedAt),
		ObservedAt:        formatTime(view.State.ObservedAt),
	}
	if view.State.SourceUpdatedAt != nil {
		sourceUpdatedAt := formatTime(*view.State.SourceUpdatedAt)
		state.SourceUpdatedAt = &sourceUpdatedAt
	}
	body.State = state
	return body, nil
}

func decodeJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func apiError(status int, code, message string) error {
	return NewStatusError(status, code, message)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
