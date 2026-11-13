//go:build integration

package august //nolint:testpackage // Exercises private subscriber against a synthetic TLS peer.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// The transport rewrites only the socket destination to a synthetic loopback peer.
// It does not contact PubNub or establish compatibility with a physical device.
func TestPushTLSLongPollCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tt") == "0" {
			_, _ = w.Write([]byte(`{"t":{"t":"123","r":3},"m":[]}`))
			return
		}
		if r.URL.Query().Get("tt") != "123" || r.URL.Query().Get("tr") != "3" {
			t.Error("incorrect cursor on wire")
		}
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	destination, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newPushClient(syntheticTransport(func(r *http.Request) (*http.Response, error) {
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = destination.Scheme, destination.Host
		return server.Client().Transport.RoundTrip(request)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- client.Run(ctx, []pushTarget{{testLockID, "alpha"}}, func(context.Context, pushReport) error {
			return errors.New("unexpected report")
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("long poll did not start")
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long poll did not stop")
	}
}
