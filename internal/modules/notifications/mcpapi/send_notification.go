package mcpapi

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type SendInput struct {
	DestinationID string `json:"destination_id"`
	Title         string `json:"title,omitempty"`
	Message       string `json:"message"`
}
type NotificationReceipt struct {
	NotificationID string `json:"notification_id"`
	DestinationID  string `json:"destination_id"`
	Status         string `json:"status"`
	ReasonCode     string `json:"reason_code,omitempty"`
	Receipt        string `json:"receipt,omitempty"`
}

func registerSendNotification(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[SendInput, NotificationReceipt]{
		Name:        "send_notification",
		Description: "Send once and return an outcome receipt; read get_notification with id=notification_id for evidence. Repeating may send another message; accepted means provider acceptance, not verified delivery",
		InputSchema: sendInputSchema(),
		Handler:     h.send,
	})
}

func sendInputSchema() *jsonschema.Schema {
	schema, err := jsonschema.For[SendInput](nil)
	if err != nil {
		panic(err)
	}
	schema.Properties["title"].MaxLength = new(maximumTitleLength)
	schema.Properties["message"].MinLength = new(1)
	schema.Properties["message"].MaxLength = new(maximumMessageLength)
	return schema
}

func (h *handler) send(ctx context.Context, in SendInput) (NotificationReceipt, error) {
	id, err := notifications.NewAttemptID()
	if err != nil {
		return NotificationReceipt{}, mcptools.InternalToolError(err)
	}
	a, err := h.service.SendPublic(ctx, notifications.SendInput{
		AttemptID: id, DestinationID: notifications.DestinationID(in.DestinationID),
		Message: notifications.Message{Title: in.Title, Text: in.Message},
	})
	if err != nil {
		return NotificationReceipt{}, toolError(err)
	}
	return NotificationReceipt{NotificationID: string(a.ID), DestinationID: string(a.DestinationID),
		Status: string(a.Outcome), ReasonCode: a.ReasonCode, Receipt: a.Receipt}, nil
}
