//go:build integration

package discord //nolint:testpackage // Exercises the constructed production transport without relaxing credential URL validation.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type inspectRequestTransport struct {
	testing   *testing.T
	transport http.RoundTripper
}

func (transport inspectRequestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.GetBody != nil {
		transport.testing.Error("webhook POST exposes a replayable body to the HTTP transport")
	}
	if request.ContentLength <= 0 || request.Body == nil {
		transport.testing.Error("webhook POST has no nonempty body")
	}
	return transport.transport.RoundTrip(request)
}

// Protects D1's one-attempt policy at the actual Go transport boundary. HTTP/2
// can replay REFUSED_STREAM even for POST; HTTP/1 cannot rewind a nonempty POST
// without GetBody. A server offering HTTP/2 must therefore observe only HTTP/1,
// and a dropped response after a reused-connection POST must remain one attempt.
func TestProductionTransportCannotReplayWebhookPOST(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ProtoMajor != 1 {
			t.Errorf("webhook negotiated %s, allowing HTTP/2 retry semantics", request.Proto)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || !strings.Contains(string(body), `"description":"test"`) {
			t.Errorf("POST body=%s error=%v", body, err)
		}
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(writer, `{"id":"123"}`)
			return
		}
		connection, _, err := http.NewResponseController(writer).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client, err := New(map[string]string{"home": "https://discord.com/api/webhooks/123/not-real-secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatal("production constructor did not supply an explicit HTTP transport")
	}
	defer transport.CloseIdleConnections()
	// Only routing/trust are injected. Protocol selection remains the production policy.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.RootCAs = roots
	transport.TLSClientConfig.ServerName = "example.com"
	client.http.Transport = inspectRequestTransport{testing: t, transport: transport}
	first := client.Send(t.Context(), "home", adapter.NotificationMessage{Text: "test"})
	if first.Outcome != "accepted" || first.Receipt != "123" {
		t.Fatalf("warm connection=%+v", first)
	}
	second := client.Send(t.Context(), "home", adapter.NotificationMessage{Text: "test"})
	if second.Outcome != "unknown" || second.ReasonCode != "transport_error" || calls.Load() != 2 {
		t.Fatalf("dropped POST=%+v total requests=%d", second, calls.Load())
	}
}
