package mcpapi_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type isolatedInput struct {
	Subject string `json:"subject,omitempty"`
}
type isolatedOutput struct {
	Service string `json:"service"`
	Subject string `json:"subject"`
}

func TestPreparedContractsOwnSchemasAndIsolateConcurrentServers(t *testing.T) {
	t.Parallel()
	inputBytes := json.RawMessage(
		`{"type":"object","properties":{"subject":{"type":"string","default":"fallback"}},"additionalProperties":false}`,
	)
	outputSchema := map[string]any{
		"type":                 "object",
		"required":             []string{"service", "subject"},
		"additionalProperties": false,
		"properties": map[string]any{
			"service": map[string]any{"type": "string"},
			"subject": map[string]any{"type": "string", "minLength": 1},
		},
	}
	input := preparedInput[isolatedInput](t, inputBytes)
	output := preparedOutput[isolatedOutput](t, outputSchema)
	// Mutate both original representations before either registration.
	for index := range inputBytes {
		inputBytes[index] = ' '
	}
	outputSchema["required"] = []string{"absent"}
	outputSchema["properties"].(map[string]any)["subject"].(map[string]any)["minLength"] = 100

	for index, service := range []string{"first-service", "second-service"} {
		t.Run(service, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := mcpapi.New(mcpapi.Config{Name: service, Version: "1"})
			registerPrepared(
				server,
				index == 1,
				mcpapi.Tool[isolatedInput, isolatedOutput]{Name: "isolated", Input: input, Output: output,
					Handler: func(_ context.Context, in isolatedInput) (isolatedOutput, error) {
						calls.Add(1)
						if in.Subject == "invalid-output" {
							return isolatedOutput{Service: service}, nil
						}
						return isolatedOutput{Service: service, Subject: in.Subject}, nil
					},
				},
			)
			t.Run("concurrent calls", func(t *testing.T) {
				for call := range 24 {
					t.Run(strconv.Itoa(call), func(t *testing.T) {
						t.Parallel()
						assertIsolatedCall(t, server, service, call)
					})
				}
			})
			if got := calls.Load(); got != 18 {
				t.Fatalf("handler calls = %d, want 18 (invalid inputs must not run)", got)
			}
		})
	}
}

func assertIsolatedCall(t *testing.T, server *mcpapi.Server, service string, call int) {
	t.Helper()
	arguments, subject := `{}`, "fallback"
	switch call % 4 {
	case 1:
		arguments, subject = `{"subject":"explicit"}`, "explicit"
	case 2:
		arguments = `{"subject":17}`
	case 3:
		arguments = `{"subject":"invalid-output"}`
	}
	result := callExactWireTool(t, server.HTTPHandler(), "isolated", arguments)
	if call%4 >= 2 {
		if !result.IsError {
			t.Fatalf("invalid call succeeded: %+v", result)
		}
		return
	}
	want := `{"service":"` + service + `","subject":"` + subject + `"}`
	if result.IsError || string(result.Structured) != want || result.Content[0].Text != want {
		t.Fatalf("contract/default/service contamination: %+v, want %s", result, want)
	}
}

func TestDefaultPreparedContractsAreReusableAcrossServers(t *testing.T) {
	t.Parallel()
	for index, service := range []string{"one", "two"} {
		t.Run(service, func(t *testing.T) {
			t.Parallel()
			server := mcpapi.New(mcpapi.Config{Name: service, Version: "1"})
			registerPrepared(server, index == 1, mcpapi.Tool[*greetInput, *greetOutput]{Name: "cached",
				Handler: func(_ context.Context, in *greetInput) (*greetOutput, error) {
					return &greetOutput{Greeting: service + ":" + in.Name}, nil
				},
			})
			for range 4 {
				result := callExactWireTool(t, server.HTTPHandler(), "cached", `{"name":"world"}`)
				want := `{"greeting":"` + service + `:world"}`
				if result.IsError || string(result.Structured) != want {
					t.Fatalf("cached contract result = %+v", result)
				}
				invalid := callExactWireTool(t, server.HTTPHandler(), "cached", `{}`)
				if !invalid.IsError {
					t.Fatalf("missing required name accepted: %+v", invalid)
				}
			}
		})
	}
}
