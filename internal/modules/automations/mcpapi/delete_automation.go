package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerDeleteAutomation(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[deleteAutomationToolInput, deleteAutomationToolOutput]{
		Name:        "delete_automation",
		Description: "Delete an Automation at its expected revision and return a deletion receipt",
		Handler:     handler.deleteAutomation,
	})
}

type deleteAutomationToolInput struct {
	AutomationID     automationID     `json:"automation_id"     doc:"Canonical Hearth Automation ID"`
	ExpectedRevision expectedRevision `json:"expected_revision" doc:"Current revision the deletion expects"`
}

// deleteAutomationToolOutput confirms one hard deletion. The revision member is
// named deleted_revision so it reads as the revision that was deleted rather
// than a revision the deletion produced.
type deleteAutomationToolOutput struct {
	AutomationID    string `json:"automation_id"    minLength:"1"`
	DeletedRevision int64  `json:"deleted_revision"               minimum:"1"`
	Deleted         bool   `json:"deleted"                                    const:"true"`
}

func (handler *handler) deleteAutomation(
	ctx context.Context,
	input deleteAutomationToolInput,
) (deleteAutomationToolOutput, error) {
	id := automations.AutomationID(input.AutomationID)
	if err := handler.automations.DeleteAutomation(ctx, id, int64(input.ExpectedRevision)); err != nil {
		return deleteAutomationToolOutput{}, automationFailure(err, failureAutomationNotFound)
	}
	return deleteAutomationToolOutput{
		AutomationID:    string(id),
		DeletedRevision: int64(input.ExpectedRevision),
		Deleted:         true,
	}, nil
}
