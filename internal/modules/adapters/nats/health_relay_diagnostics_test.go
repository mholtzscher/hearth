package nats //nolint:testpackage // The module owns delivery-error presentation, not relay policy.

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/platform/outbox"
)

func TestHealthRelayDiagnosticsRetainDeliveryClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		stage, code string
		permanent   bool
	}{
		{"list", "list_failed", false},
		{"list", "invalid_row", true},
		{"map", "invalid_message", true},
		{"publish", "publish_failed", false},
		{"ack", "ack_missing", false},
		{"ack", "unexpected_stream", false},
		{"delete", "delete_failed", false},
	} {
		t.Run(test.stage+"/"+test.code, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			source := healthPublicationSource{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			// A poison row may contain malformed or private fields. Do not log it.
			row := adapters.PendingHealthUpdate{
				ID:         "SENSITIVE-outbox",
				Snapshot:   adapters.HealthSnapshot{AdapterID: "SENSITIVE-adapter"},
				TraceState: "SENSITIVE-trace",
			}
			source.report(t.Context(), outbox.DeliveryError[adapters.PendingHealthUpdate]{
				Stage: test.stage, Code: test.code, Permanent: test.permanent,
				Item: &row, Cause: errors.New("SENSITIVE-cause subject body"),
			})
			var record map[string]any
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			event, level := "adapter_health.retry", "WARN"
			if test.permanent {
				event, level = "adapter_health.poison", "ERROR"
			}
			if record["event"] != event || record["level"] != level || record["stage"] != test.stage ||
				record["error_code"] != test.code ||
				len(record) != 6 {
				t.Errorf("lost fixed delivery classification: %s", logs.String())
			}
			if strings.Contains(logs.String(), "SENSITIVE") {
				t.Fatal("private cause or outbox data logged")
			}
		})
	}
}
