package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerDeleteAutomation(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID:   "delete-automation",
		Method:        http.MethodDelete,
		Path:          "/automations/{automation_id}",
		Summary:       "Delete an Automation",
		Tags:          []string{"Automations"},
		DefaultStatus: http.StatusNoContent,
		Responses: problemResponses(api,
			http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
			http.StatusInternalServerError, http.StatusServiceUnavailable),
	}, handler.deleteAutomation)
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
		return nil, handler.mapError(ctx, "delete-automation", err)
	}
	return &struct{}{}, nil
}
