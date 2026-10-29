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
		nil,
		&stubDevices{}, &stubAutomations{}, &stubNotifications{}, &stubDevices{}, &stubFunctions{},
		&stubAgent{}, &testReadiness{}, &stubDevices{}, &stubAutomations{}, &stubFunctions{},
		mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion}),
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", response.Code)
	}
	// Includes paths, wire schemas, component names, references and publication
	// metadata. Intentional API extensions update this digest alongside contract tests.
	got := fmt.Sprintf("%x", sha256.Sum256(response.Body.Bytes()))
	const want = "0381efc9f163af47982fd216c2aa5a8d02a0f7545a52da3300eaa60a4e7bbafe"
	if got != want {
		t.Fatalf("HTTP/OpenAPI contract changed: SHA-256 = %s, want %s", got, want)
	}
}
