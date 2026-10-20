//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"testing"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

func startLifecycleNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	return natstest.StartServer(t)
}
