package mcpapi

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListEntityCommands(server *mcptools.Server, h *handler) {
	mcptools.Register(
		server,
		mcptools.Tool[ListEntityCommandsInput, Page[CommandSummary]]{
			Name:        "list_entity_commands",
			Description: "List compact Command history; use get_command for parameters and deadline.",
			Handler:     h.listEntityCommands,
		},
	)
}

type ListEntityCommandsInput struct {
	pageInput

	EntityID entityID `json:"entity_id" jsonschema:"canonical Hearth Entity ID"`
}

func (handler *handler) listEntityCommands(
	ctx context.Context,
	input ListEntityCommandsInput,
) (Page[CommandSummary], error) {
	entityID := devices.EntityID(input.EntityID)
	params := devices.ListEntityCommandsParams{EntityID: entityID, Limit: pageSize(input.Limit)}
	if input.Cursor != "" {
		requestedAt, commandID, cursorErr := apicursor.DecodeCommandCursor(input.Cursor, entityID)
		if cursorErr != nil {
			return Page[CommandSummary]{}, invalidRequestFailure("invalid cursor")
		}
		params.BeforeRequestedAt = requestedAt
		params.BeforeID = commandID
	}
	page, err := handler.devices.ListEntityCommands(ctx, params)
	switch {
	case errors.Is(err, devices.ErrInvalidPage):
		return Page[CommandSummary]{}, invalidRequestFailure("invalid page")
	case errors.Is(err, devices.ErrEntityNotFound):
		return Page[CommandSummary]{}, toolError(failureEntityNotFound, "entity not found")
	case err != nil:
		return Page[CommandSummary]{}, mcptools.InternalToolError(err)
	}
	body := Page[CommandSummary]{Items: make([]CommandSummary, len(page.Items))}
	for index, command := range page.Items {
		body.Items[index] = commandSummary(command)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeCommandCursor(page.Items[len(page.Items)-1])
		if cursorErr != nil {
			return Page[CommandSummary]{}, mcptools.InternalToolError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return body, nil
}

func (input *ListEntityCommandsInput) UnmarshalJSON(data []byte) error {
	type arguments ListEntityCommandsInput
	var decoded arguments
	if err := decodeArguments(data, &decoded); err != nil {
		return err
	}
	*input = ListEntityCommandsInput(decoded)
	return nil
}
