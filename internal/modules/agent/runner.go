package agent

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const unknownToolOutcome = "Tool outcome is unknown: no result was retained. Do not repeat this call automatically. Inspect durable evidence before deciding what happened."

// Hooks own persistence. Consume through closure even after failure so Drain
// joins all model/tool work. Preserve a single error's cancellation identity.
func consumeRunner(iterator *adk.AsyncIterator[*adk.AgentEvent]) error {
	var runErr error
	for {
		event, ok := iterator.Next()
		if !ok {
			return runErr
		}
		if event.Err == nil {
			continue
		}
		if runErr == nil {
			runErr = event.Err
		} else {
			runErr = errors.Join(runErr, event.Err)
		}
	}
}

// newAgentRunner gives ADK the loop, instruction composition, native call IDs,
// and input repair. Hearth's hooks own durable evidence and public turn events.
func (service *Service) newAgentRunner(ctx context.Context, trace *turnTrace) (*adk.Runner, error) {
	skills, err := skill.NewMiddleware(ctx, &skill.Config{
		Backend: service.skills,
		CustomSystemPrompt: func(_ context.Context, toolName string) string {
			return "Before creating or editing an Automation, load hearth-automations using the " + toolName +
				" tool and follow its workflow. Skills return bundled instructions inline; all operations use Hearth tools. " +
				"No shell, filesystem, downloads, or subagents are available."
		},
		CustomToolDescription: func(_ context.Context, available []skill.FrontMatter) string {
			return "Load bundled instructions before automation authoring. Available skill: " + available[0].Name + ": " + available[0].Description
		},
	})
	if err != nil {
		return nil, err
	}
	repair, err := patchtoolcalls.New(ctx, &patchtoolcalls.Config{
		PatchedToolResultGenerator: func(context.Context, string, string, *schema.ToolArgument) (*patchtoolcalls.PatchedToolResult, error) {
			return &patchtoolcalls.PatchedToolResult{Content: unknownToolOutcome}, nil
		},
	})
	if err != nil {
		return nil, err
	}
	staticTools, search, err := agentToolSearch(ctx, service.tools)
	if err != nil {
		return nil, err
	}
	handlers := []adk.ChatModelAgentMiddleware{skills, repair}
	if search != nil {
		handlers = append(handlers, search)
	}
	handlers = append(handlers, trace)
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name: "hearth", Instruction: defaultSystemPrompt,
		Model:         turnModel{BaseChatModel: service.chatModel, trace: trace},
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: staticTools}},
		MaxIterations: service.maxIterations,
		Handlers:      handlers,
	})
	if err != nil {
		return nil, err
	}
	return adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent}), nil
}
