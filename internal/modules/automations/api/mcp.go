package api

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// MCP tool names mirror the Huma operation IDs in snake_case; the Hearth domain
// names (Automation, Run, Skip) stay parameters and payload members.
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

// createAutomationToolInput carries one strict Automation definition document.
// Definition is declared as a JSON object so the SDK can decode the argument, but
// the tool advertises the canonical Automation definition schema in place of the
// derived property schema, so the SDK validates the document against the same
// strict schema the Huma route publishes. The decoded value itself is unused: the
// handler reads the exact document bytes from the raw tool request, because the
// SDK's argument pipeline rounds every number through float64.
type createAutomationToolInput struct {
	Definition map[string]any `json:"definition" jsonschema:"The strict Automation definition document"`
}

type getAutomationToolInput struct {
	AutomationID mcpAutomationID `json:"automation_id" jsonschema:"Canonical Hearth Automation ID"`
}

type listAutomationsToolInput struct {
	Limit  mcpPageLimit `json:"limit,omitempty"  jsonschema:"Page size, 1 to 200; omitted means 50"`
	Cursor string       `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from a previous page"`
}

type replaceAutomationToolInput struct {
	AutomationID     mcpAutomationID     `json:"automation_id"     jsonschema:"Canonical Hearth Automation ID"`
	ExpectedRevision mcpExpectedRevision `json:"expected_revision" jsonschema:"Current revision the replacement expects"`
	Definition       map[string]any      `json:"definition"        jsonschema:"The replacement strict Automation definition document"`
}

type deleteAutomationToolInput struct {
	AutomationID     mcpAutomationID     `json:"automation_id"     jsonschema:"Canonical Hearth Automation ID"`
	ExpectedRevision mcpExpectedRevision `json:"expected_revision" jsonschema:"Current revision the deletion expects"`
}

// deleteAutomationToolOutput confirms one hard deletion. The revision member is
// named deleted_revision so it reads as the revision that was deleted rather
// than a revision the deletion produced.
type deleteAutomationToolOutput struct {
	AutomationID    string `json:"automation_id"`
	DeletedRevision int64  `json:"deleted_revision"`
	Deleted         bool   `json:"deleted"`
}

type startAutomationRunToolInput struct {
	AutomationID     mcpAutomationID `json:"automation_id"               jsonschema:"Canonical Hearth Automation ID"`
	BypassConditions bool            `json:"bypass_conditions,omitempty" jsonschema:"Admit without evaluating configured Conditions"`
}

type listAutomationHistoryToolInput struct {
	AutomationID mcpAutomationID `json:"automation_id"    jsonschema:"Canonical Hearth Automation ID"`
	Limit        mcpPageLimit    `json:"limit,omitempty"  jsonschema:"Page size, 1 to 200; omitted means 50"`
	Cursor       string          `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from a previous history page"`
}

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

	// These tools use the raw request to preserve the definition's exact numeric
	// values while advertising its canonical schema.
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[createAutomationToolInput, mcpAutomationBody]{
		Name:        mcpCreateAutomationTool,
		Description: "Create an Automation. Observation comparisons address the Observation value directly: use value_pointer \"\" for scalar values and never /state/value.",
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
		Description: "Replace an Automation. Observation comparisons address the Observation value directly: use value_pointer \"\" for scalar values and never /state/value.",
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

	handler.registerResources(server)
}

// createAutomation decodes the exact definition document and persists it. The
// decoded argument struct is unused: the SDK validates it against the published
// schema, but the document itself is decoded from the raw request so no number
// is rounded before the canonical strict decoder sees it.
func (handler *Handler) createAutomation(
	ctx context.Context,
	request *mcp.CallToolRequest,
	_ createAutomationToolInput,
) (mcpAutomationBody, error) {
	definition, err := mcpDefinitionArgument(request.Params.Arguments)
	if err != nil {
		return mcpAutomationBody{}, mcpAutomationFailure(err, mcpFailureNone)
	}
	record, err := handler.automations.CreateAutomation(ctx, definition)
	if err != nil {
		return mcpAutomationBody{}, mcpAutomationFailure(err, mcpFailureNone)
	}
	return mcpAutomationOutput(automationBody(record)), nil
}

func (handler *Handler) getAutomation(
	ctx context.Context,
	input getAutomationToolInput,
) (mcpAutomationBody, error) {
	record, err := handler.automations.GetAutomation(ctx, automations.AutomationID(input.AutomationID))
	if err != nil {
		return mcpAutomationBody{}, mcpAutomationFailure(err, mcpFailureAutomationNotFound)
	}
	return mcpAutomationOutput(automationBody(record)), nil
}

// listAutomations pages current definitions through the Huma read the route
// serves, so the page default, cursor decode, body mapping, and next-cursor
// encoding stay in one place and the tool cannot drift from REST. Only the
// output conversion differs: the SDK derives a tool's output schema by
// reflection, so the raw-JSON leaves are retyped (see mcp_outputs.go).
func (handler *Handler) listAutomations(
	ctx context.Context,
	input listAutomationsToolInput,
) (mcpAutomationCollectionBody, error) {
	output, err := handler.ListAutomations(ctx, &ListAutomationsInput{
		Limit: input.Limit.pageSize(), Cursor: input.Cursor,
	})
	if err != nil {
		return mcpAutomationCollectionBody{}, mcpAutomationFailure(err, mcpFailureNone)
	}
	return mcpCollectionOutput(output.Body), nil
}

func (handler *Handler) replaceAutomation(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input replaceAutomationToolInput,
) (mcpAutomationBody, error) {
	definition, err := mcpDefinitionArgument(request.Params.Arguments)
	if err != nil {
		return mcpAutomationBody{}, mcpAutomationFailure(err, mcpFailureNone)
	}
	record, err := handler.automations.ReplaceAutomation(
		ctx,
		automations.AutomationID(input.AutomationID),
		int64(input.ExpectedRevision),
		definition,
	)
	if err != nil {
		return mcpAutomationBody{}, mcpAutomationFailure(err, mcpFailureAutomationNotFound)
	}
	return mcpAutomationOutput(automationBody(record)), nil
}

func (handler *Handler) deleteAutomation(
	ctx context.Context,
	input deleteAutomationToolInput,
) (deleteAutomationToolOutput, error) {
	id := automations.AutomationID(input.AutomationID)
	if err := handler.automations.DeleteAutomation(ctx, id, int64(input.ExpectedRevision)); err != nil {
		return deleteAutomationToolOutput{}, mcpAutomationFailure(err, mcpFailureAutomationNotFound)
	}
	return deleteAutomationToolOutput{
		AutomationID:    string(id),
		DeletedRevision: int64(input.ExpectedRevision),
		Deleted:         true,
	}, nil
}

func (handler *Handler) startAutomationRun(
	ctx context.Context,
	input startAutomationRunToolInput,
) (mcpAutomationRunBody, error) {
	run, err := handler.automations.StartManualRun(ctx, automations.ManualRunInput{
		AutomationID:     automations.AutomationID(input.AutomationID),
		BypassConditions: input.BypassConditions,
	})
	if err != nil {
		return mcpAutomationRunBody{}, mcpAutomationFailure(err, mcpFailureAutomationNotFound)
	}
	return mcpRunOutput(automationRunBody(run)), nil
}

// listAutomationHistory pages retained history through the Huma read the route
// serves, so the page default, cursor decode, body mapping, and next-cursor
// encoding stay in one place and the tool cannot drift from REST.
func (handler *Handler) listAutomationHistory(
	ctx context.Context,
	input listAutomationHistoryToolInput,
) (mcpAutomationHistoryCollectionBody, error) {
	output, err := handler.ListHistory(ctx, &ListHistoryInput{
		AutomationID: string(input.AutomationID),
		Limit:        input.Limit.pageSize(),
		Cursor:       input.Cursor,
	})
	if err != nil {
		return mcpAutomationHistoryCollectionBody{}, mcpAutomationFailure(err, mcpFailureNone)
	}
	return mcpHistoryCollectionOutput(output.Body), nil
}

// getAutomationHistoryEntry reads exactly one retained Run or Skip. The domain
// reports one sentinel for a missing entry and for a missing or mismatched
// Automation, so the tool publishes the entry code for both rather than
// pretending it can tell them apart.
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
		return mcpAutomationHistoryEntryBody{}, mcpAutomationFailure(err, mcpFailureHistoryEntryNotFound)
	}
	return mcpHistoryEntryOutput(historyEntryBody(entry)), nil
}
