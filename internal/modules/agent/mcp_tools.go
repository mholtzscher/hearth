package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// MCPTools exposes the household tool catalog as Eino tools, excluding direct
// Automation definition writes, while dialing the MCP server
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
		if advertised.Name == "create_automation" || advertised.Name == "replace_automation" {
			continue
		}
		converted, convertErr := mcpTool(advertised, clientSession)
		if convertErr != nil {
			return nil, convertErr
		}
		bridged = append(bridged, converted)
	}
	return bridged, nil
}

// mcpTool converts one advertised MCP tool into an Eino tool. The advertised
// input schema becomes the model's parameter schema, with an agent-only
// completion flag on presentation tools. The server re-validates MCP arguments.
func mcpTool(advertised *mcp.Tool, session *mcp.ClientSession) (tool.BaseTool, error) {
	params, err := mcpParams(advertised)
	if err != nil {
		return nil, err
	}
	name := advertised.Name
	invoke := func(ctx context.Context, argsJSON string) (string, error) {
		output, callErr := callMCPTool(ctx, session, name, argsJSON)
		if callErr != nil {
			return "", toolCallError{err: callErr}
		}
		return output, nil
	}
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
	if isPresentationTool(advertised.Name) {
		raw, err = presentationCompletionSchema(raw)
		if err != nil {
			return nil, err
		}
	}
	if unmarshalErr := json.Unmarshal(raw, &converted); unmarshalErr != nil {
		return nil, fmt.Errorf("agent MCP tool %s: input schema: %w", advertised.Name, unmarshalErr)
	}
	return schema.NewParamsOneOfByJSONSchema(&converted), nil
}

// mcpBridgeTool is one MCP tool callable by the agent.
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
	status, _ := ctx.Value(toolResultStatusKey{}).(*toolResultStatus)
	if status != nil {
		// Local argument failures also need model recovery, just like MCP errors.
		status.modeledFailure = true
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, err)
	}
	var decoded json.RawMessage
	decoder := json.NewDecoder(bytes.NewBufferString(argsJSON))
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Sprintf("agent tool %s: invalid arguments: %v", name, err), nil
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Sprintf("agent tool %s: invalid arguments: expected one JSON object", name), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &object); err != nil {
		return fmt.Sprintf("agent tool %s: invalid arguments: expected a JSON object: %v", name, err), nil
	}
	if object == nil {
		return fmt.Sprintf("agent tool %s: invalid arguments: expected a JSON object", name), nil
	}
	complete := false
	if value, exists := object[completeTurnArgument]; exists && isPresentationTool(name) {
		flag := string(bytes.TrimSpace(value))
		if flag != "true" && flag != "false" {
			return "invalid arguments: complete_turn must be a boolean", nil
		}
		complete = flag == "true"
		delete(object, completeTurnArgument)
		var raw bytes.Buffer
		encoder := json.NewEncoder(&raw)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(object); err != nil {
			return "", err
		}
		argsJSON = raw.String()
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(argsJSON)})
	if contextErr := ctx.Err(); contextErr != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, contextErr)
	}
	if err != nil {
		return "", fmt.Errorf("agent tool %s: %w", name, err)
	}
	if status != nil {
		status.modeledFailure = result.IsError
		status.completeTurn = complete && !result.IsError
	}
	return mcpResultText(result), nil
}

// mcpResultText preserves the server's exact JSON text for successes and modeled
// failures. Structured content is only a fallback for a result with no text.
func mcpResultText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if text != "" || result.StructuredContent == nil {
		return text
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return text
	}
	return string(raw)
}
