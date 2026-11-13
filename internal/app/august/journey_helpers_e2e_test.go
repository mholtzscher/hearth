//go:build e2e

package august //nolint:testpackage // Helpers for the production Core assembly journey.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

func startCore(t *testing.T) (string, string) {
	t.Helper()
	broker := natstest.StartServer(t)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	database := dbtest.MigratedPath(t)
	go func() {
		done <- hearthd.Run(ctx, hearthd.Config{HouseholdTimezone: "UTC", HTTPAddr: address,
			NATSURL: broker.ClientURL(), SQLitePath: database}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(20 * time.Second):
			t.Error("Core did not join")
		}
	})
	base := "http://" + address
	waitCondition(t, func() bool {
		status, _, requestErr := coreRequest(t.Context(), base, http.MethodGet, "/readyz", "")
		return requestErr == nil && status == http.StatusOK
	})
	return base, broker.ClientURL()
}

func startCompanion(t *testing.T, config Config, transport http.RoundTripper) (context.CancelFunc, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var result error
	go func() { result = run(ctx, config, slog.New(slog.DiscardHandler), transport); close(done) }()
	join := func() {
		t.Helper()
		select {
		case <-done:
			if result != nil {
				t.Error(result)
			}
		case <-time.After(10 * time.Second):
			t.Error("companion did not join")
		}
	}
	t.Cleanup(func() { cancel(); join() })
	return cancel, join
}

func waitCondition(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("August journey condition timed out")
		}
	}
}

func coreRequest(ctx context.Context, base, method, path, body string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/merge-patch+json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func coreJSON(t *testing.T, base, method, path, body string, target any) {
	t.Helper()
	status, data, err := coreRequest(t.Context(), base, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: %d %s %v", method, path, status, data, err)
	}
	if err = json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
