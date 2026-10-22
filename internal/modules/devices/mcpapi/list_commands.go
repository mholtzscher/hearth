package mcpapi

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListCommands(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListCommandsInput, Page[CommandSummary]]{
			Name:        "list_commands",
			Description: "List compact household Command history; use get_command for detail.",
			Handler:     h.listCommands,
		},
	)
}

type ListCommandsInput struct {
	PageInput

	EntityID *entityID `json:"entity_id,omitempty" doc:"canonical Hearth Entity ID; omit to list every Entity's Commands" nullable:"true"`
	Status   string    `json:"status,omitempty"    doc:"Command lifecycle status filter, such as satisfied"`
}

func (handler *handler) listCommands(ctx context.Context, input ListCommandsInput) (Page[CommandSummary], error) {
	params := devices.ListCommandsParams{Limit: pageSize(input.Limit)}
	if input.EntityID != nil {
		value := devices.EntityID(*input.EntityID)
		params.EntityID = &value
	}
	if input.Status != "" {
		status := devices.CommandStatus(input.Status)
		if !devices.ValidCommandStatus(status) {
			return Page[CommandSummary]{}, invalidRequestFailure("invalid status")
		}
		params.Status = &status
	}
	if input.Cursor != "" {
		requestedAt, commandID, cursorErr := apicursor.DecodeCommandListCursor(
			input.Cursor,
			params.EntityID,
			params.Status,
		)
		if cursorErr != nil {
			return Page[CommandSummary]{}, invalidRequestFailure("invalid cursor")
		}
		params.BeforeRequestedAt = requestedAt
		params.BeforeID = commandID
	}
	page, err := handler.devices.ListCommands(ctx, params)
	if errors.Is(err, devices.ErrInvalidPage) {
		return Page[CommandSummary]{}, invalidRequestFailure("invalid page")
	}
	if err != nil {
		return Page[CommandSummary]{}, mcptools.InternalToolError(err)
	}
	body := Page[CommandSummary]{Items: make([]CommandSummary, len(page.Items))}
	for index, command := range page.Items {
		body.Items[index] = commandSummary(command)
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor, cursorErr := apicursor.EncodeCommandListCursor(last, params.EntityID, params.Status)
		if cursorErr != nil {
			return Page[CommandSummary]{}, mcptools.InternalToolError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return body, nil
}

func (input *ListCommandsInput) UnmarshalJSON(data []byte) error {
	type arguments ListCommandsInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = ListCommandsInput(decoded)
	return nil
}
