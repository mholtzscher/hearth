package hearthd //nolint:testpackage // Pins the assembled HTTP contract across transport moves.

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestHTTPTransportContractSnapshot(t *testing.T) {
	t.Parallel()
	handler, _ := newHTTPHandler(
		nil,
		&stubDevices{}, &stubAutomations{}, &stubNotifications{}, &stubDevices{}, &stubFunctions{},
		&stubAgent{}, &testReadiness{}, &stubDevices{}, &stubAutomations{}, &stubFunctions{},
		mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion}), dashboards.NewService(nil, &stubDevices{}),
	)
	response := appRequest(handler, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", response.Code)
	}
	// Includes paths, wire schemas, component names, references and publication
	// metadata. Intentional API extensions update this digest alongside contract tests.
	got := fmt.Sprintf("%x", sha256.Sum256(response.Body.Bytes()))
	const want = "1aaa6207da0c896276d8e17db7ed32c90f3884bae9700fae3f8d5c8262ed74e5"
	if got != want {
		t.Fatalf("HTTP/OpenAPI contract changed: SHA-256 = %s, want %s", got, want)
	}
}
