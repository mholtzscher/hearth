package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Automations exposes definition management, manual admission, and history to
// HTTP handlers, excluding Fact admission and execution-state writes.
type Automations interface {
	CreateAutomation(context.Context, automations.Definition) (automations.Record, error)
	GetAutomation(context.Context, automations.AutomationID) (automations.Record, error)
	ListAutomations(
		context.Context,
		automations.ListAutomationsParams,
	) (automations.Page[automations.Record], error)
	ReplaceAutomation(
		context.Context,
		automations.AutomationID,
		int64,
		automations.Definition,
	) (automations.Record, error)
	DeleteAutomation(context.Context, automations.AutomationID, int64) error
	StartManualRun(context.Context, automations.ManualRunInput) (automations.Run, error)
	GetHistoryEntry(context.Context, automations.AutomationID, string) (automations.HistoryEntry, error)
	ListHistory(
		context.Context,
		automations.ListHistoryParams,
	) (automations.Page[automations.HistorySummary], error)
}

// Handler owns the automation HTTP operations.
type Handler struct {
	automations Automations
}

// definitionCodec is the canonical strict definition schema, compiled once.
//
//nolint:gochecknoglobals // One immutable compiled schema, never reassigned.
var definitionCodec = sync.OnceValues(automations.NewDefinitionCodec)

// manualRunMaximumBodyBytes bounds the optional manual bypass body to 1 KiB.
// Huma rejects any body that reaches the limit with 413 before decoding.
const manualRunMaximumBodyBytes = 1024

// Register installs the eight Automation operations and publishes the strict
// definition schema as a reusable OpenAPI component.
func Register(api huma.API, service Automations) {
	codec, err := definitionCodec()
	if err != nil {
		panic(fmt.Errorf("automation API definition schema: %w", err))
	}
	publishDefinitionSchema(api, codec.AutomationDefinitionSchema())
	publishReplacementSchema(api)
	handler := &Handler{automations: service}
	const tag = "Automations"

	create := operation("create-automation", http.MethodPost, "/automations", "Create an Automation")
	create.DefaultStatus = http.StatusCreated
	create.SkipValidateBody = true
	create.RequestBody = definitionRequestBody()
	create.Errors = []int{
		http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	}
	huma.Register(api, create, handler.CreateAutomation)

	huma.Register(api, operation("list-automations", http.MethodGet, "/automations", "List Automations", tag),
		handler.ListAutomations)
	huma.Register(api, operation(
		"get-automation", http.MethodGet, "/automations/{automation_id}", "Get an Automation", tag,
	), handler.GetAutomation)

	replace := operation("replace-automation", http.MethodPut, "/automations/{automation_id}", "Replace an Automation")
	replace.SkipValidateBody = true
	replace.RequestBody = replacementRequestBody()
	replace.Errors = []int{
		http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
		http.StatusServiceUnavailable, http.StatusInternalServerError,
	}
	huma.Register(api, replace, handler.ReplaceAutomation)

	remove := operation("delete-automation", http.MethodDelete, "/automations/{automation_id}", "Delete an Automation")
	remove.DefaultStatus = http.StatusNoContent
	remove.Errors = []int{
		http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
		http.StatusInternalServerError,
	}
	huma.Register(api, remove, handler.DeleteAutomation)

	start := operation(
		"start-automation-run",
		http.MethodPost,
		"/automations/{automation_id}/runs",
		"Start a manual Automation Run",
	)
	start.DefaultStatus = http.StatusAccepted
	start.RequestBody = manualRunRequestBody()
	start.MaxBodyBytes = manualRunMaximumBodyBytes
	// 409 stays out of Errors so its published schema keeps the optional
	// committed-Skip history reference instead of the generic error model.
	start.Responses["409"] = conditionBlockedProblemResponse(
		"A Run is already active, or committed Conditions blocked manual admission",
	)
	start.Errors = []int{
		http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError,
	}
	huma.Register(api, start, handler.StartAutomationRun)

	huma.Register(api, operation("list-automation-history", http.MethodGet,
		"/automations/{automation_id}/history", "List Automation history"), handler.ListHistory)
	huma.Register(api, operation("get-automation-history-entry", http.MethodGet,
		"/automations/{automation_id}/history/{entry_id}", "Get one Automation history entry"), handler.GetHistoryEntry)
}

func operation(id, method, path, summary string, tags ...string) huma.Operation {
	if len(tags) == 0 {
		tags = []string{"Automations"}
	}
	return huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Tags:        tags,
		Errors:      []int{http.StatusBadRequest, http.StatusInternalServerError},
		Responses: map[string]*huma.Response{
			"409": problemResponse("Revision conflict or a Run is already active"),
			"503": problemResponse("Automation admission is unavailable"),
		},
	}
}

// publishDefinitionSchema preserves every canonical JSON Schema keyword as the
// named component the request bodies reference.
func publishDefinitionSchema(api huma.API, raw json.RawMessage) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		panic(fmt.Errorf("automation API schema publication: %w", err))
	}
	api.OpenAPI().Components.Schemas.Map()["AutomationDefinition"] = &huma.Schema{Extensions: document}
}

func definitionRequestBody() *huma.RequestBody {
	return &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		jsonContentType: {Schema: &huma.Schema{Ref: "#/components/schemas/AutomationDefinition"}},
	}}
}

// manualRunRequestBody documents the optional, non-nullable manual bypass body.
// It stays inline so the only accepted member and the closed
// additionalProperties:false contract are visible on the operation itself. Huma
// keeps validating the request body against this schema.
func manualRunRequestBody() *huma.RequestBody {
	return &huma.RequestBody{Required: false, Content: map[string]*huma.MediaType{
		jsonContentType: {Schema: &huma.Schema{
			Type: huma.TypeObject,
			Properties: map[string]*huma.Schema{
				"bypass_conditions": {Type: huma.TypeBoolean},
			},
			AdditionalProperties: false,
		}},
	}}
}

// publishReplacementSchema documents the required expected_revision/definition
// envelope, not the bare definition accepted by create.
func publishReplacementSchema(api huma.API) {
	minimumRevision := float64(1)
	api.OpenAPI().Components.Schemas.Map()["AutomationReplacement"] = &huma.Schema{
		Type: huma.TypeObject,
		Properties: map[string]*huma.Schema{
			"expected_revision": {Type: huma.TypeInteger, Minimum: &minimumRevision},
			"definition":        {Ref: "#/components/schemas/AutomationDefinition"},
		},
		Required:             []string{"expected_revision", "definition"},
		AdditionalProperties: false,
	}
}

// replacementRequestBody references the replacement envelope component.
func replacementRequestBody() *huma.RequestBody {
	return &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		jsonContentType: {Schema: &huma.Schema{Ref: "#/components/schemas/AutomationReplacement"}},
	}}
}

func (handler *Handler) CreateAutomation(
	ctx context.Context,
	input *CreateAutomationInput,
) (*AutomationOutput, error) {
	definition, err := decodeDefinitionBody(input.Body)
	if err != nil {
		return nil, err
	}
	record, err := handler.automations.CreateAutomation(ctx, definition)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationOutput{
		Location: "/v1/automations/" + string(record.ID),
		Body:     automationBody(record),
	}, nil
}

func (handler *Handler) GetAutomation(
	ctx context.Context,
	input *GetAutomationInput,
) (*AutomationOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	record, err := handler.automations.GetAutomation(ctx, id)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationOutput{Body: automationBody(record)}, nil
}

func (handler *Handler) ListAutomations(
	ctx context.Context,
	input *ListAutomationsInput,
) (*AutomationCollectionOutput, error) {
	params := automations.ListAutomationsParams{Limit: input.Limit}
	if input.Cursor != "" {
		afterID, err := decodeAutomationsCursor(input.Cursor)
		if err != nil {
			return nil, newProblem(http.StatusBadRequest, "invalid_cursor", "automation cursor is invalid")
		}
		params.AfterID = afterID
	}
	page, err := handler.automations.ListAutomations(ctx, params)
	if err != nil {
		return nil, mapDomainError(err)
	}
	output := &AutomationCollectionOutput{}
	output.Body.Items = make([]AutomationBody, len(page.Items))
	for index, record := range page.Items {
		output.Body.Items[index] = automationBody(record)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeAutomationsCursor(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			return nil, huma.Error500InternalServerError("internal error")
		}
		output.Body.NextCursor = &cursor
	}
	return output, nil
}

func (handler *Handler) ReplaceAutomation(
	ctx context.Context,
	input *ReplaceAutomationInput,
) (*AutomationOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	expectedRevision, definition, err := decodeReplaceEnvelope(input.Body)
	if err != nil {
		return nil, err
	}
	record, err := handler.automations.ReplaceAutomation(ctx, id, expectedRevision, definition)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationOutput{Body: automationBody(record)}, nil
}

func (handler *Handler) DeleteAutomation(
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

func (handler *Handler) StartAutomationRun(
	ctx context.Context,
	input *StartAutomationRunInput,
) (*AutomationRunOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	run, err := handler.automations.StartManualRun(ctx, automations.ManualRunInput{
		AutomationID:     id,
		BypassConditions: input.Body != nil && input.Body.BypassConditions,
	})
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationRunOutput{
		Location: "/v1/automations/" + string(run.AutomationID) + "/history/" + string(run.ID),
		Body:     automationRunBody(run),
	}, nil
}

func (handler *Handler) ListHistory(
	ctx context.Context,
	input *ListHistoryInput,
) (*AutomationHistoryCollectionOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	params := automations.ListHistoryParams{AutomationID: id, Limit: input.Limit}
	if input.Cursor != "" {
		recordedAt, entryID, cursorErr := decodeHistoryCursor(input.Cursor, id)
		if cursorErr != nil {
			return nil, newProblem(http.StatusBadRequest, "invalid_cursor", "history cursor is invalid")
		}
		params.BeforeRecordedAt = recordedAt
		params.BeforeID = entryID
	}
	page, err := handler.automations.ListHistory(ctx, params)
	if err != nil {
		return nil, mapDomainError(err)
	}
	output := &AutomationHistoryCollectionOutput{}
	output.Body.Items = make([]AutomationHistorySummaryBody, len(page.Items))
	for index, summary := range page.Items {
		output.Body.Items[index] = historySummaryBody(summary)
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor, cursorErr := encodeHistoryCursor(id, last.RecordedAt, last.ID)
		if cursorErr != nil {
			return nil, huma.Error500InternalServerError("internal error")
		}
		output.Body.NextCursor = &cursor
	}
	return output, nil
}

func (handler *Handler) GetHistoryEntry(
	ctx context.Context,
	input *GetHistoryEntryInput,
) (*AutomationHistoryEntryOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	if !validEntryID(input.EntryID) {
		return nil, newProblem(http.StatusBadRequest, "invalid_entry_id", "entry_id is not a canonical history ID")
	}
	entry, err := handler.automations.GetHistoryEntry(ctx, id, input.EntryID)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationHistoryEntryOutput{Body: historyEntryBody(entry)}, nil
}

// decodeDefinitionBody strictly decodes one definition payload.
func decodeDefinitionBody(raw json.RawMessage) (automations.Definition, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_definition", "definition body is required",
		)
	}
	definition, err := automations.DecodeDefinition(raw)
	if err != nil {
		return automations.Definition{}, mapDomainError(err)
	}
	return definition, nil
}

// decodeReplaceEnvelope strictly decodes the expected-revision envelope and its
// nested strict definition.
func decodeReplaceEnvelope(
	raw json.RawMessage,
) (int64, automations.Definition, error) {
	var envelope struct {
		ExpectedRevision int64           `json:"expected_revision"`
		Definition       json.RawMessage `json:"definition"`
	}
	if err := decodeSingleJSONValue(raw, &envelope); err != nil {
		return 0, automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_request_body", "replacement body is invalid",
		)
	}
	if envelope.ExpectedRevision < 1 {
		return 0, automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_revision", "expected_revision must be at least 1",
		)
	}
	definition, err := decodeDefinitionBody(envelope.Definition)
	if err != nil {
		return 0, automations.Definition{}, err
	}
	return envelope.ExpectedRevision, definition, nil
}

// decodeSingleJSONValue rejects unknown envelope fields and trailing content.
func decodeSingleJSONValue(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body contains trailing JSON")
	}
	return nil
}

func validEntryID(value string) bool {
	if _, err := automations.ParseRunID(value); err == nil {
		return true
	}
	if _, err := automations.ParseSkipID(value); err == nil {
		return true
	}
	return false
}
