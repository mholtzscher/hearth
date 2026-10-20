//go:build integration

package discord_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/discord"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func controlledClient(t *testing.T, handler http.HandlerFunc) *discord.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := discord.New(map[string]string{"home": testWebhook}, rewriteTransport{target: target})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// A1 protects the independently specified Discord payload and receipt boundary.
func TestSinglePOSTPayloadAndReceipt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := controlledClient(t, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Query().Get("wait") != "true" ||
			request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request metadata: %s %s %v", request.Method, request.URL, request.Header)
		}
		var payload struct {
			Embeds []struct {
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"embeds"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Embeds) != 1 || payload.Embeds[0].Title != "Freezer" ||
			payload.Embeds[0].Description != "@everyone Too warm" ||
			payload.AllowedMentions.Parse == nil ||
			len(payload.AllowedMentions.Parse) != 0 {
			t.Errorf("payload: %+v", payload)
		}
		_, _ = fmt.Fprint(writer, `{"id":"1234567890","unknown_field":true}`)
	})
	got := client.Send(t.Context(), "home", adapter.NotificationMessage{Title: "Freezer", Text: "@everyone Too warm"})
	if got != (adapter.NotificationOutcome{Outcome: "accepted", Receipt: "1234567890"}) || calls.Load() != 1 {
		t.Fatalf("outcome=%+v calls=%d", got, calls.Load())
	}
}

// A2 protects refusal/ambiguity classification, no redirect/retry and credential isolation.
func TestHTTPFailuresAreSingleAttemptAndSanitized(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		status                int
		body, outcome, reason string
		drop                  bool
	}{
		{"bad request", 400, testWebhook, "rejected", "invalid_message", false},
		{"unauthorized", 401, testWebhook, "rejected", "provider_forbidden", false},
		{"forbidden", 403, testWebhook, "rejected", "provider_forbidden", false},
		{"missing", 404, testWebhook, "rejected", "destination_unknown", false},
		{"rate limited", 429, testWebhook, "rejected", "provider_rate_limited", false},
		{"other refusal", 422, testWebhook, "rejected", "provider_rejected", false},
		{"server error", 500, testWebhook, "unknown", "provider_error", false},
		{"redirect", 307, testWebhook, "unknown", "provider_error", false},
		{"unexpected success", 204, "", "unknown", "provider_error", false},
		{"malformed", 200, testWebhook, "unknown", "invalid_response", false},
		{"numeric id", 200, `{"id":123}`, "unknown", "invalid_response", false},
		{"empty id", 200, `{"id":""}`, "unknown", "invalid_response", false},
		{"nondecimal id", 200, `{"id":"abc"}`, "unknown", "invalid_response", false},
		{"long id", 200, `{"id":"` + strings.Repeat("1", 257) + `"}`, "unknown", "invalid_response", false},
		{"oversized", 200, `{"id":"123","extra":"` + strings.Repeat("x", 64*1024) + `"}`, "unknown", "invalid_response", false},
		{"trailing json", 200, `{"id":"123"}{}`, "unknown", "invalid_response", false},
		{"dropped", 200, "", "unknown", "transport_error", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := controlledClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if test.drop {
					connection, _, err := http.NewResponseController(writer).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = connection.Close()
					return
				}
				writer.Header().Set("Location", testWebhook)
				writer.WriteHeader(test.status)
				_, _ = fmt.Fprint(writer, test.body)
			})
			got := client.Send(t.Context(), "home", adapter.NotificationMessage{Text: "test"})
			if got != (adapter.NotificationOutcome{Outcome: test.outcome, ReasonCode: test.reason}) ||
				calls.Load() != 1 {
				t.Fatalf("outcome=%+v calls=%d", got, calls.Load())
			}
			if strings.Contains(fmt.Sprint(got), testWebhook) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestLocalGuardsAndUTF16LimitsDoNotSubmit(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := controlledClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprint(writer, `{"id":"1"}`)
	})
	for _, test := range []struct {
		key     string
		message adapter.NotificationMessage
		reason  string
	}{
		{"missing", adapter.NotificationMessage{Text: "test"}, "destination_unknown"},
		{"home", adapter.NotificationMessage{Text: " \n"}, "invalid_message"},
		{"home", adapter.NotificationMessage{Text: strings.Repeat("x", 2001)}, "invalid_message"},
		{"home", adapter.NotificationMessage{Title: strings.Repeat("😀", 129), Text: "test"}, "invalid_message"},
		{"home", adapter.NotificationMessage{Text: "\xff"}, "invalid_message"},
	} {
		got := client.Send(t.Context(), test.key, test.message)
		if got.Outcome != "rejected" || got.ReasonCode != test.reason {
			t.Errorf("outcome=%+v", got)
		}
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	got := client.Send(ctx, "home", adapter.NotificationMessage{Text: "test"})
	if got.Outcome != "not_sent" || got.ReasonCode != "deadline_expired" || calls.Load() != 0 {
		t.Fatalf("expired=%+v calls=%d", got, calls.Load())
	}
	stopped, stop := context.WithCancel(t.Context())
	stop()
	got = client.Send(stopped, "home", adapter.NotificationMessage{Text: "test"})
	if got.Outcome != "not_sent" || got.ReasonCode != "destination_unavailable" || calls.Load() != 0 {
		t.Fatalf("canceled=%+v calls=%d", got, calls.Load())
	}
	got = client.Send(
		t.Context(),
		"home",
		adapter.NotificationMessage{Title: strings.Repeat("😀", 128), Text: strings.Repeat("😀", 2000)},
	)
	if got.Outcome != "accepted" || calls.Load() != 1 {
		t.Fatalf("valid boundaries=%+v calls=%d", got, calls.Load())
	}
}

func TestInFlightDeadlineIsAmbiguousAndDoesNotRetry(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	client := controlledClient(t, func(_ http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	got := client.Send(ctx, "home", adapter.NotificationMessage{Text: "test"})
	if got.Outcome != "unknown" || got.ReasonCode != "transport_error" || calls.Load() != 1 {
		t.Fatalf("outcome=%+v calls=%d", got, calls.Load())
	}
}
