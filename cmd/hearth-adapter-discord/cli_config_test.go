package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/app/discord"
)

func TestCommandPrecedenceAndYAMLOnlyRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(
		path,
		[]byte(
			"adapter_id: yaml-id\nnats_url: nats://yaml:4222\ndestinations:\n  home:\n    name: Household\n    webhook_url_file: /secret\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEARTH_ADAPTER_DISCORD_CONFIG", path)
	t.Setenv("HEARTH_ADAPTER_DISCORD_ADAPTER_ID", "env-id")
	t.Setenv("HEARTH_ADAPTER_DISCORD_NATS_URL", "nats://env:4222")
	var got discord.Config
	var output bytes.Buffer
	command := newCommand(
		func(_ context.Context, config discord.Config, _ *slog.Logger) error { got = config; return nil },
		&output,
	)
	if err := command.Run(t.Context(), []string{"hearth-adapter-discord", "--adapter-id", "flag-id"}); err != nil {
		t.Fatal(err)
	}
	if got.AdapterID != "flag-id" || got.NATSURL != "nats://env:4222" || len(got.Destinations) != 1 ||
		got.Destinations["home"].WebhookURLFile != "/secret" {
		t.Fatalf("resolved=%+v", got)
	}
	if strings.Contains(output.String(), "/secret") {
		t.Fatal("secret path leaked")
	}
}

func TestMalformedExplicitConfigFailsEvenWithOverrides(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"adapter_id: [", "adapter_id: one\nadapter_id: two\n", "adapter_id: one\n---\nadapter_id: two\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		called := false
		command := newCommand(
			func(context.Context, discord.Config, *slog.Logger) error { called = true; return nil },
			&bytes.Buffer{},
		)
		if err := command.Run(
			t.Context(),
			[]string{"hearth-adapter-discord", "--config", path, "--adapter-id", "flag-id"},
		); err == nil ||
			called {
			t.Fatalf("malformed config reached assembly: %v", err)
		}
	}
}
