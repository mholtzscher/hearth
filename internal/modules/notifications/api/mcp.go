package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

type pageToolInput struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

const (
	schemaType           = "type"
	schemaString         = "string"
	maximumPageLimit     = 200
	maximumTitleLength   = 256
	maximumMessageLength = 2000
)

type destinationToolInput struct {
	DestinationID string `json:"destination_id"`
}
type attemptToolInput struct {
	ID string `json:"id"`
}
type sendToolInput struct {
	DestinationID string `json:"destination_id"`
	Title         string `json:"title,omitempty"`
	Message       string `json:"message"`
}
type attemptListToolInput struct {
	Limit         int    `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	DestinationID string `json:"destination_id,omitempty"`
}

// toolSchema makes omission and null semantics explicit. The MCP SDK validates
// against this schema before Go's decoder can coerce a null string or integer.
func toolSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		schemaType:             "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}
func stringSchema() map[string]any { return map[string]any{schemaType: schemaString} }
func pageProperties() map[string]any {
	return map[string]any{
		"limit":  map[string]any{schemaType: "integer", "minimum": 1, "maximum": maximumPageLimit},
		"cursor": stringSchema(),
	}
}
func RegisterMCP(server *mcpapi.Server, service Notifications) {
	h := &Handler{service: service}
	mcpapi.Register(
		server,
		mcpapi.Tool[pageToolInput, DestinationCollectionBody]{Name: "list_notification_destinations",
			Description: "List notification destinations", ExactOutput: true, InputSchema: toolSchema(pageProperties()),
			Handler: func(ctx context.Context, in pageToolInput) (DestinationCollectionBody, error) {
				out, err := h.ListDestinations(ctx, &PageInput{Limit: in.Limit, Cursor: in.Cursor})
				if err != nil {
					return DestinationCollectionBody{}, mcpError(err)
				}
				return out.Body, nil
			}},
	)
	mcpapi.Register(server, mcpapi.Tool[destinationToolInput, DestinationBody]{Name: "get_notification_destination",
		Description: "Get a notification destination", ExactOutput: true,
		InputSchema: toolSchema(map[string]any{"destination_id": stringSchema()}, "destination_id"),
		Handler: func(ctx context.Context, in destinationToolInput) (DestinationBody, error) {
			out, err := h.GetDestination(ctx, &DestinationInput{ID: in.DestinationID})
			if err != nil {
				return DestinationBody{}, mcpError(err)
			}
			return out.Body, nil
		}})
	mcpapi.Register(server, mcpapi.Tool[sendToolInput, AttemptBody]{
		Name:        "send_notification",
		Description: "Send once; repeating this invocation creates a new attempt and may deliver another message",
		ExactOutput: true,
		InputSchema: toolSchema(map[string]any{
			"destination_id": stringSchema(),
			"title":          map[string]any{schemaType: schemaString, "maxLength": maximumTitleLength},
			"message": map[string]any{
				schemaType:  schemaString,
				"minLength": 1,
				"maxLength": maximumMessageLength,
			},
		}, "destination_id", "message"),
		Handler: func(ctx context.Context, in sendToolInput) (AttemptBody, error) {
			out, err := h.Send(
				ctx,
				&SendInput{ID: in.DestinationID, Body: SendBody{Title: in.Title, Message: in.Message}},
			)
			if err != nil {
				return AttemptBody{}, mcpError(err)
			}
			return out.Body, nil
		},
	})
	properties := pageProperties()
	properties["destination_id"] = stringSchema()
	mcpapi.Register(server, mcpapi.Tool[attemptListToolInput, AttemptCollectionBody]{Name: "list_notifications",
		Description: "List standalone notification history", ExactOutput: true, InputSchema: toolSchema(properties),
		Handler: func(ctx context.Context, in attemptListToolInput) (AttemptCollectionBody, error) {
			out, err := h.ListAttempts(
				ctx,
				&AttemptListInput{
					Limit: in.Limit, Cursor: in.Cursor,
					DestinationID: in.DestinationID,
				},
			)
			if err != nil {
				return AttemptCollectionBody{}, mcpError(err)
			}
			return out.Body, nil
		}})
	mcpapi.Register(server, mcpapi.Tool[attemptToolInput, AttemptBody]{Name: "get_notification",
		Description: "Get a retained notification attempt", ExactOutput: true,
		InputSchema: toolSchema(map[string]any{"id": stringSchema()}, "id"),
		Handler: func(ctx context.Context, in attemptToolInput) (AttemptBody, error) {
			out, err := h.GetAttempt(ctx, &AttemptInput{ID: in.ID})
			if err != nil {
				return AttemptBody{}, mcpError(err)
			}
			return out.Body, nil
		}})
}
func mcpError(err error) error {
	if status, ok := errors.AsType[huma.StatusError](err); ok {
		switch status.GetStatus() {
		case http.StatusUnprocessableEntity:
			return &mcpapi.ToolError{Code: "invalid_input", Message: "invalid notification input"}
		case http.StatusNotFound:
			return &mcpapi.ToolError{Code: "not_found", Message: "notification resource not found"}
		case http.StatusServiceUnavailable:
			return &mcpapi.ToolError{Code: "admission_closed", Message: "notification admission closed"}
		}
	}
	return mcpapi.InternalToolError(err)
}
