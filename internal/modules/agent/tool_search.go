package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/components/tool"
)

// agentToolSearch keeps common inventory reads visible and makes the remaining
// bridged MCP tools discoverable through Eino's client-side search.
func agentToolSearch(
	ctx context.Context,
	tools []tool.BaseTool,
) ([]tool.BaseTool, adk.ChatModelAgentMiddleware, error) {
	var static, dynamic []tool.BaseTool
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("agent tool search info: %w", err)
		}
		switch info.Name {
		case "list_devices", "get_device", "list_entities", "get_entity":
			static = append(static, candidate)
		default:
			dynamic = append(dynamic, candidate)
		}
	}
	if len(dynamic) == 0 {
		return static, nil, nil
	}
	search, err := toolsearch.New(ctx, &toolsearch.Config{DynamicTools: dynamic, UseModelToolSearch: false})
	if err != nil {
		return nil, nil, fmt.Errorf("agent tool search: %w", err)
	}
	return static, search, nil
}
