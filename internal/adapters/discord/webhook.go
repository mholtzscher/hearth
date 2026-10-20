// Package discord translates notification messages into single Discord webhook executions.
package discord

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	maximumResponseBytes = 64 * 1024
	maximumSendDuration  = 10 * time.Second
	maximumBMPCodePoint  = 0xffff
)

// Client retains immutable credential routes and function contract; transport
// injection does not change URL policy.
type Client struct {
	routes      map[string]string
	http        *http.Client
	inputSchema json.RawMessage
}

// New validates and copies credentials. A nil transport selects HTTP/1-only HTTPS.
func New(routes map[string]string, transport http.RoundTripper) (*Client, error) {
	resolved := make(map[string]string, len(routes))
	webhookPath := regexp.MustCompile(`^/api(/v[0-9]+)?/webhooks/[0-9]+/[A-Za-z0-9_-]+$`)
	for key, credential := range routes {
		parsed, err := url.Parse(credential)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "discord.com" || parsed.User != nil ||
			parsed.Fragment != "" || strings.Contains(credential, "#") || parsed.RawQuery != "" || parsed.ForceQuery ||
			parsed.RawPath != "" || !webhookPath.MatchString(parsed.Path) {
			return nil, errors.New(
				"webhook secret must contain an HTTPS discord.com incoming webhook URL without query or fragment",
			)
		}
		parsed.RawQuery = "wait=true"
		resolved[key] = parsed.String()
	}
	if transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, errors.New("default HTTP transport is unavailable")
		}
		configured := base.Clone()
		// Go's HTTP/2 transport may retry POST after REFUSED_STREAM/GOAWAY,
		// including some pre-body failures without GetBody. Restrict protocol
		// negotiation, rather than relying on POST's non-idempotent method alone.
		configured.Protocols = new(http.Protocols)
		configured.Protocols.SetHTTP1(true)
		// Clone can inherit an already initialized default transport's h2 ALPN
		// offer. Clear both the TLS offer and the legacy protocol dispatch map.
		if configured.TLSClientConfig == nil {
			configured.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		configured.TLSClientConfig.NextProtos = []string{"http/1.1"}
		configured.TLSNextProto = nil
		transport = configured
	}
	inputSchema, err := sendMessageInputSchema(resolved)
	if err != nil {
		return nil, fmt.Errorf("send_message input schema: %w", err)
	}
	return &Client{routes: resolved, http: &http.Client{Transport: transport, Timeout: maximumSendDuration,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		inputSchema: inputSchema}, nil
}

// Send performs at most one POST. Transport errors never escape this credential boundary.
func (client *Client) Send(
	ctx context.Context,
	key string,
	message adapter.NotificationMessage,
) adapter.NotificationOutcome {
	endpoint, known := client.routes[key]
	if !known {
		return outcome("rejected", "destination_unknown")
	}
	if !validMessage(message) {
		return outcome("rejected", "invalid_message")
	}
	if stopped, reason := submissionStopped(ctx); stopped {
		return outcome("not_sent", reason)
	}
	payload, err := json.Marshal(webhookPayload{
		Embeds:          []embed{{Title: message.Title, Description: message.Text}},
		AllowedMentions: allowedMentions{Parse: []string{}},
	})
	if err != nil {
		return outcome("rejected", "invalid_message")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return outcome("rejected", "invalid_message")
	}
	// NewRequest recognizes bytes.Reader and installs GetBody. Remove rewind
	// capability so HTTP/1 cannot retry this nonempty POST on a reused connection.
	request.GetBody = nil
	request.Header.Set("Content-Type", "application/json")
	// Recheck after local preparation; no external call begins with known expiry.
	if stopped, reason := submissionStopped(ctx); stopped {
		return outcome("not_sent", reason)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return outcome("unknown", "transport_error")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return statusOutcome(response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if err != nil {
		return outcome("unknown", "transport_error")
	}
	var receipt struct {
		ID string `json:"id"`
	}
	if len(body) > maximumResponseBytes || json.Unmarshal(body, &receipt) != nil || len(receipt.ID) > 256 ||
		receipt.ID == "" || strings.Trim(receipt.ID, "0123456789") != "" {
		return outcome("unknown", "invalid_response")
	}
	return adapter.NotificationOutcome{Outcome: "accepted", Receipt: receipt.ID}
}

type embed struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
}

type webhookPayload struct {
	Embeds          []embed         `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

type allowedMentions struct {
	Parse []string `json:"parse"`
}

func validMessage(message adapter.NotificationMessage) bool {
	return utf8.ValidString(message.Title) && utf8.ValidString(message.Text) && strings.TrimSpace(message.Text) != "" &&
		utf8.RuneCountInString(message.Title) <= maximumTitleLength &&
		utf8.RuneCountInString(message.Text) <= maximumTextLength &&
		utf16Length(message.Title) <= maximumTitleLength && utf16Length(message.Text) <= 4096
}

func submissionStopped(ctx context.Context) (bool, string) {
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return true, "deadline_expired"
	}
	if ctx.Err() != nil {
		return true, "destination_unavailable"
	}
	return false, ""
}

func utf16Length(text string) int {
	length := 0
	for _, value := range text {
		length++
		if value > maximumBMPCodePoint {
			length++
		}
	}
	return length
}

func outcome(status, reason string) adapter.NotificationOutcome {
	return adapter.NotificationOutcome{Outcome: status, ReasonCode: reason}
}

func statusOutcome(status int) adapter.NotificationOutcome {
	switch status {
	case http.StatusBadRequest:
		return outcome("rejected", "invalid_message")
	case http.StatusUnauthorized, http.StatusForbidden:
		return outcome("rejected", "provider_forbidden")
	case http.StatusNotFound:
		return outcome("rejected", "destination_unknown")
	case http.StatusTooManyRequests:
		return outcome("rejected", "provider_rate_limited")
	default:
		if status >= 400 && status < 500 {
			return outcome("rejected", "provider_rejected")
		}
		return outcome("unknown", "provider_error")
	}
}
