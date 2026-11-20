//go:build integration

package homekit //nolint:testpackage // Tests the Session owner with downstream bridge startup isolated.

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestRunClaimsAndReleasesSession(t *testing.T) {
	t.Parallel()
	broker := natstest.StartServer(t)
	conn, err := natsgo.Connect(broker.ClientURL())
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	validator, err := contractsv1.Compile()
	require.NoError(t, err)
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "core.db"))
	repository := adapterssqlite.NewAdapterRepository(database)
	service := adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
	sessions, err := adaptersnats.StartSessionServer(conn, validator, service, service, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sessions.Close(context.Background())) })

	config := Config{
		AdapterID: "homekit-test", NATSURL: new(broker.ClientURL()),
		CoreURL: "http://127.0.0.1:8080", DataDir: t.TempDir(),
	}
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = runSession(ctx, config, slog.New(slog.DiscardHandler),
			func(ctx context.Context, _ Config, _ *adapter.Session, _ *slog.Logger) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Adapter Run did not join")
		}
	}()

	online := awaitClaim(t, service, config.AdapterID)
	require.Equal(t, adapters.RuntimeStatusOnline, online.Health.Runtime.Status)
	assert.Equal(t, SoftwareName, online.Health.Runtime.SoftwareName)
	assert.Equal(t, adapters.AdapterHealthUnknown, online.Health.Status)

	// A persisted claim can be visible before Connect receives its reply.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Adapter Run did not finish claiming the Session")
	}
	cancel()
	select {
	case <-done:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("Adapter Run did not release the Session")
	}
	offline, err := service.GetAdapter(t.Context(), config.AdapterID)
	require.NoError(t, err)
	require.NotNil(t, offline.Health.Runtime)
	assert.Equal(t, online.Health.Runtime.ID, offline.Health.Runtime.ID)
	assert.Equal(t, adapters.RuntimeStatusOffline, offline.Health.Runtime.Status)
}

func awaitClaim(t *testing.T, service *adapters.Service, id string) adapters.AdapterInstance {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		instance, err := service.GetAdapter(ctx, id)
		if err == nil && instance.Health.Runtime != nil {
			return instance
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Core did not record the Adapter Session: %v", err)
		case <-ticker.C:
		}
	}
}
