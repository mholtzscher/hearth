package homeassistant_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	homeassistantadapter "github.com/mholtzscher/hearth/internal/adapters/homeassistant"
	apphomeassistant "github.com/mholtzscher/hearth/internal/app/homeassistant"
)

// This test protects graceful startup cancellation and fails if a canceled
// startup context is converted into a generic Run error instead of
// propagating context cancellation for the executable wrapper to report as
// process.stopped.
func TestRunCanceledContextReturnsCancellation(t *testing.T) {
	t.Parallel()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := apphomeassistant.Config{
		AdapterID: "homeassistant-test",
		NATSURL:   "nats://127.0.0.1:4222",
		Binding: apphomeassistant.BindingConfig{
			Key: "test-light", DeviceName: "Test light",
			EntityExternalID: "light.test", EntityName: "Test light",
		},
		Upstream: apphomeassistant.UpstreamConfig{
			URL: "http://127.0.0.1:8123", TokenFile: tokenFile,
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := apphomeassistant.Run(ctx, config, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run with canceled context = %v, want context.Canceled", err)
	}
}

// This test protects the fatal authentication error code and fails if an
// upstream authentication failure collapses to the generic run_failed code or
// leaks token, URL, or upstream error text into the bounded code.
func TestErrorCodeReportsAuthenticationFailureWithoutSecrets(t *testing.T) {
	t.Parallel()
	const secretSentinel = "token-secret-sentinel-8f2c1a7e"
	const urlSentinel = "https://ha-secret-sentinel-3b9d.internal"
	authErr := &homeassistantadapter.AuthenticationError{
		Message: "invalid access token " + secretSentinel + " for " + urlSentinel,
	}
	if code := apphomeassistant.ErrorCode(authErr); code != "authentication_failed" {
		t.Fatalf("ErrorCode(authentication error) = %q, want authentication_failed", code)
	}
	wrapped := fmt.Errorf("connect Home Assistant: %w", authErr)
	if code := apphomeassistant.ErrorCode(wrapped); code != "authentication_failed" {
		t.Fatalf("ErrorCode(wrapped authentication error) = %q, want authentication_failed", code)
	}
	for _, code := range []string{
		apphomeassistant.ErrorCode(authErr),
		apphomeassistant.ErrorCode(wrapped),
		apphomeassistant.ErrorCode(errors.New("boom " + secretSentinel)),
	} {
		for _, sentinel := range []string{secretSentinel, urlSentinel} {
			if strings.Contains(code, sentinel) {
				t.Fatalf("error code leaks secret %q: %q", sentinel, code)
			}
		}
	}
}

// This test protects the generic error code fallback and fails if a
// non-authentication failure reports anything other than run_failed.
func TestErrorCodeFallsBackToRunFailed(t *testing.T) {
	t.Parallel()
	if code := apphomeassistant.ErrorCode(errors.New("dial failed")); code != "run_failed" {
		t.Fatalf("ErrorCode(generic error) = %q, want run_failed", code)
	}
}
