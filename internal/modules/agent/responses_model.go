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
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const responsesOutputKey = "hearth_responses_output"

// responsesModel adapts the provider at the model boundary. The retained output
// items are authoritative for later requests; Content and ToolCalls are their
// projection for ADK and Hearth's existing HTTP/history contract.
type responsesModel struct {
	client responses.ResponseService
	config ModelConfig
	tools  []*schema.ToolInfo
}

func newResponsesModel(config ModelConfig) *responsesModel {
	opts := []option.RequestOption{
		option.WithEnvironmentProduction(),
		option.WithAPIKey(config.APIKey),
		option.WithMaxRetries(0),
	}
	if config.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(config.BaseURL))
	}
	return &responsesModel{client: responses.NewResponseService(opts...), config: config}
}

func (provider *responsesModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound := *provider
	bound.tools = tools
	return &bound, nil
}

func (provider *responsesModel) Generate(
	ctx context.Context,
	messages []*schema.Message,
	options ...model.Option,
) (*schema.Message, error) {
	input, err := responsesInput(messages)
	if err != nil {
		return nil, err
	}
	settings := model.GetCommonOptions(&model.Options{Tools: provider.tools}, options...)
	tools, err := responsesTools(settings.Tools)
	if err != nil {
		return nil, err
	}
	stream := provider.client.NewStreaming(ctx, responses.ResponseNewParams{
		Model:     provider.config.Model,
		Input:     responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Tools:     tools,
		Reasoning: shared.ReasoningParam{Effort: shared.ReasoningEffort(provider.config.ReasoningEffort)},
		// SQLite owns conversation context. Encrypted reasoning allows stateless
		// continuation after tool calls and restarts without provider-side storage.
		Store:   openai.Bool(false),
		Include: []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
	})
	defer stream.Close()
	response, err := finalResponse(ctx, stream)
	if err != nil {
		return nil, err
	}
	return responsesMessage(response)
}

// finalResponse streams only to learn early which tools the model is calling.
// The terminal event carries the complete response, so the returned message is
// the same one a non-streaming request produces.
func finalResponse(
	ctx context.Context,
	stream *ssestream.Stream[responses.ResponseStreamEventUnion],
) (*responses.Response, error) {
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_item.added":
			if event.Item.Type == "function_call" {
				announceToolPreparing(ctx, event.Item.CallID, event.Item.Name)
			}
		case "response.completed", "response.failed", "response.incomplete":
			return &event.Response, nil
		case "error":
			return nil, fmt.Errorf("agent response stream error %q", event.Code)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("agent response stream ended before the response completed")
}

func (*responsesModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("agent Responses model supports non-streaming generation only")
}

func responsesInput(messages []*schema.Message) (responses.ResponseInputParam, error) {
	var input responses.ResponseInputParam
	for _, message := range messages {
		switch message.Role {
		case schema.System:
			input = append(
				input,
				responses.ResponseInputItemParamOfMessage(message.Content, responses.EasyInputMessageRoleSystem),
			)
		case schema.User:
			input = append(
				input,
				responses.ResponseInputItemParamOfMessage(message.Content, responses.EasyInputMessageRoleUser),
			)
		case schema.Tool:
			input = append(
				input,
				responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
						CallID: openai.String(message.ToolCallID),
						Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
							OfString: openai.String(message.Content),
						},
					},
				},
			)
		case schema.Assistant:
			raw, ok := message.Extra[responsesOutputKey].(string)
			if !ok {
				return nil, errors.New("agent assistant history lacks Responses output; start a new conversation")
			}
			var items []responses.ResponseInputItemUnion
			if err := json.Unmarshal([]byte(raw), &items); err != nil {
				return nil, fmt.Errorf("decode retained Responses output: %w", err)
			}
			for _, item := range items {
				input = append(input, item.ToParam())
			}
		default:
			return nil, fmt.Errorf("unsupported agent message role %q", message.Role)
		}
	}
	return input, nil
}

func responsesTools(infos []*schema.ToolInfo) ([]responses.ToolUnionParam, error) {
	tools := make([]responses.ToolUnionParam, 0, len(infos))
	for _, info := range infos {
		parameters, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return nil, fmt.Errorf("agent tool %s schema: %w", info.Name, err)
		}
		raw, err := json.Marshal(parameters)
		if err != nil {
			return nil, err
		}
		var fields map[string]any
		if err = json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		tools = append(tools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
			Name: info.Name, Description: openai.String(info.Desc), Parameters: fields,
			// MCP schemas allow optional fields. Responses otherwise normalizes
			// schemas to strict mode, changing the shared tool contract.
			Strict: openai.Bool(false),
		}})
	}
	return tools, nil
}

func responsesMessage(response *responses.Response) (*schema.Message, error) {
	if response.Status != responses.ResponseStatusCompleted {
		return nil, fmt.Errorf("agent response did not complete: %s", response.Status)
	}
	message := schema.AssistantMessage("", nil)
	var text strings.Builder
	var output []json.RawMessage
	for _, item := range response.Output {
		switch item.Type {
		case "reasoning":
			// Retain the opaque reasoning item without publishing it as a reply.
		case "function_call":
			call := item.AsFunctionCall()
			if call.CallID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
				return nil, errors.New("agent response contains an invalid function call")
			}
			message.ToolCalls = append(
				message.ToolCalls,
				schema.ToolCall{ID: call.CallID, Type: "function", Function: schema.FunctionCall{
					Name: call.Name, Arguments: call.Arguments,
				}},
			)
		case "message":
			for _, content := range item.AsMessage().Content {
				switch content.Type {
				case "output_text":
					text.WriteString(content.Text)
				case "refusal":
					text.WriteString(content.Refusal)
				default:
					return nil, fmt.Errorf("unsupported agent response content %q", content.Type)
				}
			}
		default:
			return nil, fmt.Errorf("unsupported agent response output %q", item.Type)
		}
		output = append(output, json.RawMessage(item.RawJSON()))
	}
	message.Content = text.String()
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return nil, errModelResponseInvalid
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	message.Extra = map[string]any{responsesOutputKey: string(raw)}
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens: int(
			response.Usage.InputTokens,
		),
		CompletionTokens: int(response.Usage.OutputTokens),
		TotalTokens:      int(response.Usage.TotalTokens),
	}}
	return message, nil
}
