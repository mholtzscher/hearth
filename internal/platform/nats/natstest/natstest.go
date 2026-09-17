// Package natstest starts isolated JetStream servers for tests. Production
// runs file-backed JetStream, and tests must too: the stream contracts
// require FileStorage, and omitting StoreDir does not give an in-memory
// server. The broker falls back to a shared default directory under [os.TempDir]
// that is repeatable across restarts, so StoreDir-less servers from parallel
// tests recover and fight over each other's streams. Every helper here
// therefore uses a fresh per-test directory; only the directory setup is shared.
package natstest

import (
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
)

// serverReadyTimeout bounds embedded-broker startup; the suite starts dozens
// of loopback servers in parallel under -race, so this must tolerate a
// heavily loaded host without masking a broker that never comes up.
const serverReadyTimeout = 10 * time.Second

// StartServer starts an isolated file-backed JetStream server on loopback
// and registers its shutdown with the test's cleanup. The store lives in a
// fresh per-test directory because the broker's StoreDir-less default is a
// shared repeatable path, not memory. Callers that manage their own server
// lifecycle may shut it down early; Shutdown is idempotent.
func StartServer(t *testing.T) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(serverReadyTimeout) {
		server.Shutdown()
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}
