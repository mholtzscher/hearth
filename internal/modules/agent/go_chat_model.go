package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

const goChatMessageKey = "hearth_go_chat_message"

// modelConversationKey is request-scoped. A shared provider must never keep the
// current conversation's routing header in mutable client configuration.
type modelConversationKey struct{}

// goChatModel owns Go's DeepSeek Chat Completions boundary. ADK receives text
// and native tool calls; private reasoning is retained only for continuation.
type goChatModel struct {
	client openai.ChatCompletionService
	config ModelConfig
	tools  []*schema.ToolInfo
}

func newGoChatModel(config ModelConfig) *goChatModel {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = DefaultGoBaseURL
	}
	return &goChatModel{
		client: openai.NewChatCompletionService(
			option.WithEnvironmentProduction(), option.WithAPIKey(config.APIKey),
			option.WithBaseURL(baseURL), option.WithMaxRetries(0),
			option.WithHeader("User-Agent", "hearth/1.0"),
		),
		config: config,
	}
}

func (provider *goChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound := *provider
	bound.tools = tools
	return &bound, nil
}

func (provider *goChatModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	conversationID, _ := ctx.Value(modelConversationKey{}).(string)
	if conversationID == "" {
		return nil, errors.New("agent Go request lacks a conversation identity")
	}
	input, err := goChatInput(messages)
	if err != nil {
		return nil, err
	}
	settings := model.GetCommonOptions(&model.Options{Tools: provider.tools}, options...)
	tools, err := goChatTools(settings.Tools)
	if err != nil {
		return nil, err
	}
	stream := provider.client.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model: provider.config.Model, Messages: input, Tools: tools,
		ReasoningEffort: shared.ReasoningEffort(provider.config.ReasoningEffort),
		StreamOptions:   openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	}, option.WithHeader("X-Opencode-Session", conversationID))
	defer stream.Close()
	var output goChatAccumulator
	for stream.Next() {
		if err = output.add(ctx, stream.Current()); err != nil {
			return nil, err
		}
	}
	if err = stream.Err(); err != nil {
		return nil, err
	}
	return output.message()
}

func (*goChatModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("agent Go model supports non-streaming generation only")
}

func goChatInput(messages []*schema.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	input := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case schema.System:
			input = append(input, openai.SystemMessage(message.Content))
		case schema.User:
			input = append(input, openai.UserMessage(message.Content))
		case schema.Tool:
			input = append(input, openai.ToolMessage(message.Content, message.ToolCallID))
		case schema.Assistant:
			raw, ok := message.Extra[goChatMessageKey].(string)
			if !ok || !json.Valid([]byte(raw)) {
				return nil, errors.New("agent assistant history lacks Go continuation; start a new conversation")
			}
			// The SDK's standard assistant parameters omit reasoning_content.
			// Override with our retained native message, not a text-only projection.
			input = append(input, param.Override[openai.ChatCompletionMessageParamUnion](json.RawMessage(raw)))
		default:
			return nil, fmt.Errorf("unsupported agent message role %q", message.Role)
		}
	}
	return input, nil
}

func goChatTools(infos []*schema.ToolInfo) ([]openai.ChatCompletionToolUnionParam, error) {
	tools := make([]openai.ChatCompletionToolUnionParam, 0, len(infos))
	for _, info := range infos {
		parameters, err := toolParameters(info)
		if err != nil {
			return nil, err
		}
		tools = append(tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name: info.Name, Description: openai.String(info.Desc),
			Parameters: parameters, Strict: openai.Bool(false),
		}))
	}
	return tools, nil
}

type goChatAccumulator struct {
	completion   openai.ChatCompletionAccumulator
	reasoning    strings.Builder
	finishReason string
	announced    map[string]bool
}

func (output *goChatAccumulator) add(ctx context.Context, chunk openai.ChatCompletionChunk) error {
	if len(chunk.Choices) > 1 || (len(chunk.Choices) == 1 && chunk.Choices[0].Index != 0) {
		return errors.New("agent Go response has unexpected choices")
	}
	if len(chunk.Choices) == 1 {
		if output.finishReason != "" {
			return errors.New("agent Go response continued after completion")
		}
		choice := chunk.Choices[0]
		if raw := choice.Delta.JSON.ExtraFields["reasoning_content"].Raw(); raw != "" && raw != "null" {
			var reasoning string
			if err := json.Unmarshal([]byte(raw), &reasoning); err != nil {
				return fmt.Errorf("decode agent Go reasoning: %w", err)
			}
			output.reasoning.WriteString(reasoning)
		}
		output.finishReason = choice.FinishReason
	}
	if !output.completion.AddChunk(chunk) {
		return errors.New("agent Go stream changed response identity")
	}
	if len(output.completion.Choices) == 1 {
		output.announce(ctx)
	}
	return nil
}

func (output *goChatAccumulator) announce(ctx context.Context) {
	if output.announced == nil {
		output.announced = make(map[string]bool)
	}
	for _, call := range output.completion.Choices[0].Message.ToolCalls {
		if call.ID != "" && call.Function.Name != "" && call.Function.Arguments != "" && !output.announced[call.ID] {
			output.announced[call.ID] = true
			announceToolPreparing(ctx, call.ID, call.Function.Name)
		}
	}
}

// goChatAssistant is the private continuation document, including reasoning
// even for final text responses. It is never the public history representation.
type goChatAssistant struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []goChatToolCall `json:"tool_calls,omitempty"`
}

type goChatToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function schema.FunctionCall `json:"function"`
}

func (output *goChatAccumulator) message() (*schema.Message, error) {
	if len(output.completion.Choices) != 1 || (output.finishReason != "stop" && output.finishReason != "tool_calls") {
		return nil, errors.New("agent Go response did not complete")
	}
	response := output.completion.Choices[0].Message
	if (output.finishReason == "tool_calls") != (len(response.ToolCalls) > 0) {
		return nil, errors.New("agent Go response has inconsistent tool completion")
	}
	message := schema.AssistantMessage(response.Content+response.Refusal, nil)
	native := goChatAssistant{Role: "assistant", Content: message.Content, ReasoningContent: output.reasoning.String()}
	ids := make(map[string]bool, len(response.ToolCalls))
	for _, call := range response.ToolCalls {
		if call.Type != functionToolType || call.ID == "" || ids[call.ID] || call.Function.Name == "" ||
			!json.Valid([]byte(call.Function.Arguments)) {
			return nil, errors.New("agent Go response contains an invalid function call")
		}
		ids[call.ID] = true
		function := schema.FunctionCall{Name: call.Function.Name, Arguments: call.Function.Arguments}
		message.ToolCalls = append(
			message.ToolCalls,
			schema.ToolCall{ID: call.ID, Type: functionToolType, Function: function},
		)
		native.ToolCalls = append(
			native.ToolCalls,
			goChatToolCall{ID: call.ID, Type: functionToolType, Function: function},
		)
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return nil, errModelResponseInvalid
	}
	raw, err := json.Marshal(native)
	if err != nil {
		return nil, err
	}
	message.Extra = map[string]any{goChatMessageKey: string(raw)}
	usage := output.completion.Usage
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens: int(
			usage.PromptTokens,
		),
		CompletionTokens: int(usage.CompletionTokens),
		TotalTokens:      int(usage.TotalTokens),
	}}
	return message, nil
}
