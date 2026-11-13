package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/august"
)

func TestCLIConfigAndAuthorize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "august.yaml")
	data := `adapter_id: from-file
credentials_file: /private/credentials.json
installation_id: 00000000-0000-4000-8000-000000000001
locks:
  - binding_key: front
    lock_id: "11111111111111111111111111111111"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_AUGUST_ADAPTER_ID", "from-env")
	var got august.Config
	var output bytes.Buffer
	run := func(_ context.Context, c august.Config, _ *slog.Logger) error { got = c; return nil }
	authorize := func(_ context.Context, c august.Config, codeReader func() (string, error), _ *slog.Logger) error {
		got = c
		code, err := codeReader()
		if err != nil || code != "123456" {
			t.Fatal("verification input not read")
		}
		return nil
	}
	cmd := newCommand(run, authorize, strings.NewReader("123456\n"), &output)
	if err := cmd.Run(
		t.Context(),
		[]string{"august", "--config", path, "--adapter-id", "from-flag", "--poll-interval", "2m"},
	); err != nil {
		t.Fatal(err)
	}
	if got.AdapterID != "from-flag" || got.PollInterval == nil || *got.PollInterval != 2*time.Minute ||
		got.NATSURL != "nats://127.0.0.1:4222" {
		t.Fatal("incorrect precedence or defaults")
	}
	cmd = newCommand(run, authorize, strings.NewReader("123456\n"), &output)
	if err := cmd.Run(t.Context(), []string{"august", "authorize", "--config", path}); err != nil {
		t.Fatal(err)
	}
	if got.AdapterID != "from-env" || strings.Contains(output.String(), "123456") ||
		strings.Contains(output.String(), "/private/credentials.json") {
		t.Fatal("authorization config or output unsafe")
	}
	cmd = newCommand(run, authorize, strings.NewReader(""), &output)
	if err := cmd.Run(t.Context(), []string{"august", "--password", "secret"}); err == nil {
		t.Fatal("accepted password flag")
	}
}

func TestReadCodeIsBounded(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
		fail  bool
	}{
		{"123456\n", "123456", false}, {"123456", "123456", false}, {"", "", true}, {strings.Repeat("1", 1000), "", true},
	} {
		reader := strings.NewReader(test.input)
		got, err := readCode(reader)
		if (err != nil) != test.fail || got != test.want {
			t.Fatalf("readCode = %q, %v", got, err)
		}
		if len(test.input) > 8 && reader.Len() < len(test.input)-8 {
			t.Fatal("read unbounded input")
		}
	}
	if _, err := readCode(io.LimitReader(strings.NewReader(""), 0)); err == nil {
		t.Fatal("empty input accepted")
	}
}

func TestAuthorizeInheritsExplicitFlagsBeforeAndAfterSubcommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("configs", 0o700); err != nil {
		t.Fatal(err)
	}
	defaultConfig := `adapter_id: default-installation
credentials_file: /private/default.json
installation_id: 00000000-0000-4000-8000-000000000001
locks:
  - binding_key: front
    lock_id: "11111111111111111111111111111111"
`
	if err := os.WriteFile("configs/august.yaml", []byte(defaultConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	explicit := strings.ReplaceAll(defaultConfig, "default-installation", "explicit-installation")
	explicit = strings.ReplaceAll(explicit, "default.json", "explicit.json")
	if err := os.WriteFile("explicit.yaml", []byte(explicit), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_AUGUST_ADAPTER_ID", "from-env")
	for _, args := range [][]string{
		{"august", "--config", "explicit.yaml", "--adapter-id", "from-flag", "authorize"},
		{"august", "authorize", "--config", "explicit.yaml", "--adapter-id", "from-flag"},
	} {
		called := false
		cmd := newCommand(func(context.Context, august.Config, *slog.Logger) error {
			t.Fatal("monitor called during authorization")
			return nil
		},
			func(_ context.Context, c august.Config, _ func() (string, error), _ *slog.Logger) error {
				called = true
				if c.AdapterID != "from-flag" || c.CredentialsFile != "/private/explicit.json" {
					t.Fatalf("explicit authorization configuration was shadowed: %+v", c)
				}
				return nil
			}, strings.NewReader(""), io.Discard)
		if err := cmd.Run(t.Context(), args); err != nil || !called {
			t.Fatalf("authorization args %v: %v", args, err)
		}
	}
}

type syntheticSafeError struct{ code string }

func (e syntheticSafeError) Error() string {
	return "private@example.com private-password private-code"
}
func (e syntheticSafeError) SafeCode() string { return e.code }

func TestCLIKnownFailuresAreActionableWithoutSecretOutput(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "private-config.yaml")
	data := `adapter_id: august-home
credentials_file: /private/absent-private-credentials.json
installation_id: 00000000-0000-4000-8000-000000000001
locks:
  - binding_key: front
    lock_id: "11111111111111111111111111111111"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, code, want string }{
		{"password", "auth_rejected", "auth_rejected"},
		{"code", "verification_rejected", "verification_rejected"},
		{"untrusted classification", "private-code", "run_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			cmd := newCommand(
				august.Run,
				func(context.Context, august.Config, func() (string, error), *slog.Logger) error {
					return syntheticSafeError{code: test.code}
				},
				strings.NewReader(""),
				&output,
			)
			err := cmd.Run(t.Context(), []string{"august", "authorize", "--config", path})
			if !errors.Is(err, errAdapterFailed) || !strings.Contains(output.String(), "error_code="+test.want) {
				t.Fatalf("classification missing: %v %s", err, output.String())
			}
			assertNoSecretDiagnostic(t, output.String(), path)
		})
	}
	var output bytes.Buffer
	cmd := newCommand(august.Run, august.Authorize, strings.NewReader(""), &output)
	if err := cmd.Run(
		t.Context(),
		[]string{"august", "--config", path},
	); !errors.Is(err, errAdapterFailed) ||
		!strings.Contains(output.String(), "credentials_file_unreadable") {
		t.Fatalf("credential failure was not actionable: %v %s", err, output.String())
	}
	assertNoSecretDiagnostic(t, output.String(), path)
	output.Reset()
	cmd = newCommand(august.Run, august.Authorize, strings.NewReader(""), &output)
	if err := cmd.Run(
		t.Context(),
		[]string{"august", "--config", path, "--installation-id", "private-code"},
	); !errors.Is(err, errAdapterFailed) ||
		!strings.Contains(output.String(), "installation_id must be a nonzero UUID") {
		t.Fatalf("safe validation reason missing: %v %s", err, output.String())
	}
	assertNoSecretDiagnostic(t, output.String(), path)
}

func assertNoSecretDiagnostic(t *testing.T, output, path string) {
	t.Helper()
	for _, secret := range []string{path, "/private/absent-private-credentials.json", "private@example.com", "private-password", "private-code"} {
		if strings.Contains(output, secret) {
			t.Fatal("diagnostic leaked secret input, raw error or path")
		}
	}
}
