package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/levoit"
)

func TestCLIConfigPrecedenceAndSecretSafety(t *testing.T) {
	path := filepath.Join(t.TempDir(), "levoit.yaml")
	data := "adapter_id: from-file\nnats_url: nats://127.0.0.1:4222\ncredentials_file: /private/credentials.json\npoll_interval: 90s\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_LEVOIT_ADAPTER_ID", "from-env")
	var got levoit.Config
	var output bytes.Buffer
	command := newCommand(
		func(_ context.Context, config levoit.Config, _ *slog.Logger) error { got = config; return nil },
		&output,
	)
	if err := command.Run(
		t.Context(),
		[]string{"levoit", "--config", path, "--adapter-id", "from-flag", "--poll-interval", "2m"},
	); err != nil {
		t.Fatal(err)
	}
	if got.AdapterID != "from-flag" || got.PollInterval == nil || *got.PollInterval != 2*time.Minute ||
		got.CredentialsFile != "/private/credentials.json" {
		t.Fatalf("resolved config = %+v", got)
	}
	if strings.Contains(output.String(), "/private/credentials.json") {
		t.Fatal("logs contain credential path")
	}
	if err := command.Run(t.Context(), []string{"levoit", "--password", "do-not-print"}); err == nil {
		t.Fatal("accepted credential flag")
	}
}
