package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
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

func TestSendMessageDescriptorContract(t *testing.T) {
	t.Parallel()
	client := controlledClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(writer, `{"id":"1"}`)
	})
	functions := client.Functions()
	if len(functions) != 1 {
		t.Fatalf("functions=%d", len(functions))
	}
	descriptor := functions[0].Descriptor
	if descriptor.Key != "send_message" || descriptor.TimeoutMS != 10000 {
		t.Fatalf("descriptor=%+v", descriptor)
	}
	if descriptor.Description == "" || len(descriptor.Description) > 2000 {
		t.Fatalf("description length=%d", len(descriptor.Description))
	}
}

func TestSendMessageSuccessReturnsReceipt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := controlledClient(t, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var payload struct {
			Embeds []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"embeds"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Embeds) != 1 || payload.Embeds[0].Title != "Freezer" ||
			payload.Embeds[0].Description != "Too warm" {
			t.Errorf("payload=%+v", payload)
		}
		_, _ = fmt.Fprint(writer, `{"id":"987654321"}`)
	})
	result := invokeSend(client, functionArguments(t, "home", "Too warm", "Freezer"))
	succeeded, ok := result.(adapter.FunctionSucceeded)
	if !ok {
		t.Fatalf("result=%T %+v", result, result)
	}
	var decoded struct {
		Destination string `json:"destination"`
		Receipt     string `json:"receipt"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(succeeded.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != (struct {
		Destination string `json:"destination"`
		Receipt     string `json:"receipt"`
		Status      string `json:"status"`
	}{Destination: "home", Receipt: "987654321", Status: "sent"}) || calls.Load() != 1 {
		t.Fatalf("decoded=%+v calls=%d", decoded, calls.Load())
	}
}

func TestSendMessageLocalGuardsDoNotSubmit(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := controlledClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(writer, `{"id":"1"}`)
	})
	for _, test := range []struct {
		name      string
		arguments json.RawMessage
		reason    string
	}{
		{"unknown destination", functionArguments(t, "missing", "hi", ""), "destination_unknown"},
		{"blank text", functionArguments(t, "home", "  ", ""), "invalid_message"},
		{"overlong text", functionArguments(t, "home", strings.Repeat("x", 2001), ""), "invalid_message"},
		{"malformed json", json.RawMessage(`{"destination":`), "invalid_message"},
	} {
		result := invokeSend(client, test.arguments)
		notExecuted, ok := result.(adapter.FunctionNotExecuted)
		if !ok || notExecuted.ReasonCode != test.reason {
			t.Errorf("%s: result=%+v", test.name, result)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestSendMessageProviderMapping(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
		kind   string
		reason string
	}{
		{"rate limited", 429, "limited", "failed", "provider_rate_limited"},
		{"refused", 422, "no", "failed", "provider_rejected"},
		{"server error", 500, "boom", "unknown", "provider_error"},
		{"malformed success", 200, `{"id":123}`, "unknown", "invalid_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := controlledClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				writer.WriteHeader(test.status)
				_, _ = fmt.Fprint(writer, test.body)
			})
			result := invokeSend(client, functionArguments(t, "home", "hi", ""))
			switch test.kind {
			case "failed":
				got, ok := result.(adapter.FunctionFailed)
				if !ok || got.ReasonCode != test.reason {
					t.Fatalf("result=%+v", result)
				}
			case "unknown":
				got, ok := result.(adapter.FunctionUnknown)
				if !ok || got.ReasonCode != test.reason {
					t.Fatalf("result=%+v", result)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestSendMessageExpiredDeadlineDoesNotSubmit(t *testing.T) {
	t.Parallel()
	client := controlledClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("must not submit")
		_, _ = fmt.Fprint(writer, `{"id":"1"}`)
	})
	functions := client.Functions()
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	// Exercise the deadline path through Send mapping with an expired context.
	result := functions[0].Handler(expired, adapter.FunctionRequest{
		InvocationID: "afi_expired",
		FunctionKey:  "send_message",
		Arguments:    functionArguments(t, "home", "hi", ""),
		Deadline:     time.Now().Add(time.Minute),
	})
	notExecuted, ok := result.(adapter.FunctionNotExecuted)
	if !ok || notExecuted.ReasonCode != "deadline_expired" {
		t.Fatalf("result=%+v", result)
	}
}
