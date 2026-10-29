package httpapi //nolint:testpackage // Tests exercise real handlers and private response mapping.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestDeviceHTTPDiagnosticsOwnUnexpectedFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, method, path, body string
		mapping, canceled        bool
		cause                    error
		wantDiagnostic           bool
		status                   int
	}{
		{"read", "GET", "/v1/entities/" + string(apiEntityID), "", false, false, errors.New("SENSITIVE-device-storage"), true, 500},
		{"mapping", "GET", "/v1/entities/" + string(apiEntityID), "", true, false, nil, true, 500},
		{"mutation", "PATCH", "/v1/devices/" + string(apiDeviceID), `{"name_override":"Synthetic"}`, false, false, errors.New("SENSITIVE-device-mutation"), true, 500},
		{"command admission", "POST", "/v1/entities/" + string(apiEntityID) + "/commands", `{"operation":"set","parameters":{"value":true}}`, false, false, errors.New("SENSITIVE-command-admission"), true, 500},
		{"caller cancellation", "GET", "/v1/entities/" + string(apiEntityID), "", false, true, context.Canceled, false, 500},
		{"dependency cancellation", "GET", "/v1/entities/" + string(apiEntityID), "", false, false, context.Canceled, true, 500},
		{"joined fault", "GET", "/v1/entities/" + string(apiEntityID), "", false, true, errors.Join(context.Canceled, errors.New("SENSITIVE-storage")), true, 500},
		{"not found", "GET", "/v1/entities/" + string(apiEntityID), "", false, false, devices.ErrEntityNotFound, false, 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			capture := &deviceDiagnosticContextHandler{Handler: slog.NewJSONHandler(&logs, nil), expected: ctx}
			stub := &stubDevices{
				patchDevice: func(context.Context, devices.DeviceID, devices.DevicePatch) (devices.Device, error) {
					return devices.Device{}, test.cause
				},
				getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
					if test.mapping {
						return apiEntityWithState(&devices.State{Value: devices.Value(`SENSITIVE-invalid-json`)}), nil
					}
					return devices.EntityWithState{}, test.cause
				},
				executeCommand: func(context.Context, devices.CommandInput) (devices.CommandResult, error) {
					return devices.CommandResult{}, test.cause
				},
			}
			router, _ := testAPIWithLogger(t, stub, slog.New(capture))
			if test.canceled {
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			if test.method == http.MethodPatch {
				request.Header.Set("Content-Type", "application/merge-patch+json")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String()+fmt.Sprint(response.Header())+logs.String(), "SENSITIVE") {
				t.Fatal("private data leaked")
			}
			operation, stage := deviceDiagnosticLocation(test.name)
			assertDeviceDiagnosticEvidence(t, logs.Bytes(), capture.matched, test.wantDiagnostic, operation, stage)
		})
	}
}

func deviceDiagnosticLocation(name string) (string, string) {
	switch name {
	case "mapping":
		return "get-entity", "mapping"
	case "mutation":
		return "update-device", "service"
	case "command admission":
		return "execute-entity-command", "service"
	default:
		return "get-entity", "service"
	}
}

func assertDeviceDiagnosticEvidence(t *testing.T, raw []byte, matched, want bool, operation, stage string) {
	t.Helper()
	if !want {
		if len(raw) != 0 {
			t.Errorf("modeled failure or expected cancellation logged: %s", raw)
		}
		return
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Errorf("want exactly one safe failure diagnostic: %v", err)
	} else if record["level"] != "ERROR" || record["error_code"] != "internal_error" || !matched ||
		record["event"] != "device.http_internal_failure" || record["operation"] != operation || record["stage"] != stage ||
		record["error_type"] == "" || len(record) != 8 {
		t.Errorf("diagnostic lost fixed classification or request context: %v", record)
	}
}

type deviceDiagnosticContextHandler struct {
	slog.Handler

	expected context.Context
	matched  bool
}

func (handler *deviceDiagnosticContextHandler) Handle(ctx context.Context, record slog.Record) error {
	handler.matched = ctx == handler.expected
	return handler.Handler.Handle(ctx, record)
}
