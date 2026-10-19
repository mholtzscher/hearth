package discord

import (
	"context"
	"encoding/json"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const sendMessageTimeoutMS = 10000

// Functions exposes the immutable vendor contract for this runtime.
func (client *Client) Functions() []adapter.Function {
	return []adapter.Function{{
		Descriptor: adapter.FunctionDescriptor{
			Key:         "send_message",
			Description: "Send a one-off message to a configured Discord destination. Reuses the notification webhook path with one POST and no retry.",
			InputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["destination","text"],"properties":{"destination":{"type":"string","minLength":1,"maxLength":63},"text":{"type":"string","minLength":1,"maxLength":2000},"title":{"type":"string","maxLength":256}}}`,
			),
			OutputSchema: json.RawMessage(
				`{"type":"object","additionalProperties":false,"required":["destination","receipt","status"],"properties":{"destination":{"type":"string","minLength":1,"maxLength":63},"receipt":{"type":"string","minLength":1,"maxLength":256},"status":{"const":"sent"}}}`,
			),
			TimeoutMS: sendMessageTimeoutMS,
		},
		Handler: client.sendMessage,
	}}
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
