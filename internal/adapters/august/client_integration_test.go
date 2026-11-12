//go:build integration

package august //nolint:testpackage // Exercises real HTTP transport against a synthetic peer.

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

func TestNativeGETRedirectRefusalAndCancellation(t *testing.T) {
	t.Parallel()
	var redirected atomic.Int32
	started, returned := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		switch r.URL.Path {
		case inventoryPath:
			if r.Header.Get("X-August-Access-Token") != "original-token" ||
				r.Header.Get("X-August-Api-Key") != applicationID {
				t.Error("incorrect credential headers")
			}
			if r.Header.Get("Content-Length") != "" || r.ContentLength != 0 {
				t.Error("incorrect native bodyless GET envelope")
			}
			http.Redirect(w, r, "/credential-leak", http.StatusTemporaryRedirect)
		case "/credential-leak":
			redirected.Add(1)
		default:
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
	if _, err = c.inventory(t.Context()); err == nil || redirected.Load() != 0 {
		t.Fatal("followed redirect or accepted failed listing")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, readErr := c.status(ctx, testLockID); done <- readErr }()
	<-started
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("lost caller cancellation: %v", err)
	}
	<-returned
}
