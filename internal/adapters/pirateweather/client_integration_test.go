//go:build integration

package pirateweather //nolint:testpackage // Exercises the private client with real HTTP.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The synthetic server exercises real HTTP; production origin remains fixed.
func serverTransport(t *testing.T, server *httptest.Server) http.RoundTripper {
	t.Helper()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		cloned := request.Clone(request.Context())
		cloned.URL.Scheme, cloned.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(cloned)
	})
}

func TestHTTPRejectsRedirectAndOversizedBody(t *testing.T) {
	t.Parallel()
	var forwarded atomic.Int32
	destination := httptest.NewServer(
		http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { forwarded.Add(1) }),
	)
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("redirect") != "" {
			t.Error("unexpected parameter")
		}
		if r.Header.Get("Apikey") != "secret-test-key" {
			t.Error("missing key")
		}
		if r.URL.Query().Get("exclude") == "minutely,alerts" {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxProviderBody+1))
	}))
	defer server.Close()
	c := testClient(t, serverTransport(t, server))
	if _, err := c.poll(context.Background()); err == nil || err.Error() != "provider_unavailable" {
		t.Fatalf("redirect: %v", err)
	}
	if forwarded.Load() != 0 {
		t.Fatal("followed redirect")
	}
	if _, _, err := c.request(context.Background(), "hourly"); err == nil || err.Error() != "invalid_response" {
		t.Fatalf("body: %v", err)
	}
}

func TestHTTPRespectsCancellationDuringResponse(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := testClient(t, serverTransport(t, server))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.poll(ctx); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "provider_unavailable" {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not stop")
	}
}
