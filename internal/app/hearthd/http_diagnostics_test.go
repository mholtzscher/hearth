package hearthd //nolint:testpackage // Verifies production HTTP logger assembly.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestHTTPAssemblyInjectsOneComponentScopedDiagnostic(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil)).With(slog.String("app", "hearthd"))
	service := &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, errors.New("SENSITIVE-query-body")
	}}
	handler, _ := newHTTPHandler(logger, service, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		mcpapi.New(mcpapi.Config{Name: mcpName, Version: mcpVersion}))
	response := appRequest(handler, "/v1/entities/"+string(testHTTPEntityID))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var record map[string]any
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatalf("want one diagnostic: %v", err)
	}
	if record["app"] != "hearthd" || record["component"] != "devices" ||
		record["event"] != "device.http_internal_failure" {
		t.Fatalf("assembly diagnostic=%v", record)
	}
	if strings.Count(logs.String(), `"component"`) != 1 {
		t.Fatal("assembly duplicated component fields")
	}
	if strings.Contains(logs.String()+response.Body.String(), "SENSITIVE") {
		t.Fatal("assembly exposed private cause")
	}
}
