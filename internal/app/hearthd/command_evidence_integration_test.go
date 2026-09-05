package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
)

// This test protects the Happy Command evidence chain and fails if core
// creation, SDK receipt/acceptance, the command-linked Observation publication
// (Debug), or the single satisfied core outcome disagree on IDs, if acceptance
// is logged twice, or if the outcome disagrees with persisted history.
func TestHappyCommandEndToEndLogEvidence(t *testing.T) {
	t.Parallel()
	harness := newSimulatorMatrixHarness(t, simulatoradapter.ScenarioHappy, simulatorMatrixOptions{
		logLevel: slog.LevelDebug,
	})
	harness.waitForState(t)

	status, body, err := harness.postCommand(harness.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 {
		t.Fatalf("command status = %d, body = %s", status, body)
	}
	var result devicesapi.CommandResultBody
	if decodeErr := json.Unmarshal(body, &result); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if result.CommandID == "" || result.ObservationID == "" {
		t.Fatalf("command result omitted IDs: %#v", result)
	}
	commandID, err := devices.ParseCommandID(result.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := harness.repository.GetCommand(harness.ctx, commandID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != devices.CommandStatusSatisfied {
		t.Fatalf("stored command status = %q, want satisfied", stored.Status)
	}

	wantCommandID := string(commandID)
	wantCorrelationID := string(stored.CorrelationID)
	wantObservationID := result.ObservationID
	records := waitForMatrixLogEvent(t, harness, "command.completed", wantCommandID, 5*time.Second)

	created := requireSingleMatrixCommandEvent(t, records, "command.created", wantCommandID)
	requireMatrixAttr(t, created, "correlation_id", wantCorrelationID)
	requireMatrixAttr(t, created, "entity_id", string(harness.entityID))
	requireMatrixAttr(t, created, "adapter_id", simulatorMatrixAdapterID)
	requireMatrixAttr(t, created, "operation", "set")
	requireMatrixAttr(t, created, "component", "devices")
	requireMatrixLevel(t, created, "INFO")

	received := requireSingleMatrixCommandEvent(t, records, "command.received", wantCommandID)
	requireMatrixAttr(t, received, "correlation_id", wantCorrelationID)
	requireMatrixAttr(t, received, "entity_id", string(harness.entityID))
	requireMatrixAttr(t, received, "component", "adapter_session")

	// Acceptance has exactly one owner: the SDK. A second core acceptance for
	// the same command would double-report terminal progress.
	accepted := requireSingleMatrixCommandEvent(t, records, "command.accepted", wantCommandID)
	requireMatrixAttr(t, accepted, "correlation_id", wantCorrelationID)
	requireMatrixAttr(t, accepted, "entity_id", string(harness.entityID))
	requireMatrixAttr(t, accepted, "component", "adapter_session")

	published := matrixEventsForCommand(records, "observation.published", wantCommandID)
	if len(published) == 0 {
		t.Fatal("missing command-linked observation.published")
	}
	requireMatrixAttr(t, published[0], "correlation_id", wantCorrelationID)
	requireMatrixAttr(t, published[0], "observation_id", wantObservationID)
	requireMatrixAttr(t, published[0], "entity_id", string(harness.entityID))
	requireMatrixLevel(t, published[0], "DEBUG")

	projected := matrixEventsForCommand(records, "observation.projected", wantCommandID)
	if len(projected) == 0 {
		t.Fatal("missing command-linked observation.projected")
	}
	requireMatrixAttr(t, projected[0], "correlation_id", wantCorrelationID)
	requireMatrixLevel(t, projected[0], "DEBUG")

	completed := requireSingleMatrixCommandEvent(t, records, "command.completed", wantCommandID)
	requireMatrixAttr(t, completed, "correlation_id", wantCorrelationID)
	requireMatrixAttr(t, completed, "entity_id", string(harness.entityID))
	requireMatrixAttr(t, completed, "status", string(devices.CommandStatusSatisfied))
	requireMatrixAttr(t, completed, "observation_id", wantObservationID)
	requireMatrixAttr(t, completed, "component", "devices")
	requireMatrixLevel(t, completed, "INFO")
	if duration, ok := completed["duration_ms"].(float64); !ok || duration < 0 {
		t.Fatalf("command.completed duration_ms = %#v, want nonnegative", completed["duration_ms"])
	}
	if stored.OutcomeObservationID == nil || string(*stored.OutcomeObservationID) != wantObservationID {
		t.Fatalf("persisted outcome Observation = %#v, want %s", stored.OutcomeObservationID, wantObservationID)
	}
}

// waitForMatrixLogEvent polls the thread-safe harness log buffer until a
// record with the wanted event and command ID is captured, then returns the
// full decoded snapshot so counts stay exact.
func waitForMatrixLogEvent(
	t *testing.T,
	harness *simulatorMatrixHarness,
	event string,
	commandID string,
	timeout time.Duration,
) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		records := decodeMatrixLogRecords(t, harness.logs.String())
		if len(matrixEventsForCommand(records, event, commandID)) > 0 {
			return records
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log event %q for command %s", event, commandID)
	return nil
}

// decodeMatrixLogRecords parses one JSON record per line, skipping a trailing
// partial line left by a concurrent write to the locked harness buffer.
func decodeMatrixLogRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records
}

func matrixEventsForCommand(
	records []map[string]any,
	event string,
	commandID string,
) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["event"] != event {
			continue
		}
		if value, ok := record["command_id"].(string); ok && value == commandID {
			matched = append(matched, record)
		}
	}
	return matched
}

func requireSingleMatrixCommandEvent(
	t *testing.T,
	records []map[string]any,
	event string,
	commandID string,
) map[string]any {
	t.Helper()
	matched := matrixEventsForCommand(records, event, commandID)
	if len(matched) != 1 {
		t.Fatalf("%s records for command %s = %d, want exactly one", event, commandID, len(matched))
	}
	return matched[0]
}

func requireMatrixAttr(t *testing.T, record map[string]any, key string, want string) {
	t.Helper()
	value, ok := record[key].(string)
	if !ok || value != want {
		t.Fatalf("log event %v attr %q = %#v, want %q", record["event"], key, record[key], want)
	}
}

func requireMatrixLevel(t *testing.T, record map[string]any, want string) {
	t.Helper()
	value, ok := record["level"].(string)
	if !ok || value != want {
		t.Fatalf("log event %v level = %#v, want %s", record["event"], record["level"], want)
	}
}
