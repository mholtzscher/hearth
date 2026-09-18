package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// MCP tool names mirror the Huma operation IDs in snake_case; the Hearth
// domain names (Automation, Run, Skip) stay parameters and payload members.
const (
	mcpCreateAutomationTool          = "create_automation"
	mcpListAutomationsTool           = "list_automations"
	mcpGetAutomationTool             = "get_automation"
	mcpReplaceAutomationTool         = "replace_automation"
	mcpDeleteAutomationTool          = "delete_automation"
	mcpStartAutomationRunTool        = "start_automation_run"
	mcpListAutomationHistoryTool     = "list_automation_history"
	mcpGetAutomationHistoryEntryTool = "get_automation_history_entry"
)

// mcpPageDefaultLimit mirrors the Huma default page size for omitted limits.
const mcpPageDefaultLimit = 50

// mcpPageMaximumLimit mirrors the Huma maximum page size for explicit limits.
const mcpPageMaximumLimit = 200

// mcpInternalFailureCode is the stable code for an unpublished internal failure.
const mcpInternalFailureCode = "internal_error"

// createAutomationToolInput carries one strict Automation definition document.
//
// Definition is declared as a JSON object so the SDK can decode the argument,
// but the tool advertises the canonical Automation definition schema in place of
// the derived property schema, so the SDK validates the document against the
// same strict schema the Huma route publishes and rejects a schema-invalid
// document before the handler runs. The decoded value itself is unused: the
// handler reads the exact document bytes from the raw tool request, because the
// SDK's argument pipeline rounds every number through float64.
type createAutomationToolInput struct {
	Definition map[string]any `json:"definition" jsonschema:"The strict Automation definition document"`
}

// getAutomationToolInput addresses one current Automation definition.
type getAutomationToolInput struct {
	AutomationID mcpAutomationID `json:"automation_id" jsonschema:"Canonical Hearth Automation ID"`
}

// listAutomationsToolInput pages current Automation definitions by opaque cursor.
type listAutomationsToolInput struct {
	Limit  mcpPageLimit `json:"limit,omitempty"  jsonschema:"Page size, 1 to 200; omitted means 50"`
	Cursor string       `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from a previous page"`
}

// replaceAutomationToolInput replaces one definition at a required current revision.
type replaceAutomationToolInput struct {
	AutomationID     mcpAutomationID     `json:"automation_id"     jsonschema:"Canonical Hearth Automation ID"`
	ExpectedRevision mcpExpectedRevision `json:"expected_revision" jsonschema:"Current revision the replacement expects"`
	Definition       map[string]any      `json:"definition"        jsonschema:"The replacement strict Automation definition document"`
}

// deleteAutomationToolInput hard-deletes one definition under optimistic revision concurrency.
type deleteAutomationToolInput struct {
	AutomationID     mcpAutomationID     `json:"automation_id"     jsonschema:"Canonical Hearth Automation ID"`
	ExpectedRevision mcpExpectedRevision `json:"expected_revision" jsonschema:"Current revision the deletion expects"`
}

// deleteAutomationToolOutput confirms one hard deletion.
type deleteAutomationToolOutput struct {
	AutomationID string `json:"automation_id"`
	Revision     int64  `json:"revision"`
	Deleted      bool   `json:"deleted"`
}

// startAutomationRunToolInput admits one manual Run, optionally bypassing Conditions.
type startAutomationRunToolInput struct {
	AutomationID     mcpAutomationID `json:"automation_id"               jsonschema:"Canonical Hearth Automation ID"`
	BypassConditions bool            `json:"bypass_conditions,omitempty" jsonschema:"Admit without evaluating configured Conditions"`
}

// listAutomationHistoryToolInput pages newest-first history for one Automation.
type listAutomationHistoryToolInput struct {
	AutomationID mcpAutomationID `json:"automation_id"    jsonschema:"Canonical Hearth Automation ID"`
	Limit        mcpPageLimit    `json:"limit,omitempty"  jsonschema:"Page size, 1 to 200; omitted means 50"`
	Cursor       string          `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from a previous history page"`
}

// getAutomationHistoryEntryToolInput addresses one retained Run or Skip.
type getAutomationHistoryEntryToolInput struct {
	AutomationID mcpAutomationID `json:"automation_id" jsonschema:"Canonical Hearth Automation ID"`
	EntryID      mcpEntryID      `json:"entry_id"      jsonschema:"Canonical Hearth Automation Run or Skip ID"`
}

// RegisterMCP installs the eight Automation tools and the automation resource
// templates described by the MCP spec on server.
//
// Tools call the same [Automations] service methods as the Huma operations, so
// the MCP surface stays a thin transport over the module. create_automation and
// replace_automation advertise the canonical Automation definition schema for
// their definition argument, so tools/list exposes the Trigger, Condition, and
// Step constraints and the SDK rejects a schema-invalid document before the
// handler runs. Every tool publishes a derived typed output schema; the MCP
// output DTOs in mcp_outputs.go describe the bodies these tools return without
// the open raw-JSON fields whose derived schema would reject them.
func RegisterMCP(server *mcpapi.Server, service Automations) {
	handler := &Handler{automations: service}
	handler.registerTools(server)
	handler.registerResources(server)
}

// registerTools adds every Automation tool described by the MCP spec.
func (handler *Handler) registerTools(server *mcpapi.Server) {
	// create_automation and replace_automation need the raw request so the
	// definition document stays byte-exact, and they advertise the canonical
	// definition schema so a schema-invalid document is rejected before the
	// handler runs. Every other tool needs only its decoded arguments and uses
	// the schema derived from its input struct.
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[createAutomationToolInput, mcpAutomationBody]{
		Name:        mcpCreateAutomationTool,
		Description: "Create an Automation",
		InputSchema: mcpAutomationDefinitionInputSchema[createAutomationToolInput](),
		Handler:     handler.createAutomation,
	})
	mcpapi.Register(server, mcpapi.Tool[listAutomationsToolInput, mcpAutomationCollectionBody]{
		Name:        mcpListAutomationsTool,
		Description: "List Automations",
		Handler:     handler.listAutomations,
	})
	mcpapi.Register(server, mcpapi.Tool[getAutomationToolInput, mcpAutomationBody]{
		Name:        mcpGetAutomationTool,
		Description: "Get an Automation",
		Handler:     handler.getAutomation,
	})
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[replaceAutomationToolInput, mcpAutomationBody]{
		Name:        mcpReplaceAutomationTool,
		Description: "Replace an Automation",
		InputSchema: mcpAutomationDefinitionInputSchema[replaceAutomationToolInput](),
		Handler:     handler.replaceAutomation,
	})
	mcpapi.Register(server, mcpapi.Tool[deleteAutomationToolInput, deleteAutomationToolOutput]{
		Name:        mcpDeleteAutomationTool,
		Description: "Delete an Automation",
		Handler:     handler.deleteAutomation,
	})
	mcpapi.Register(server, mcpapi.Tool[startAutomationRunToolInput, mcpAutomationRunBody]{
		Name:        mcpStartAutomationRunTool,
		Description: "Start a manual Automation Run",
		Handler:     handler.startAutomationRun,
	})
	mcpapi.Register(server, mcpapi.Tool[listAutomationHistoryToolInput, mcpAutomationHistoryCollectionBody]{
		Name:        mcpListAutomationHistoryTool,
		Description: "List Automation history",
		Handler:     handler.listAutomationHistory,
	})
	mcpapi.Register(server, mcpapi.Tool[getAutomationHistoryEntryToolInput, mcpAutomationHistoryEntryBody]{
		Name:        mcpGetAutomationHistoryEntryTool,
		Description: "Get one Automation history entry",
		Handler:     handler.getAutomationHistoryEntry,
	})
}

// createAutomation decodes the exact definition document and persists it.
//
// The decoded argument struct is unused: the SDK validates it against the
// published schema, but the document itself is decoded from the raw request so
// no number is rounded before the canonical strict decoder sees it.
func (handler *Handler) createAutomation(
	ctx context.Context,
	request *mcp.CallToolRequest,
	_ createAutomationToolInput,
) (mcpAutomationBody, error) {
	definition, err := mcpDefinitionArgument(request.Params.Arguments)
	if err != nil {
		return mcpAutomationBody{}, mcpError(err)
	}
	record, err := handler.automations.CreateAutomation(ctx, definition)
	if err != nil {
		return mcpAutomationBody{}, mcpError(err)
	}
	return mcpAutomationOutput(automationBody(record)), nil
}

// getAutomation reads one current definition.
func (handler *Handler) getAutomation(
	ctx context.Context,
	input getAutomationToolInput,
) (mcpAutomationBody, error) {
	record, err := handler.automations.GetAutomation(ctx, automations.AutomationID(input.AutomationID))
	if err != nil {
		return mcpAutomationBody{}, mcpError(err)
	}
	return mcpAutomationOutput(automationBody(record)), nil
}

// listAutomations pages current definitions by opaque cursor.
func (handler *Handler) listAutomations(
	ctx context.Context,
	input listAutomationsToolInput,
) (mcpAutomationCollectionBody, error) {
	params := automations.ListAutomationsParams{Limit: input.Limit.pageSize()}
	if input.Cursor != "" {
		afterID, cursorErr := decodeAutomationsCursor(input.Cursor)
		if cursorErr != nil {
			return mcpAutomationCollectionBody{}, mcpError(newProblem(
				http.StatusBadRequest, "invalid_cursor", "automation cursor is invalid",
			))
		}
		params.AfterID = afterID
	}
	page, err := handler.automations.ListAutomations(ctx, params)
	if err != nil {
		return mcpAutomationCollectionBody{}, mcpError(err)
	}
	body := AutomationCollectionBody{Items: make([]AutomationBody, len(page.Items))}
	for index, record := range page.Items {
		body.Items[index] = automationBody(record)
	}
	if page.HasMore && len(page.Items) > 0 {
		cursor, cursorErr := encodeAutomationsCursor(page.Items[len(page.Items)-1].ID)
		if cursorErr != nil {
			return mcpAutomationCollectionBody{}, mcpError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return mcpCollectionOutput(body), nil
}

// replaceAutomation decodes the exact replacement definition and swaps it in.
func (handler *Handler) replaceAutomation(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input replaceAutomationToolInput,
) (mcpAutomationBody, error) {
	definition, err := mcpDefinitionArgument(request.Params.Arguments)
	if err != nil {
		return mcpAutomationBody{}, mcpError(err)
	}
	record, err := handler.automations.ReplaceAutomation(
		ctx,
		automations.AutomationID(input.AutomationID),
		int64(input.ExpectedRevision),
		definition,
	)
	if err != nil {
		return mcpAutomationBody{}, mcpError(err)
	}
	return mcpAutomationOutput(automationBody(record)), nil
}

// deleteAutomation hard-deletes one definition under the expected revision.
func (handler *Handler) deleteAutomation(
	ctx context.Context,
	input deleteAutomationToolInput,
) (deleteAutomationToolOutput, error) {
	id := automations.AutomationID(input.AutomationID)
	if err := handler.automations.DeleteAutomation(ctx, id, int64(input.ExpectedRevision)); err != nil {
		return deleteAutomationToolOutput{}, mcpError(err)
	}
	return deleteAutomationToolOutput{
		AutomationID: string(id),
		Revision:     int64(input.ExpectedRevision),
		Deleted:      true,
	}, nil
}

// startAutomationRun admits one manual Run from the current definition snapshot.
func (handler *Handler) startAutomationRun(
	ctx context.Context,
	input startAutomationRunToolInput,
) (mcpAutomationRunBody, error) {
	run, err := handler.automations.StartManualRun(ctx, automations.ManualRunInput{
		AutomationID:     automations.AutomationID(input.AutomationID),
		BypassConditions: input.BypassConditions,
	})
	if err != nil {
		return mcpAutomationRunBody{}, mcpError(err)
	}
	return mcpRunOutput(automationRunBody(run)), nil
}

// listAutomationHistory pages newest-first retained Run and Skip summaries.
func (handler *Handler) listAutomationHistory(
	ctx context.Context,
	input listAutomationHistoryToolInput,
) (mcpAutomationHistoryCollectionBody, error) {
	id := automations.AutomationID(input.AutomationID)
	params := automations.ListHistoryParams{AutomationID: id, Limit: input.Limit.pageSize()}
	if input.Cursor != "" {
		recordedAt, entryID, cursorErr := decodeHistoryCursor(input.Cursor, id)
		if cursorErr != nil {
			return mcpAutomationHistoryCollectionBody{}, mcpError(newProblem(
				http.StatusBadRequest, "invalid_cursor", "history cursor is invalid",
			))
		}
		params.BeforeRecordedAt = recordedAt
		params.BeforeID = entryID
	}
	page, err := handler.automations.ListHistory(ctx, params)
	if err != nil {
		return mcpAutomationHistoryCollectionBody{}, mcpError(err)
	}
	body := AutomationHistoryCollectionBody{
		Items: make([]AutomationHistorySummaryBody, len(page.Items)),
	}
	for index, summary := range page.Items {
		body.Items[index] = historySummaryBody(summary)
	}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor, cursorErr := encodeHistoryCursor(id, last.RecordedAt, last.ID)
		if cursorErr != nil {
			return mcpAutomationHistoryCollectionBody{}, mcpError(cursorErr)
		}
		body.NextCursor = &cursor
	}
	return mcpHistoryCollectionOutput(body), nil
}

// getAutomationHistoryEntry reads exactly one retained Run or Skip.
func (handler *Handler) getAutomationHistoryEntry(
	ctx context.Context,
	input getAutomationHistoryEntryToolInput,
) (mcpAutomationHistoryEntryBody, error) {
	entry, err := handler.automations.GetHistoryEntry(
		ctx,
		automations.AutomationID(input.AutomationID),
		string(input.EntryID),
	)
	if err != nil {
		return mcpAutomationHistoryEntryBody{}, mcpError(err)
	}
	return mcpHistoryEntryOutput(historyEntryBody(entry)), nil
}

// mcpError maps one module or Huma error to an idiomatic MCP tool failure that
// preserves the stable problem code.
//
// The failure crosses as a [mcpapi.ToolError], so the SDK renders it as an
// isError tool result carrying "<code>: <message>" and structured content with
// the stable failure code, human-readable message, and optional details. Any
// error the module does not classify stays a generic internal failure, so no
// unpublished server detail reaches the client; the original error is retained
// with [mcpapi.ToolError.WithCause] instead, so the tool middleware logs the
// full cause for server diagnostics while the client sees only the generic
// message.
func mcpError(err error) error {
	var problem *automationProblemError
	if !errors.As(err, &problem) {
		mapped := mapDomainError(err)
		if !errors.As(mapped, &problem) {
			return mcpInternalFailureCause(err)
		}
	}
	return &mcpapi.ToolError{
		Code:    problem.Code,
		Message: problem.Detail,
		Details: problemDetails(problem),
	}
}

// mcpInternalFailureCause is the 500-class result that also retains cause for
// the server-side diagnostic log.
//
// The client still sees only the generic message; mcpapi.ToolError.WithCause
// keeps cause off Error and structured content, so the middleware can log it
// without widening what the agent receives.
func mcpInternalFailureCause(cause error) *mcpapi.ToolError {
	return (&mcpapi.ToolError{Code: mcpInternalFailureCode, Message: "internal error"}).WithCause(cause)
}

// problemDetails collects the optional structured problem fields, if any.
func problemDetails(problem *automationProblemError) map[string]any {
	if problem.HistoryID == "" && problem.HistoryURL == "" {
		return nil
	}
	details := make(map[string]any)
	if problem.HistoryID != "" {
		details["history_id"] = problem.HistoryID
	}
	if problem.HistoryURL != "" {
		details["history_url"] = problem.HistoryURL
	}
	return details
}
