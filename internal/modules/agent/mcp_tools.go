package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// MCPTools exposes every tool on the MCP server as an Eino tool, dialing it
// over an in-memory transport: one catalog, no sockets, no self-address
// configuration, and the full protocol path (argument decoding, input
// validation, error mapping) still runs per call.
//
// The returned tools live as long as ctx: cancelling it closes the client and
// server sessions. Pass the application's run context, not a request context.
func MCPTools(ctx context.Context, server *mcpapi.Server) ([]tool.BaseTool, error) {
	if server == nil {
		return nil, errors.New("agent MCP server is required")
	}
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Raw().Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("agent MCP server session: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "hearth-agent"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		return nil, fmt.Errorf("agent MCP client session: %w", err)
	}
	go func() {
		<-ctx.Done()
		_ = clientSession.Close()
		_ = serverSession.Close()
	}()
	listed, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("agent list MCP tools: %w", err)
	}
	bridged := make([]tool.BaseTool, 0, len(listed.Tools))
	for _, advertised := range listed.Tools {
		converted, convertErr := mcpTool(advertised, clientSession)
		if convertErr != nil {
			return nil, convertErr
		}
		bridged = append(bridged, converted)
	}
	return bridged, nil
}

// mcpTool converts one advertised MCP tool into an Eino tool. The advertised
// input schema becomes the model's parameter schema, so the model sees what
// any other MCP client sees; the server re-validates arguments per call.
func mcpTool(advertised *mcp.Tool, session *mcp.ClientSession) (tool.BaseTool, error) {
	params, err := mcpParams(advertised)
	if err != nil {
		return nil, err
	}
	name := advertised.Name
	invoke := traced(name, func(ctx context.Context, argsJSON string) (string, error) {
		return callMCPTool(ctx, session, name, argsJSON)
	})
	return &mcpBridgeTool{
		info: &schema.ToolInfo{
			Name:        name,
			Desc:        advertised.Description,
			ParamsOneOf: params,
		},
		invoke: invoke,
	}, nil
}

// mcpParams decodes one advertised input schema into Eino parameters. A
// missing schema fails the bridge at startup rather than admitting schemaless
// calls.
func mcpParams(advertised *mcp.Tool) (*schema.ParamsOneOf, error) {
	if advertised.InputSchema == nil {
		return nil, fmt.Errorf("agent MCP tool %s: missing input schema", advertised.Name)
	}
	raw, err := json.Marshal(advertised.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("agent MCP tool %s: input schema: %w", advertised.Name, err)
	}
	var converted einojsonschema.Schema
	if unmarshalErr := json.Unmarshal(raw, &converted); unmarshalErr != nil {
		return nil, fmt.Errorf("agent MCP tool %s: input schema: %w", advertised.Name, unmarshalErr)
	}
	return schema.NewParamsOneOfByJSONSchema(&converted), nil
}

// mcpBridgeTool is one MCP tool callable from the ReAct loop.
type mcpBridgeTool struct {
	info   *schema.ToolInfo
	invoke utils.InvokeFunc[string, string]
}

// Info returns the model-facing tool identity and parameter schema.
func (native *mcpBridgeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return native.info, nil
}

// InvokableRun executes one model tool call against the MCP server.
func (native *mcpBridgeTool) InvokableRun(
	ctx context.Context,
	argumentsInJSON string,
	_ ...tool.Option,
) (string, error) {
	return native.invoke(ctx, argumentsInJSON)
}

// callMCPTool runs one tool call and renders its result as model text. A
// modeled domain failure stays a successful tool output so the ReAct loop hands
// it back to the model as another step to explain or recover from; a transport
// error, unparseable arguments, or an unmodelled isError result still ends the
// tool with a Go error.
func callMCPTool(
	ctx context.Context,
	session *mcp.ClientSession,
	name, argsJSON string,
) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("agent tool %s: invalid arguments: %w", name, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, err)
	}
	text := mcpResultText(result)
	// The wrapper publishes the structured half only for a domain ToolError, so
	// it marks the modeled failure the model can act on; an input rejection or
	// unmodelled server fault keeps none and stays a failed turn.
	if result.IsError && result.StructuredContent == nil {
		return "", errors.New(text)
	}
	return text, nil
}

// mcpResultText renders one result as model text: the concatenated text parts,
// plus the structured payload of a modeled failure. The SDK renders a success
// output as both halves, but a failure's structured fields exist only in the
// structured half, so the model and the persisted trace need them appended to
// read the failure code, a Command's durable status and Command ID, or an
// Automation failure's history coordinates.
func mcpResultText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if result.StructuredContent == nil {
		return text
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return text
	}
	switch {
	case text == "":
		return string(raw)
	case result.IsError:
		return text + "\n" + string(raw)
	default:
		return text
	}
}

// traced records one tool execution on the turn assembler carried by ctx. The
// ReAct loop exposes no per-tool stream, so without this the trace would never
// reach the persisted history or the event stream. Outside a turn the
// assembler is absent and the tool simply executes.
//
// The bridged tool receives its arguments as the raw JSON string the model
// sent, so they are recorded verbatim: marshalling that string again would
// persist an escaped JSON string literal instead of the argument object.
func traced(name string, fn utils.InvokeFunc[string, string]) utils.InvokeFunc[string, string] {
	return func(ctx context.Context, argsJSON string) (string, error) {
		assembler := assemblerFrom(ctx)
		if assembler == nil {
			return fn(ctx, argsJSON)
		}
		assembler.emitEvent(TurnEvent{Type: EventToolStarted, Name: name, Arguments: argsJSON})
		output, err := fn(ctx, argsJSON)
		if traceErr := assembler.recordToolCall(name, argsJSON, output, err); traceErr != nil {
			return "", traceErr
		}
		return output, err
	}
}
