package zigbee2mqtt //nolint:testpackage // Startup seam tests replace package-private loader and connection seams.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	zigbee2mqttadapter "github.com/mholtzscher/hearth/internal/adapters/zigbee2mqtt"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects catalog-first startup and fails if a catalog load
// failure reaches the NATS session connection or the failure loses its
// load_profile_catalog stage. The defect would be a startup path that dials
// external systems with an uncompiled profile catalog or misreports the
// failure stage to the executable.
//
//nolint:paralleltest // This test replaces the process-wide startup seams and must run sequentially.
func TestRunProfileCatalogFailureMakesNoConnectionAttempt(t *testing.T) {
	previousLoader := loadEmbeddedProfileCatalog
	previousConnect := connectAdapterSession
	t.Cleanup(func() {
		loadEmbeddedProfileCatalog = previousLoader
		connectAdapterSession = previousConnect
	})

	rawValue := "SECRET-RAW-PROFILE-VALUE-7f3a"
	loadEmbeddedProfileCatalog = func() (*zigbee2mqttadapter.ProfileCatalog, error) {
		return nil, &zigbee2mqttadapter.ProfileCatalogError{
			Code:        "schema_invalid",
			Document:    "profiles/light.profile.json",
			ProfileID:   "light",
			RuleID:      "light.power",
			JSONPointer: "/candidate_groups/0/entities/0",
			Err:         errors.New("strategy parameters carry " + rawValue),
		}
	}
	var connectCalls int
	connectAdapterSession = func(context.Context, adapter.Config) (*adapter.Session, error) {
		connectCalls++
		return nil, errors.New("unexpected NATS connection attempt")
	}

	handler := &startupLogHandler{}
	runErr := Run(context.Background(), validStartupConfig(), slog.New(handler))
	if runErr == nil {
		t.Fatal("Run with failing catalog loader succeeded, want load_profile_catalog failure")
	}
	if stage := ErrorStage(runErr); stage != StageLoadProfileCatalog {
		t.Fatalf("Run failure stage = %q, want %q", stage, StageLoadProfileCatalog)
	}
	if code := ErrorCode(runErr); code != ErrorCodeProfileCatalogInvalid {
		t.Fatalf("Run failure code = %q, want %q", code, ErrorCodeProfileCatalogInvalid)
	}
	if connectCalls != 0 {
		t.Fatalf("Run made %d NATS connection attempts after catalog failure, want zero", connectCalls)
	}

	diagnostic, found := handler.find("adapter.profile_catalog_failed")
	if !found {
		t.Fatal("missing adapter.profile_catalog_failed diagnostic")
	}
	for field, want := range map[string]string{
		"error_code":   "schema_invalid",
		"profile_id":   "light",
		"rule_id":      "light.power",
		"json_pointer": "/candidate_groups/0/entities/0",
	} {
		if value, ok := diagnostic.attrs[field].(string); !ok || value != want {
			t.Fatalf("diagnostic field %q = %#v, want %q", field, diagnostic.attrs[field], want)
		}
	}
	for key, value := range diagnostic.attrs {
		if text, ok := value.(string); ok && strings.Contains(text, rawValue) {
			t.Fatalf("diagnostic field %q repeats raw profile contents", key)
		}
	}
}

// This test protects startup ordering and fails if the embedded catalog does
// not compile before the NATS session connection. The defect would be an
// adapter that connects to external systems before its profiles are known to
// be valid.
//
//nolint:paralleltest // This test replaces the process-wide startup seams and must run sequentially.
func TestRunLoadsCatalogBeforeConnecting(t *testing.T) {
	previousLoader := loadEmbeddedProfileCatalog
	previousConnect := connectAdapterSession
	t.Cleanup(func() {
		loadEmbeddedProfileCatalog = previousLoader
		connectAdapterSession = previousConnect
	})

	var mu sync.Mutex
	var order []string
	track := func(stage string) {
		mu.Lock()
		order = append(order, stage)
		mu.Unlock()
	}
	loadEmbeddedProfileCatalog = func() (*zigbee2mqttadapter.ProfileCatalog, error) {
		track("load_profile_catalog")
		return zigbee2mqttadapter.LoadEmbeddedProfileCatalog()
	}
	connectErr := errors.New("startup connection refused")
	connectAdapterSession = func(context.Context, adapter.Config) (*adapter.Session, error) {
		track("connect_session")
		return nil, connectErr
	}

	runErr := Run(context.Background(), validStartupConfig(), slog.New(slog.DiscardHandler))
	if !errors.Is(runErr, connectErr) {
		t.Fatalf("Run error = %v, want the stubbed connection failure after a successful catalog load", runErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "load_profile_catalog" || order[1] != "connect_session" {
		t.Fatalf("startup order = %v, want [load_profile_catalog connect_session]", order)
	}
}

// This test protects staged failure reporting and fails if a catalog stage is
// lost through wrapping or an unclassified error gains a catalog code. The
// oracle is the hearthd runStageError pattern: wrapped stages survive and
// only the catalog stage maps to profile_catalog_invalid.
func TestErrorStageAndCodeClassifyStartupFailures(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []struct {
		name  string
		err   error
		stage string
		code  string
	}{
		{
			name:  "catalog stage",
			err:   failStage(StageLoadProfileCatalog, boom),
			stage: StageLoadProfileCatalog,
			code:  ErrorCodeProfileCatalogInvalid,
		},
		{
			name:  "wrapped catalog stage",
			err:   fmt.Errorf("startup: %w", failStage(StageLoadProfileCatalog, boom)),
			stage: StageLoadProfileCatalog,
			code:  ErrorCodeProfileCatalogInvalid,
		},
		{
			name:  "other stage keeps run code",
			err:   failStage("connect_nats", boom),
			stage: "connect_nats",
			code:  ErrorCodeRunFailed,
		},
		{
			name:  "unstaged error",
			err:   boom,
			stage: "run",
			code:  ErrorCodeRunFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if stage := ErrorStage(test.err); stage != test.stage {
				t.Fatalf("ErrorStage = %q, want %q", stage, test.stage)
			}
			if code := ErrorCode(test.err); code != test.code {
				t.Fatalf("ErrorCode = %q, want %q", code, test.code)
			}
		})
	}
	if err := failStage("load_profile_catalog", nil); err != nil {
		t.Fatalf("failStage with nil error = %v, want nil", err)
	}
}

func validStartupConfig() Config {
	return Config{
		AdapterID: "zigbee2mqtt-test",
		NATSURL:   "nats://127.0.0.1:4222",
		MQTT: MQTTConfig{
			URL: "mqtt://127.0.0.1:1883", BaseTopic: "zigbee2mqtt",
		},
	}
}

// startupLogHandler captures records for startup tests that must assert safe
// failure evidence without the integration helpers.
type startupLogHandler struct {
	mu      sync.Mutex
	records []startupLogRecord
}

type startupLogRecord struct {
	message string
	attrs   map[string]any
}

func (handler *startupLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *startupLogHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	handler.mu.Lock()
	handler.records = append(handler.records, startupLogRecord{message: record.Message, attrs: attrs})
	handler.mu.Unlock()
	return nil
}

func (handler *startupLogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler *startupLogHandler) WithGroup(string) slog.Handler { return handler }

func (handler *startupLogHandler) find(event string) (startupLogRecord, bool) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	for _, record := range handler.records {
		if record.attrs["event"] == event {
			return record, true
		}
	}
	return startupLogRecord{}, false
}
