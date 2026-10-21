package hearthd //nolint:testpackage // Benchmarks the package-private production catalog assembly.

import (
	"runtime"
	"testing"
)

// BenchmarkMCPCatalogConstruction measures binding the complete catalog to fresh
// services, excluding Huma assembly and MCP sessions. Preparation is warmed before
// timing; cold assembly is measured separately by the integration catalog test.
func BenchmarkMCPCatalogConstruction(b *testing.B) {
	runtime.KeepAlive(
		newMCPServer(&stubDevices{}, &stubAutomations{}, &stubNotifications{}, &stubDevices{}, &stubFunctions{}, nil),
	)
	b.ReportAllocs()
	b.ResetTimer()
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
