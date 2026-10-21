package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func registerDeleteAutomation(api huma.API, handler *handler) {
	remove := operation("delete-automation", http.MethodDelete, "/automations/{automation_id}", "Delete an Automation")
	remove.DefaultStatus = http.StatusNoContent
	remove.Errors = []int{
		http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
		http.StatusInternalServerError,
	}
	huma.Register(api, remove, handler.deleteAutomation)
}

// DeleteAutomationInput requires optimistic revision concurrency.
type DeleteAutomationInput struct {
	AutomationID     string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	ExpectedRevision int64  `                                                          query:"expected_revision" required:"true" minimum:"1"`
}

func (handler *handler) deleteAutomation(
	ctx context.Context,
	input *DeleteAutomationInput,
) (*struct{}, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	if err = handler.automations.DeleteAutomation(ctx, id, input.ExpectedRevision); err != nil {
		return nil, mapDomainError(err)
	}
	return &struct{}{}, nil
}
