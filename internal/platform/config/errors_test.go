package config_test

import (
	"errors"
	"strings"
	"testing"

	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
)

// A validation failure keeps its path for direct callers while exposing a
// path-free reason that a process record can publish.
func TestInvalidErrorCarriesAPathFreeReason(t *testing.T) {
	t.Parallel()
	underlying := errors.New("http_addr must contain a host and port")
	err := platformconfig.Invalid("/etc/hearth/hearthd.yaml", underlying)

	if !strings.Contains(err.Error(), "/etc/hearth/hearthd.yaml") {
		t.Fatalf("error lost the path: %q", err.Error())
	}
	reason := platformconfig.Reason(err)
	if reason != underlying.Error() {
		t.Fatalf("reason = %q, want %q", reason, underlying.Error())
	}
	if strings.Contains(reason, "/etc/hearth/hearthd.yaml") {
		t.Fatalf("reason repeated the path: %q", reason)
	}
	if !errors.Is(err, underlying) {
		t.Fatalf("errors.Is lost the underlying failure: %v", err)
	}
	var invalid *platformconfig.InvalidError
	if !errors.As(err, &invalid) || invalid.Path != "/etc/hearth/hearthd.yaml" {
		t.Fatalf("errors.As did not recover the InvalidError: %#v", invalid)
	}
}

// An unreadable or undecodable configuration is classified without its text,
// because a decode failure can echo a configured value.
func TestReasonClassifiesUnreportedFailures(t *testing.T) {
	t.Parallel()
	secret := errors.New("yaml: unmarshal errors: line 1: cannot unmarshal !!str `super-secret` into bool")
	reason := platformconfig.Reason(secret)
	if reason == "" {
		t.Fatal("unreported failure has no classification")
	}
	if strings.Contains(reason, "super-secret") || strings.Contains(reason, "yaml") {
		t.Fatalf("classification echoed the failure text: %q", reason)
	}
}
