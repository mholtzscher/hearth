package httpapi //nolint:testpackage // Exercises the real Command service through HTTP.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestCommandHTTPKeepsWorkerFailureDiagnosticOwner(t *testing.T) {
	t.Parallel()
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	capture := &commandDiagnosticCapture{}
	logger := slog.New(capture)
	store := &commandDiagnosticStore{}
	service := devices.NewService(
		devices.Stores{Reads: store, Commands: store},
		nil,
		catalog,
		devices.Dependencies{Logger: logger},
	)
	router, _ := testAPIWithLogger(t, service, logger)
	path := "/v1/entities/" + string(apiEntityID) + "/commands"
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		path,
		strings.NewReader(`{"operation":"set","parameters":{"value":true}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "SENSITIVE") {
		t.Fatal("response leaked worker fault")
	}
	if err = service.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	if len(capture.failures) != 1 || capture.failures[0] != "command.execution_failed" {
		t.Fatalf("Error events=%v, want exactly one worker failure and no HTTP duplicate", capture.failures)
	}
}

type commandDiagnosticStore struct {
	devices.ReadRepository
	devices.CommandLedger
}

func (*commandDiagnosticStore) GetEntity(context.Context, devices.EntityID) (devices.EntityWithState, error) {
	return apiEntityWithState(nil), nil
}

func (*commandDiagnosticStore) CreateCommand(
	_ context.Context,
	command devices.CommandRecord,
) (devices.CommandRecord, error) {
	runtimeID := adapters.RuntimeID("art_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	command.RuntimeID = &runtimeID
	return command, nil
}
func (*commandDiagnosticStore) CompleteCommand(context.Context, devices.CommandCompletion) error {
	return errors.New("SENSITIVE-worker-persistence")
}

// The worker may emit its creation record after releasing admission. Capture
// safely without imposing any diagnostic ordering on the production worker.
type commandDiagnosticCapture struct {
	mutex    sync.Mutex
	failures []string
}

func (*commandDiagnosticCapture) Enabled(context.Context, slog.Level) bool { return true }
func (capture *commandDiagnosticCapture) Handle(_ context.Context, record slog.Record) error {
	if record.Level != slog.LevelError {
		return nil
	}
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "event" {
			capture.failures = append(capture.failures, attr.Value.String())
		}
		return true
	})
	return nil
}
func (capture *commandDiagnosticCapture) WithAttrs([]slog.Attr) slog.Handler { return capture }
func (capture *commandDiagnosticCapture) WithGroup(string) slog.Handler      { return capture }
