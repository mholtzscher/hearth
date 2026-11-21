package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/mholtzscher/hearth/internal/app/musicassistant"
	platformconfig "github.com/mholtzscher/hearth/internal/platform/config"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

const executable = "hearth-adapter-music-assistant"

func main() { os.Exit(run()) }

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := command().Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func command() *cli.Command {
	return &cli.Command{
		Name: executable, Writer: os.Stderr, ErrWriter: os.Stderr,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "config", Value: "configs/music-assistant.yaml",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_MUSIC_ASSISTANT_CONFIG")),
			},
			&cli.StringFlag{
				Name: "log-level", Value: "info",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_MUSIC_ASSISTANT_LOG_LEVEL")),
			},
			&cli.StringFlag{
				Name: "log-format", Value: "text",
				Sources: cli.NewValueSourceChain(cli.EnvVar("HEARTH_ADAPTER_MUSIC_ASSISTANT_LOG_FORMAT")),
			},
		},
		Action: start,
	}
}

func start(ctx context.Context, cmd *cli.Command) error {
	logger, err := logging.NewApplicationLogger(
		os.Stderr, executable, logging.LogOptions{Level: cmd.String("log-level"), Format: cmd.String("log-format")},
	)
	if err != nil {
		return err
	}
	config, err := platformconfig.LoadYAML[musicassistant.Config](cmd.String("config"), true)
	if err != nil {
		return err
	}
	if err = musicassistant.Run(ctx, config, logger); err != nil && ctx.Err() == nil {
		logger.ErrorContext(ctx, "Music Assistant adapter stopped", slog.String("event", "process.failed"))
		return errors.New("hearth-adapter-music-assistant failed; check the adapter configuration and NATS connection")
	}
	return nil
}
