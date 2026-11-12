package main

import (
	"bytes"
	"context"
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
