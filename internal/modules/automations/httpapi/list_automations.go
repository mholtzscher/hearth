package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/apicursor"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerListAutomations(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-automations",
		Method:      http.MethodGet,
		Path:        "/automations",
		Summary:     "List Automations",
		Tags:        []string{"Automations"},
		Responses: problemResponses(api,
			http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusInternalServerError),
	}, handler.listAutomations)
}

// ListAutomationsInput uses an opaque ascending-ID continuation.
type ListAutomationsInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

// AutomationCollectionOutput is one ID-ascending keyset page.
type AutomationCollectionOutput struct {
	Body AutomationCollectionBody
}

func (handler *handler) listAutomations(
	ctx context.Context,
	input *ListAutomationsInput,
) (*AutomationCollectionOutput, error) {
	params := automations.ListAutomationsParams{Limit: input.Limit}
	if input.Cursor != "" {
		afterID, err := apicursor.DecodeAutomations(input.Cursor)
		if err != nil {
			return nil, newProblem(http.StatusBadRequest, "invalid_cursor", "automation cursor is invalid")
		}
		params.AfterID = afterID
	}
	page, err := handler.automations.ListAutomations(ctx, params)
	if err != nil {
		return nil, handler.mapError(ctx, "list-automations", err)
	}
	output := &AutomationCollectionOutput{}
	output.Body.Items = make([]AutomationBody, len(page.Items))
	for index, record := range page.Items {
		output.Body.Items[index] = automationBody(record)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := apicursor.EncodeAutomations(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			handler.logInternalFailure(ctx, "list-automations", "mapping", cursorErr)
			return nil, newProblem(http.StatusInternalServerError, "internal_error", "internal error")
		}
		output.Body.NextCursor = &cursor
	}
	return output, nil
}

// AutomationCollectionBody always returns an array, including empty pages.
type AutomationCollectionBody struct {
	Items      []AutomationBody `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}
