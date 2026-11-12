//go:build integration

package levoit //nolint:testpackage // Injects a real HTTP peer at the private client boundary.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

type reroutedTransport struct{ target *url.URL }

func (r reroutedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme, clone.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestHTTPRedirectRefusalAndCancellation(t *testing.T) {
	t.Parallel()
	var redirected atomic.Int32
	started, returned := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		switch r.URL.Path {
		case inventoryPath:
			http.Redirect(w, r, "/credential-leak", http.StatusTemporaryRedirect)
		case "/credential-leak":
			redirected.Add(1)
		case bypassPath:
			close(started)
			<-r.Context().Done()
			close(returned)
		}
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := authenticatedClient(t, reroutedTransport{target: target})
	if _, err = c.devices(t.Context()); err == nil || redirected.Load() != 0 {
		t.Fatal("followed redirect or accepted incomplete inventory")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.setPower(ctx, device{CID: "cid", ConfigModule: "module"}, true) }()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("request did not start")
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel result = %v", err)
	}
	select {
	case <-returned:
	case <-t.Context().Done():
		t.Fatal("request handler did not observe cancellation")
	}
}
