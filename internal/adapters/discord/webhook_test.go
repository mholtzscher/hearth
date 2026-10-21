package discord_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/discord"
)

const testWebhook = "https://discord.com/api/webhooks/123/not-a-real-secret"

type rewriteTransport struct{ target *url.URL }

func (transport rewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copyRequest := request.Clone(request.Context())
	copyRequest.URL.Scheme = transport.target.Scheme
	copyRequest.URL.Host = transport.target.Host
	return http.DefaultTransport.RoundTrip(copyRequest)
}

func TestProductionURLPolicyCannotBeRelaxedByTransport(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{
		"http://discord.com/api/webhooks/123/token", "https://evil.test/api/webhooks/123/token",
		"https://discord.com.evil.test/api/webhooks/123/token", "https://discord.com:443/api/webhooks/123/token",
		"https://user@discord.com/api/webhooks/123/token", testWebhook + "#fragment", testWebhook + "?thread_id=123", testWebhook + "?wait=true",
		"https://discord.com/api/webhooks/abc/token", "https://discord.com/api/webhooks/123/token/extra",
	} {
		if _, err := discord.New(
			map[string]string{"home": endpoint},
			rewriteTransport{},
		); err == nil ||
			strings.Contains(err.Error(), endpoint) {
			t.Errorf("invalid URL accepted or leaked: %v", err)
		}
	}
}
