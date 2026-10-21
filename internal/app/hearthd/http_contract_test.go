package hearthd //nolint:testpackage // Pins the assembled HTTP contract across transport moves.

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestHTTPTransportContractSnapshot(t *testing.T) {
	t.Parallel()
	handler, _ := newHTTPHandler(
		&stubDevices{}, &stubAutomations{}, &stubNotifications{}, &stubDevices{}, &stubFunctions{},
		&stubAgent{}, &testReadiness{}, &stubDevices{}, &stubAutomations{}, &stubFunctions{},
		mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion}),
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", response.Code)
	}
	// Includes paths, wire schemas, component names, references and publication
	// metadata. A protocol package move must not change this public contract.
	got := fmt.Sprintf("%x", sha256.Sum256(response.Body.Bytes()))
	const want = "9e6c02f585374faae86b20f3d2e6b415d626a89fef3832205e106761e67b72ca"
	if got != want {
		t.Fatalf("HTTP/OpenAPI contract changed: SHA-256 = %s, want %s", got, want)
	}
}
