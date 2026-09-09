package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Automations is the HTTP consumer's management and history seam.
type Automations interface {
	CreateAutomation(context.Context, automations.AutomationDefinition) (automations.AutomationRecord, error)
	UpdateAutomation(context.Context, automations.AutomationUpdate) (automations.AutomationRecord, error)
	GetAutomation(context.Context, automations.AutomationID) (automations.AutomationRecord, error)
	ListAutomations(
		context.Context,
		automations.AutomationListParams,
	) (automations.AutomationPage[automations.AutomationRecord], error)
	DeleteAutomation(context.Context, automations.AutomationID, int64) error
	StartManualRun(context.Context, automations.AutomationManualRequest) (automations.AutomationAdmission, error)
	GetAutomationRun(context.Context, automations.AutomationRunID) (automations.AutomationRunRecord, error)
	ListAutomationRuns(
		context.Context,
		automations.AutomationRunListParams,
	) (automations.AutomationPage[automations.AutomationRunRecord], error)
	AutomationExecutionReady() bool
}

type automationHandler struct {
	service     Automations
	definitions *automations.AutomationDefinitionCodec
}

// Register owns automation routes; framework validation policy stays local to this group.
func Register(api huma.API, service Automations, definitions *automations.AutomationDefinitionCodec) {
	group := automationAPI{Group: huma.NewGroup(api)}
	handler := &automationHandler{service: service, definitions: definitions}
	publishAutomationDefinitionSchema(group, definitions.AutomationDefinitionSchema())
	create := automationOperation("create-automation", http.MethodPost, "/automations", "Create an Automation")
	create.DefaultStatus = http.StatusCreated
	create.SkipValidateBody = true
	create.RequestBody = automationDefinitionRequestBody()
	huma.Register(group, create, handler.createAutomation)
	huma.Register(
		group,
		automationOperation("list-automations", http.MethodGet, "/automations", "List Automations"),
		handler.listAutomations,
	)
	huma.Register(
		group,
		automationOperation("get-automation", http.MethodGet, "/automations/{automation_id}", "Get an Automation"),
		handler.getAutomation,
	)
	update := automationOperation(
		"update-automation",
		http.MethodPut,
		"/automations/{automation_id}",
		"Replace an Automation",
	)
	update.SkipValidateBody = true
	update.RequestBody = automationDefinitionRequestBody()
	huma.Register(group, update, handler.updateAutomation)
	remove := automationOperation(
		"delete-automation",
		http.MethodDelete,
		"/automations/{automation_id}",
		"Delete an Automation",
	)
	remove.DefaultStatus = http.StatusNoContent
	huma.Register(group, remove, handler.deleteAutomation)
	start := automationOperation(
		"start-automation-run",
		http.MethodPost,
		"/automations/{automation_id}/runs",
		"Start a manual Automation Run",
	)
	start.DefaultStatus = http.StatusAccepted
	runSchema := huma.SchemaFromType(group.OpenAPI().Components.Schemas, reflect.TypeFor[AutomationRunBody]())
	start.Responses["200"] = &huma.Response{
		Description: "Retained invocation reused",
		Content:     map[string]*huma.MediaType{"application/json": {Schema: runSchema}},
		Headers:     map[string]*huma.Param{"Location": {Schema: &huma.Schema{Type: "string"}}},
	}
	start.Middlewares = huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
		if err := automations.ValidateAutomationIdempotencyKey(ctx.Header("Idempotency-Key")); err != nil {
			_ = huma.WriteErr(group, ctx, http.StatusBadRequest, "invalid idempotency key")
			return
		}
		// This endpoint accepts no body, not caller-supplied execution identities.
		var probe [1]byte
		n, err := io.ReadFull(ctx.BodyReader(), probe[:])
		if n != 0 || (err != nil && !errors.Is(err, io.EOF)) {
			_ = huma.WriteErr(group, ctx, http.StatusBadRequest, "manual invocation accepts no body")
			return
		}
		next(ctx)
	}}
	huma.Register(group, start, handler.startAutomationRun)
	huma.Register(
		group,
		automationOperation(
			"list-automation-runs",
			http.MethodGet,
			"/automation-runs",
			"List Automation Run summaries",
		),
		handler.listAutomationRuns,
	)
	huma.Register(
		group,
		automationOperation("get-automation-run", http.MethodGet, "/automation-runs/{run_id}", "Get an Automation Run"),
		handler.getAutomationRun,
	)
}

func automationOperation(id, method, path, summary string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"Automations"},
		Errors: []int{400, 404, 422, 500}, Responses: map[string]*huma.Response{
			"409": automationProblemResponse("Revision conflict or active Run"),
			"503": automationProblemResponse("Automation admission unavailable"),
		}}
}

func (handler *automationHandler) createAutomation(
	ctx context.Context,
	input *CreateAutomationInput,
) (*AutomationOutput, error) {
	definition, err := handler.decodeAutomationDefinition(input.Body)
	if err != nil {
		return nil, automationAPIError(err)
	}
	record, err := handler.service.CreateAutomation(ctx, definition)
	if err != nil {
		return nil, automationAPIError(err)
	}
	return &AutomationOutput{Location: "/v1/automations/" + string(record.ID), Body: automationBody(record)}, nil
}

func (handler *automationHandler) updateAutomation(
	ctx context.Context,
	input *UpdateAutomationInput,
) (*AutomationOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, automationAPIError(err)
	}
	definition, err := handler.decodeAutomationDefinition(input.Body)
	if err != nil {
		return nil, automationAPIError(err)
	}
	record, err := handler.service.UpdateAutomation(
		ctx,
		automations.AutomationUpdate{ID: id, ExpectedRevision: input.ExpectedRevision, Definition: definition},
	)
	if err != nil {
		return nil, automationAPIError(err)
	}
	return &AutomationOutput{Body: automationBody(record)}, nil
}

func (handler *automationHandler) getAutomation(
	ctx context.Context,
	input *GetAutomationInput,
) (*AutomationOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, automationAPIError(err)
	}
	record, err := handler.service.GetAutomation(ctx, id)
	if err != nil {
		return nil, automationAPIError(err)
	}
	return &AutomationOutput{Body: automationBody(record)}, nil
}

func (handler *automationHandler) deleteAutomation(
	ctx context.Context,
	input *DeleteAutomationInput,
) (*struct{}, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, automationAPIError(err)
	}
	if err = handler.service.DeleteAutomation(ctx, id, input.ExpectedRevision); err != nil {
		return nil, automationAPIError(err)
	}
	return &struct{}{}, nil
}

func (handler *automationHandler) startAutomationRun(
	ctx context.Context,
	input *StartAutomationRunInput,
) (*AutomationRunOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, automationAPIError(err)
	}
	admission, err := handler.service.StartManualRun(
		ctx,
		automations.AutomationManualRequest{AutomationID: id, IdempotencyKey: input.IdempotencyKey},
	)
	if err != nil {
		return nil, automationAPIError(err)
	}
	status := http.StatusAccepted
	if admission.Reused {
		status = http.StatusOK
	}
	return &AutomationRunOutput{
		Status:   status,
		Location: "/v1/automation-runs/" + string(admission.Run.ID),
		Body:     automationRunBody(admission.Run),
	}, nil
}

func (handler *automationHandler) getAutomationRun(
	ctx context.Context,
	input *GetAutomationRunInput,
) (*AutomationRunOutput, error) {
	id, err := automations.ParseAutomationRunID(input.RunID)
	if err != nil {
		return nil, automationAPIError(err)
	}
	run, err := handler.service.GetAutomationRun(ctx, id)
	if err != nil {
		return nil, automationAPIError(err)
	}
	return &AutomationRunOutput{Status: http.StatusOK, Body: automationRunBody(run)}, nil
}

func (handler *automationHandler) listAutomations(
	ctx context.Context,
	input *ListAutomationsInput,
) (*AutomationListOutput, error) {
	params := automations.AutomationListParams{Limit: input.Limit}
	if input.Cursor != "" {
		id, err := automationListPosition(input.Cursor)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid automation cursor")
		}
		params.AfterID = id
	}
	page, err := handler.service.ListAutomations(ctx, params)
	if err != nil {
		return nil, automationAPIError(err)
	}
	output := &AutomationListOutput{}
	output.Body.Items = make([]AutomationBody, len(page.Items))
	for i, record := range page.Items {
		output.Body.Items[i] = automationBody(record)
	}
	if page.HasMore && len(page.Items) > 0 {
		output.Body.NextCursor, err = encodeAutomationCursor(
			automationCursor{Version: 1, Resource: "automations", ID: string(page.Items[len(page.Items)-1].ID)},
		)
		if err != nil {
			return nil, automationAPIError(err)
		}
	}
	return output, nil
}

func (handler *automationHandler) listAutomationRuns(
	ctx context.Context,
	input *ListAutomationRunsInput,
) (*AutomationRunListOutput, error) {
	params := automations.AutomationRunListParams{Limit: input.Limit}
	if input.AutomationID != "" {
		id, err := automations.ParseAutomationID(input.AutomationID)
		if err != nil {
			return nil, automationAPIError(err)
		}
		params.AutomationID = &id
	}
	if input.Cursor != "" {
		startedAt, id, err := automationRunListPosition(input.Cursor, input.AutomationID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid automation cursor")
		}
		params.BeforeStartedAt, params.BeforeID = startedAt, id
	}
	page, err := handler.service.ListAutomationRuns(ctx, params)
	if err != nil {
		return nil, automationAPIError(err)
	}
	output := &AutomationRunListOutput{}
	output.Body.Items = make([]AutomationRunSummaryBody, len(page.Items))
	for i, run := range page.Items {
		output.Body.Items[i] = automationRunSummaryBody(run)
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		output.Body.NextCursor, err = encodeAutomationCursor(
			automationRunCursor{
				Version:      1,
				Resource:     "automation_runs",
				AutomationID: input.AutomationID,
				StartedAt:    last.StartedAt.UTC().Format(time.RFC3339Nano),
				ID:           string(last.ID),
			},
		)
		if err != nil {
			return nil, automationAPIError(err)
		}
	}
	return output, nil
}

// decodeAutomationDefinition classifies whitespace-only names and cron expressions
// as semantic (400). The codec normalizes them after validating the original body.
func (handler *automationHandler) decodeAutomationDefinition(
	raw json.RawMessage,
) (automations.AutomationDefinition, error) {
	definition, err := handler.definitions.DecodeAutomationDefinition(raw)
	if err != nil {
		return definition, err
	}
	if definition.Name == "" {
		return automations.AutomationDefinition{}, automations.ErrInvalidAutomation
	}
	for _, trigger := range definition.Triggers {
		if trigger.Expression == "" {
			return automations.AutomationDefinition{}, automations.ErrInvalidAutomation
		}
	}
	return definition, nil
}
