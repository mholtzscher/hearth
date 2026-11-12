package august

import (
	"context"
	"log/slog"
	"net/http"

	provider "github.com/mholtzscher/hearth/internal/adapters/august"
)

// Authorize explicitly provisions the stable installation without connecting to Core.
func Authorize(ctx context.Context, config Config, codeReader func() (string, error), _ *slog.Logger) error {
	return authorize(ctx, config, codeReader, nil)
}

func authorize(
	ctx context.Context,
	config Config,
	codeReader func() (string, error),
	transport http.RoundTripper,
) error {
	if err := config.Validate(); err != nil {
		return err
	}
	credentials, err := readCredentials(config.CredentialsFile)
	if err != nil {
		return err
	}
	runtimeConfig, err := config.runtimeConfig()
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return provider.Authorize(ctx, runtimeConfig, credentials, codeReader, transport)
}
