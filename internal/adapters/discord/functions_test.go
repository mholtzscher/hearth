package discord_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/discord"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func functionArguments(t *testing.T, destination, text, title string) json.RawMessage {
	t.Helper()
	payload := map[string]string{"destination": destination, "text": text}
	if title != "" {
		payload["title"] = title
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func invokeSend(client *discord.Client, arguments json.RawMessage) adapter.FunctionResult {
	functions := client.Functions()
	return functions[0].Handler(context.Background(), adapter.FunctionRequest{
		InvocationID: "afi_test",
		FunctionKey:  "send_message",
		Arguments:    arguments,
		Deadline:     time.Now().Add(time.Minute),
	})
}

func TestSendMessageDescriptorSortsDestinationKeys(t *testing.T) {
	t.Parallel()
	client, err := discord.New(map[string]string{
		"zulu":  testWebhook,
		"alpha": testWebhook,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(client.Functions()[0].Descriptor.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Properties["destination"].Enum; !slices.Equal(got, []string{"alpha", "zulu"}) {
		t.Fatalf("destination enum=%v", got)
	}
}
