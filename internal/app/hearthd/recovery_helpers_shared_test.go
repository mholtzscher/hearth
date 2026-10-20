//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"net"
	"testing"
)

func reserveLoopbackListener(t *testing.T) (string, runOptions) {
	t.Helper()
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().String(), runOptions{httpListener: listener}
}
