// Package musicassistant assembles the Music Assistant companion process.
package musicassistant

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	ma "github.com/mholtzscher/hearth/internal/adapters/musicassistant"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const maxTokenFile = 4096

// Config holds the runtime addresses and mounted secret.
type Config struct {
	AdapterID string `yaml:"adapter_id"`
	NATSURL   string `yaml:"nats_url"`
	ServerURL string `yaml:"server_url"`
	TokenFile string `yaml:"token_file"`
}

// Run serves the playback Function for one adapter Session.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := platformconfig.ValidateSlug("adapter_id", config.AdapterID); err != nil {
		return err
	}
	if err := platformconfig.ValidateNATSURL(config.NATSURL); err != nil {
		return err
	}
	token, err := readToken(config.TokenFile)
	if err != nil {
		return err
	}
	client, err := ma.New(config.ServerURL, token, nil)
	if err != nil {
		return err
	}
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: config.AdapterID, SoftwareName: "hearth-adapter-music-assistant",
		SoftwareVersion: "0.1.0", NATSURL: config.NATSURL, Logger: logger,
	})
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	server, err := session.StartFunctionServer(ctx, client.Functions())
	if err != nil {
		return err
	}
	defer func() { _ = server.Close(context.WithoutCancel(ctx)) }()
	if err = session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	return session.Wait(ctx)
}

func readToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("token_file must reference a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("token_file could not be read")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTokenFile+1))
	if err != nil || len(data) > maxTokenFile {
		return "", errors.New("token_file is unreadable or oversized")
	}
	return strings.TrimSpace(string(data)), nil
}
