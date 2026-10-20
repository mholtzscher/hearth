package discord

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	sendMessageTimeoutMS        = 10000
	maximumDestinationKeyLength = 63
	maximumTitleLength          = 256
	maximumTextLength           = 2000
	schemaStringType            = "string"
)

// Functions exposes the immutable vendor contract for this runtime.
func (client *Client) Functions() []adapter.Function {
	return []adapter.Function{{
		Descriptor: adapter.FunctionDescriptor{
			Key:         "send_message",
			Description: "Send a one-off message to a configured Discord destination. Reuses the notification webhook path with one POST and no retry.",
			InputSchema: client.inputSchema,
			OutputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["destination","receipt","status"],"properties":{"destination":{"type":"string","minLength":1,"maxLength":63},"receipt":{"type":"string","minLength":1,"maxLength":256},"status":{"const":"sent"}}}`,
			),
			TimeoutMS: sendMessageTimeoutMS,
		},
		Handler: client.sendMessage,
	}}
}

// sendMessageInputSchema derives the destination enum from configured routes so
// clients cannot confuse a notification display name with an adapter-local key.
func sendMessageInputSchema(routes map[string]string) (json.RawMessage, error) {
	keys := make([]string, 0, len(routes))
	for key := range routes {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	type property struct {
		Type        string   `json:"type"`
		MinLength   int      `json:"minLength,omitempty"`
		MaxLength   int      `json:"maxLength,omitempty"`
		Enum        []string `json:"enum,omitempty"`
		Description string   `json:"description,omitempty"`
	}
	type schema struct {
		Type                 string              `json:"type"`
		AdditionalProperties bool                `json:"additionalProperties"`
		Required             []string            `json:"required"`
		Properties           map[string]property `json:"properties"`
	}
	return json.Marshal(schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"destination", "text"},
		Properties: map[string]property{
			"destination": {
				Type:      schemaStringType,
				MinLength: 1,
				MaxLength: maximumDestinationKeyLength,
				Enum:      keys,
				Description: "Local destination key from the adapter configuration; " +
					"notification destination display names are not accepted.",
			},
			"text":  {Type: schemaStringType, MinLength: 1, MaxLength: maximumTextLength},
			"title": {Type: schemaStringType, MaxLength: maximumTitleLength},
		},
	})
}

func (client *Client) sendMessage(ctx context.Context, request adapter.FunctionRequest) adapter.FunctionResult {
	var arguments struct {
		Destination string `json:"destination"`
		Text        string `json:"text"`
		Title       string `json:"title"`
	}
	if json.Unmarshal(request.Arguments, &arguments) != nil {
		return adapter.FunctionNotExecuted{ReasonCode: "invalid_message"}
	}
	if _, known := client.routes[arguments.Destination]; !known {
		return adapter.FunctionNotExecuted{ReasonCode: "destination_unknown"}
	}
	message := adapter.NotificationMessage{Title: arguments.Title, Text: arguments.Text}
	if !validMessage(message) {
		return adapter.FunctionNotExecuted{ReasonCode: "invalid_message"}
	}
	outcome := client.Send(ctx, arguments.Destination, message)
	switch outcome.Outcome {
	case "accepted":
		result, err := json.Marshal(struct {
			Destination string `json:"destination"`
			Receipt     string `json:"receipt"`
			Status      string `json:"status"`
		}{Destination: arguments.Destination, Receipt: outcome.Receipt, Status: "sent"})
		if err != nil || outcome.Receipt == "" {
			return adapter.FunctionUnknown{ReasonCode: "invalid_response"}
		}
		return adapter.FunctionSucceeded{Result: json.RawMessage(result)}
	case "rejected":
		return adapter.FunctionFailed{ReasonCode: outcome.ReasonCode}
	case "not_sent":
		return adapter.FunctionNotExecuted{ReasonCode: outcome.ReasonCode}
	default:
		return adapter.FunctionUnknown{ReasonCode: outcome.ReasonCode}
	}
}
