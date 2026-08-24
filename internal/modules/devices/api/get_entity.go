package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type Handler struct {
	devices *devices.Service
}

type GetEntityInput struct {
	EntityID string `path:"entity_id" doc:"Canonical Hearth Entity ID"`
}

type GetEntityOutput struct {
	Body EntityBody
}

var (
	configureErrorsOnce = sync.Once{}
	defaultHumaNewError = huma.NewError
)

func Register(api huma.API, service *devices.Service) {
	configureErrorsOnce.Do(func() {
		huma.NewError = func(status int, message string, details ...error) huma.StatusError {
			switch status {
			case http.StatusBadRequest, http.StatusRequestEntityTooLarge,
				http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
				return apiError(http.StatusBadRequest, "invalid_request", "invalid request").(*statusError)
			default:
				return defaultHumaNewError(status, message, details...)
			}
		}
	})
	handler := &Handler{devices: service}
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
	return &statusError{
		status:    status,
		ErrorBody: ErrorBody{Error: APIError{Code: code, Message: message}},
	}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
