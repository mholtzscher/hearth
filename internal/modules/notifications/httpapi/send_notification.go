package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type SendInput struct {
	ID   string `path:"id"`
	Body SendBody
}
type SendOutput struct{ Body AttemptBody }

type SendBody struct {
	Title   string `json:"title,omitempty" maxLength:"256"`
	Message string `json:"message"         maxLength:"2000" minLength:"1"`
}

// UnmarshalJSON closes Go's null-string and invalid Unicode coercions as well as
// rejecting unknown members, including public attempt identities.
func (body *SendBody) UnmarshalJSON(data []byte) error {
	// Check raw escapes before encoding/json can replace unpaired surrogates.
	// Preserve the existing duplicate-member policy while validating Unicode.
	if !jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true)) {
		return errors.New("send body must contain valid JSON and Unicode")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return errors.New("send body must be a JSON object")
	}
	var decoded SendBody
	for key, value := range members {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("send fields cannot be null")
		}
		switch key {
		case "title":
			if err := json.Unmarshal(value, &decoded.Title); err != nil {
				return errors.New("title must be a string")
			}
		case "message":
			if err := json.Unmarshal(value, &decoded.Message); err != nil {
				return errors.New("message must be a string")
			}
		default:
			return errors.New("unknown send field")
		}
	}
	if _, ok := members["message"]; !ok {
		return errors.New("message is required")
	}
	*body = decoded
	return nil
}

func registerSendNotification(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "send-notification", Method: http.MethodPost, DefaultStatus: http.StatusCreated,
		Path: "/notification-destinations/{id}/notifications", Summary: "Send a notification without retry",
		Tags: []string{notificationTag},
	}, h.send)
}

func (h *handler) send(ctx context.Context, in *SendInput) (*SendOutput, error) {
	id, err := notifications.NewAttemptID()
	if err != nil {
		return nil, h.mapError(ctx, "send-notification", err)
	}
	a, err := h.service.SendPublic(ctx, notifications.SendInput{
		AttemptID: id, DestinationID: notifications.DestinationID(in.ID),
		Message: notifications.Message{Title: in.Body.Title, Text: in.Body.Message},
	})
	if err != nil {
		return nil, h.mapError(ctx, "send-notification", err)
	}
	return &SendOutput{Body: attemptBody(a)}, nil
}
