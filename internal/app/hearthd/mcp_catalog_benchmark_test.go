package hearthd //nolint:testpackage // Benchmarks the package-private production catalog assembly.

import (
	"runtime"
	"testing"
)

// BenchmarkMCPCatalogConstruction measures binding the complete catalog to fresh
// services, including each server's contract preparation but excluding Huma
// assembly and MCP sessions. Production contracts are not cached across servers.
func BenchmarkMCPCatalogConstruction(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		server := newMCPServer(
			&stubDevices{},
			&stubAutomations{},
			&stubNotifications{},
			&stubDevices{},
			&stubFunctions{},
			nil,
		)
		runtime.KeepAlive(server)
	}
}
