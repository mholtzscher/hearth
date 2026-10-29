package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
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
		output, callErr := callMCPTool(ctx, session, name, argsJSON)
		if callErr != nil {
			return "", toolCallError{err: callErr}
		}
		return output, nil
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

func callMCPTool(
	ctx context.Context,
	session *mcp.ClientSession,
	name, argsJSON string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, err)
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewBufferString(argsJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Sprintf("agent tool %s: invalid arguments: %v", name, err), nil
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Sprintf("agent tool %s: invalid arguments: expected one JSON object", name), nil
	}
	_, ok := decoded.(map[string]any)
	if !ok {
		return fmt.Sprintf("agent tool %s: invalid arguments: expected a JSON object", name), nil
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(argsJSON)})
	if contextErr := ctx.Err(); contextErr != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, contextErr)
	}
	if err != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, err)
	}
	return mcpResultText(result), nil
}

// mcpResultText renders one result as model text: the concatenated text parts,
// plus a modeled failure's structured payload when the text does not already
// contain it. Tools opting into exact failure text publish both halves as JSON;
// ordinary failures need structured fields appended so the model and trace can read the
// failure code, a Command's durable identity, or Automation history coordinates.
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
		// Tools may opt into publishing their complete failure as JSON text.
		// Compare using the client's decoded representation only to recognize
		// duplication; return the untouched text, never the rounded copy.
		var content any
		if json.Unmarshal([]byte(text), &content) == nil && reflect.DeepEqual(content, result.StructuredContent) {
			return text
		}
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
			return "", tracePersistError{err: traceErr}
		}
		return output, err
	}
}
