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
	ListAutomationOccurrences(
		context.Context,
		automations.AutomationOccurrenceListParams,
	) (automations.AutomationPage[automations.AutomationOccurrence], error)
	ListAutomationScheduleGaps(
		context.Context,
		automations.AutomationScheduleGapListParams,
	) (automations.AutomationPage[automations.AutomationScheduleGap], error)
	HouseholdTimezone() *time.Location
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
	huma.Register(
		group,
		automationOperation(
			"list-automation-occurrences",
			http.MethodGet,
			"/automation-occurrences",
			"List Automation Occurrences",
		),
		handler.listAutomationOccurrences,
	)
	huma.Register(
		group,
		automationOperation(
			"list-automation-schedule-gaps",
			http.MethodGet,
			"/automation-schedule-gaps",
			"List Automation Schedule Gaps",
		),
		handler.listAutomationScheduleGaps,
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
	return &AutomationOutput{
		Location: "/v1/automations/" + string(record.ID),
		Body:     automationBody(record, handler.householdTimezone()),
	}, nil
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
	return &AutomationOutput{Body: automationBody(record, handler.householdTimezone())}, nil
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
	return &AutomationOutput{Body: automationBody(record, handler.householdTimezone())}, nil
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
		output.Body.Items[i] = automationBody(record, handler.householdTimezone())
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

//nolint:dupl // Filtered newest-first histories are parallel endpoints over distinct run and occurrence contracts.
func (handler *automationHandler) listAutomationRuns(
	ctx context.Context,
	input *ListAutomationRunsInput,
) (*AutomationRunListOutput, error) {
	items, next, err := listFilteredAutomationHistory(
		ctx,
		input.Limit,
		input.AutomationID,
		input.Cursor,
		func(limit int, automationID *automations.AutomationID) automations.AutomationRunListParams {
			return automations.AutomationRunListParams{Limit: limit, AutomationID: automationID}
		},
		func(params *automations.AutomationRunListParams, cursor, filter string) error {
			startedAt, id, positionErr := automationRunListPosition(cursor, filter)
			if positionErr != nil {
				return positionErr
			}
			params.BeforeStartedAt, params.BeforeID = startedAt, id
			return nil
		},
		handler.service.ListAutomationRuns,
		automationRunSummaryBody,
		func(filter string) func(automations.AutomationRunRecord) any {
			return func(run automations.AutomationRunRecord) any {
				return automationRunCursor{
					Version:      1,
					Resource:     "automation_runs",
					AutomationID: filter,
					StartedAt:    run.StartedAt.UTC().Format(time.RFC3339Nano),
					ID:           string(run.ID),
				}
			}
		},
	)
	if err != nil {
		return nil, err
	}
	output := &AutomationRunListOutput{}
	output.Body.Items, output.Body.NextCursor = items, next
	return output, nil
}

//nolint:dupl // Filtered newest-first histories are parallel endpoints over distinct run and occurrence contracts.
func (handler *automationHandler) listAutomationOccurrences(
	ctx context.Context,
	input *ListAutomationOccurrencesInput,
) (*AutomationOccurrenceListOutput, error) {
	items, next, err := listFilteredAutomationHistory(
		ctx,
		input.Limit,
		input.AutomationID,
		input.Cursor,
		func(limit int, automationID *automations.AutomationID) automations.AutomationOccurrenceListParams {
			return automations.AutomationOccurrenceListParams{Limit: limit, AutomationID: automationID}
		},
		func(params *automations.AutomationOccurrenceListParams, cursor, filter string) error {
			scheduledAt, id, positionErr := automationOccurrenceListPosition(cursor, filter)
			if positionErr != nil {
				return positionErr
			}
			params.BeforeScheduledAt, params.BeforeAutomationID = scheduledAt, id
			return nil
		},
		handler.service.ListAutomationOccurrences,
		automationOccurrenceBody,
		func(filter string) func(automations.AutomationOccurrence) any {
			return func(occurrence automations.AutomationOccurrence) any {
				return automationOccurrenceCursor{
					Version:      1,
					Resource:     "automation_occurrences",
					AutomationID: filter,
					ScheduledAt:  occurrence.ScheduledAt.UTC().Format(time.RFC3339Nano),
					ID:           string(occurrence.AutomationID),
				}
			}
		},
	)
	if err != nil {
		return nil, err
	}
	output := &AutomationOccurrenceListOutput{}
	output.Body.Items, output.Body.NextCursor = items, next
	return output, nil
}

func (handler *automationHandler) listAutomationScheduleGaps(
	ctx context.Context,
	input *ListAutomationScheduleGapsInput,
) (*AutomationScheduleGapListOutput, error) {
	params := automations.AutomationScheduleGapListParams{Limit: input.Limit}
	if input.Cursor != "" {
		recordedAt, id, err := automationScheduleGapListPosition(input.Cursor)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid automation cursor")
		}
		params.BeforeRecordedAt, params.BeforeID = recordedAt, id
	}
	page, err := handler.service.ListAutomationScheduleGaps(ctx, params)
	if err != nil {
		return nil, automationAPIError(err)
	}
	output := &AutomationScheduleGapListOutput{}
	output.Body.Items, output.Body.NextCursor, err = automationHistoryList(
		page,
		automationScheduleGapBody,
		func(gap automations.AutomationScheduleGap) any {
			return automationScheduleGapCursor{
				Version:    1,
				Resource:   "automation_schedule_gaps",
				RecordedAt: gap.RecordedAt.UTC().Format(time.RFC3339Nano),
				ID:         gap.ID,
			}
		},
	)
	if err != nil {
		return nil, automationAPIError(err)
	}
	return output, nil
}

// listFilteredAutomationHistory resolves the optional automation filter and its
// filter-bound cursor, lists one newest-first page, and maps it to transport
// bodies with its continuation cursor.
func listFilteredAutomationHistory[Params, Record, Body any](
	ctx context.Context,
	limit int,
	automationFilter string,
	cursor string,
	makeParams func(int, *automations.AutomationID) Params,
	applyCursor func(*Params, string, string) error,
	list func(context.Context, Params) (automations.AutomationPage[Record], error),
	convert func(Record) Body,
	position func(string) func(Record) any,
) ([]Body, string, error) {
	var automationID *automations.AutomationID
	if automationFilter != "" {
		parsed, err := automations.ParseAutomationID(automationFilter)
		if err != nil {
			return nil, "", automationAPIError(err)
		}
		automationID = &parsed
	}
	params := makeParams(limit, automationID)
	if cursor != "" {
		if err := applyCursor(&params, cursor, automationFilter); err != nil {
			return nil, "", huma.Error400BadRequest("invalid automation cursor")
		}
	}
	page, err := list(ctx, params)
	if err != nil {
		return nil, "", automationAPIError(err)
	}
	return automationHistoryList(page, convert, position(automationFilter))
}

// automationHistoryList maps one descending history page to transport bodies
// and mints its filter-bound continuation cursor. Position closures keep each
// collection's cursor resource and filter binding next to its position parser.
func automationHistoryList[Record, Body any](
	page automations.AutomationPage[Record],
	convert func(Record) Body,
	position func(Record) any,
) ([]Body, string, error) {
	bodies := make([]Body, len(page.Items))
	for i, record := range page.Items {
		bodies[i] = convert(record)
	}
	if !page.HasMore || len(page.Items) == 0 {
		return bodies, "", nil
	}
	cursor, err := encodeAutomationCursor(position(page.Items[len(page.Items)-1]))
	if err != nil {
		return nil, "", err
	}
	return bodies, cursor, nil
}

// householdTimezone reports the process household zone for definition reads.
// The service always carries the startup-loaded zone; an absent zone degrades
// to an empty value rather than failing diagnostic history reads.
func (handler *automationHandler) householdTimezone() string {
	if zone := handler.service.HouseholdTimezone(); zone != nil {
		return zone.String()
	}
	return ""
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
