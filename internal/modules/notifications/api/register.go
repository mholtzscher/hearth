package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type Notifications interface {
	GetDestination(context.Context, notifications.DestinationID) (notifications.Destination, error)
	ListDestinations(context.Context, int, string) (notifications.DestinationPage, error)
	SendPublic(context.Context, notifications.SendInput) (notifications.Attempt, error)
	GetAttempt(context.Context, notifications.AttemptID) (notifications.Attempt, error)
	ListAttempts(context.Context, notifications.AttemptFilter) (notifications.AttemptPage, error)
}
type Handler struct{ service Notifications }
type PageInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}
type DestinationInput struct {
	ID string `path:"id"`
}
type AttemptInput struct {
	ID string `path:"id"`
}
type SendInput struct {
	ID   string `path:"id"`
	Body SendBody
}
type AttemptListInput struct {
	PageInput

	DestinationID string `query:"destination_id"`
}
type DestinationOutput struct{ Body DestinationBody }
type DestinationListOutput struct{ Body DestinationCollectionBody }
type AttemptOutput struct{ Body AttemptBody }
type AttemptListOutput struct{ Body AttemptCollectionBody }

func Register(api huma.API, service Notifications) {
	const notificationTag = "Notifications"
	h := &Handler{service: service}
	huma.Register(api, huma.Operation{
		OperationID: "list-notification-destinations",
		Method:      http.MethodGet,
		Path:        "/notification-destinations",
		Summary:     "List notification destinations",
		Tags:        []string{notificationTag},
	}, h.ListDestinations)
	huma.Register(api, huma.Operation{
		OperationID: "get-notification-destination",
		Method:      http.MethodGet,
		Path:        "/notification-destinations/{id}",
		Summary:     "Get a notification destination",
		Tags:        []string{notificationTag},
	}, h.GetDestination)
	huma.Register(
		api,
		huma.Operation{
			OperationID:   "send-notification",
			Method:        http.MethodPost,
			DefaultStatus: http.StatusCreated,
			Path:          "/notification-destinations/{id}/notifications",
			Summary:       "Send a notification without retry",
			Tags:          []string{notificationTag},
		},
		h.Send,
	)
	huma.Register(api, huma.Operation{OperationID: "list-notifications", Method: http.MethodGet,
		Path: "/notifications", Summary: "List notification attempts", Tags: []string{notificationTag}}, h.ListAttempts)
	huma.Register(api, huma.Operation{
		OperationID: "get-notification",
		Method:      http.MethodGet,
		Path:        "/notifications/{id}",
		Summary:     "Get a notification attempt",
		Tags:        []string{notificationTag},
	}, h.GetAttempt)
}
func (h *Handler) ListDestinations(ctx context.Context, in *PageInput) (*DestinationListOutput, error) {
	page, err := h.service.ListDestinations(ctx, in.Limit, in.Cursor)
	if err != nil {
		return nil, mapError(err)
	}
	body := DestinationCollectionBody{
		Items:      make([]DestinationBody, 0, len(page.Items)),
		NextCursor: nextCursor(page.NextCursor),
	}
	for _, d := range page.Items {
		body.Items = append(body.Items, destinationBody(d))
	}
	return &DestinationListOutput{Body: body}, nil
}
func (h *Handler) GetDestination(ctx context.Context, in *DestinationInput) (*DestinationOutput, error) {
	d, err := h.service.GetDestination(ctx, notifications.DestinationID(in.ID))
	if err != nil {
		return nil, mapError(err)
	}
	return &DestinationOutput{Body: destinationBody(d)}, nil
}
func (h *Handler) Send(ctx context.Context, in *SendInput) (*AttemptOutput, error) {
	id, err := notifications.NewAttemptID()
	if err != nil {
		return nil, mapError(err)
	}
	a, err := h.service.SendPublic(
		ctx,
		notifications.SendInput{AttemptID: id, DestinationID: notifications.DestinationID(in.ID),
			Message: notifications.Message{Title: in.Body.Title, Text: in.Body.Message}},
	)
	if err != nil {
		return nil, mapError(err)
	}
	return &AttemptOutput{Body: attemptBody(a)}, nil
}
func (h *Handler) ListAttempts(ctx context.Context, in *AttemptListInput) (*AttemptListOutput, error) {
	page, err := h.service.ListAttempts(
		ctx,
		notifications.AttemptFilter{DestinationID: notifications.DestinationID(in.DestinationID),
			Limit: in.Limit, Cursor: in.Cursor},
	)
	if err != nil {
		return nil, mapError(err)
	}
	body := AttemptCollectionBody{
		Items:      make([]AttemptBody, 0, len(page.Items)),
		NextCursor: nextCursor(page.NextCursor),
	}
	for _, a := range page.Items {
		body.Items = append(body.Items, attemptBody(a))
	}
	return &AttemptListOutput{Body: body}, nil
}
func (h *Handler) GetAttempt(ctx context.Context, in *AttemptInput) (*AttemptOutput, error) {
	a, err := h.service.GetAttempt(ctx, notifications.AttemptID(in.ID))
	if err != nil {
		return nil, mapError(err)
	}
	return &AttemptOutput{Body: attemptBody(a)}, nil
}
func mapError(err error) error {
	switch {
	case errors.Is(err, notifications.ErrInvalidInput):
		return huma.Error422UnprocessableEntity("invalid notification input")
	case errors.Is(err, notifications.ErrDestinationNotFound):
		return huma.Error404NotFound("notification destination not found")
	case errors.Is(err, notifications.ErrAttemptNotFound):
		return huma.Error404NotFound("notification attempt not found")
	case errors.Is(err, notifications.ErrAdmissionClosed):
		return huma.Error503ServiceUnavailable("notification admission closed")
	default:
		return huma.Error500InternalServerError("internal error")
	}
}
