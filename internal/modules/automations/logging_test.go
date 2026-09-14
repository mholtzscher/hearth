package automations_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// lockedAutomationLogWriter allows assertions while Run workers log concurrently.
type lockedAutomationLogWriter struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (writer *lockedAutomationLogWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *lockedAutomationLogWriter) output() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.String()
}

func (writer *lockedAutomationLogWriter) records(t *testing.T) []map[string]any {
	t.Helper()
	var parsed []map[string]any
	for line := range strings.SplitSeq(writer.output(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("automation log line is not JSON: %v\n%s", err, line)
		}
		parsed = append(parsed, record)
	}
	return parsed
}

func newAutomationLogSink() (*lockedAutomationLogWriter, *slog.Logger) {
	writer := &lockedAutomationLogWriter{}
	return writer, slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func automationLogEvents(records []map[string]any, event string) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["event"] == event {
			matched = append(matched, record)
		}
	}
	return matched
}

func requireAutomationLogField(t *testing.T, record map[string]any, field, want string) {
	t.Helper()
	got, ok := record[field].(string)
	if !ok || got != want {
		t.Fatalf("log field %q = %#v, want %q (record = %#v)", field, record[field], want, record)
	}
}

func requireAutomationLogInt(t *testing.T, record map[string]any, field string, want int64) {
	t.Helper()
	got, ok := record[field].(float64)
	if !ok || int64(got) != want {
		t.Fatalf("log field %q = %#v, want %d (record = %#v)", field, record[field], want, record)
	}
}

// fixedAutomationLogID builds a canonical UUIDv7 from a deterministic ordinal.
func fixedAutomationLogID(prefix string, ordinal int64) string {
	return fmt.Sprintf("%s_018f9c1e-0000-7000-8000-%012d", prefix, ordinal)
}

// fixedAutomationLogDependencies fixes time and identities for log assertions.
func fixedAutomationLogDependencies(logger *slog.Logger) automations.AutomationDependencies {
	var automationOrdinal, runOrdinal, skipOrdinal, commandOrdinal, correlationOrdinal atomic.Int64
	return automations.AutomationDependencies{
		Logger: logger,
		Now:    func() time.Time { return runtimeTestNow },
		NewAutomationID: func() (automations.AutomationID, error) {
			return automations.AutomationID(fixedAutomationLogID("aut", automationOrdinal.Add(1))), nil
		},
		NewRunID: func() (automations.AutomationRunID, error) {
			return automations.AutomationRunID(fixedAutomationLogID("arn", runOrdinal.Add(1))), nil
		},
		NewSkipID: func() (automations.AutomationSkipID, error) {
			return automations.AutomationSkipID(fixedAutomationLogID("ask", skipOrdinal.Add(1))), nil
		},
		NewCommandID: func() (devices.CommandID, error) {
			return devices.CommandID(fixedAutomationLogID("cmd", commandOrdinal.Add(1))), nil
		},
		NewCorrelationID: func() (devices.CorrelationID, error) {
			return devices.CorrelationID(fixedAutomationLogID("cor", correlationOrdinal.Add(1))), nil
		},
	}
}

// failingAutomationPruneRepository fails only retention writes.
type failingAutomationPruneRepository struct {
	*automations.SQLiteRepository

	pruneErr error
}

var _ automations.AutomationRepository = (*failingAutomationPruneRepository)(nil)

func (repo *failingAutomationPruneRepository) DeleteHistoryBefore(
	context.Context, time.Time, int,
) (int64, error) {
	return 0, repo.pruneErr
}

// Manual and automatic admission must log automation.run_started; only automatic
// admission carries Fact provenance.
func TestAutomationRunStartedLogsManualAndAutomaticAdmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer, logger := newAutomationLogSink()
	dependencies := fixedAutomationLogDependencies(logger)
	service, _ := newRuntimeService(t, newScriptedDevices(), dependencies)

	manual := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	manualRun, err := service.StartManualRun(ctx, manual.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	entity := newEntityID(t)
	factAutomation := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	if _, err = service.ReceiveDeviceFact(ctx, newObservationFact(t, entity, runtimeTestNow)); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	started := automationLogEvents(writer.records(t), "automation.run_started")
	if len(started) != 2 {
		t.Fatalf("automation.run_started events = %d, want 2:\n%s", len(started), writer.output())
	}
	byAutomation := map[string]map[string]any{}
	for _, record := range started {
		id, _ := record["automation_id"].(string)
		byAutomation[id] = record
	}

	manualRecord, ok := byAutomation[string(manual.ID)]
	if !ok {
		t.Fatalf("no run_started record for the manual Run:\n%s", writer.output())
	}
	requireAutomationLogField(t, manualRecord, "run_id", string(manualRun.ID))
	requireAutomationLogField(t, manualRecord, "source", string(automations.RunSourceManual))
	requireAutomationLogInt(t, manualRecord, "revision", 1)
	if _, present := manualRecord["family"]; present {
		t.Fatalf("manual run_started invents Fact provenance: %#v", manualRecord)
	}

	factRecord, ok := byAutomation[string(factAutomation.ID)]
	if !ok {
		t.Fatalf("no run_started record for the automatic Run:\n%s", writer.output())
	}
	requireAutomationLogField(t, factRecord, "source", string(automations.RunSourceDeviceFact))
	requireAutomationLogField(t, factRecord, "family", string(automations.DeviceFactObservation))
	requireAutomationLogField(t, factRecord, "variant", string(devices.DispositionApplied))
	requireAutomationLogInt(t, factRecord, "revision", 1)
	history := listHistory(t, service, factAutomation.ID)
	if len(history) != 1 {
		t.Fatalf("automatic Run history = %#v", history)
	}
	requireAutomationLogField(t, factRecord, "run_id", history[0].ID)

	if completed := automationLogEvents(writer.records(t), "automation.run_completed"); len(completed) != 2 {
		t.Fatalf("automation.run_completed events = %d, want 2:\n%s", len(completed), writer.output())
	}
}

// Skip logs must identify the Fact and exact reason, with stale taking precedence over busy.
func TestAutomationSkippedLogsExactBusyAndStaleReasons(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer, logger := newAutomationLogSink()
	dependencies := fixedAutomationLogDependencies(logger)
	scripted := newScriptedDevices()
	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	service, _ := newRuntimeService(t, scripted, dependencies)

	entity := newEntityID(t)
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	fresh := newObservationFact(t, entity, runtimeTestNow)
	if _, err := service.ReceiveDeviceFact(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	<-started

	busyFact := newObservationFact(t, entity, runtimeTestNow)
	if _, err := service.ReceiveDeviceFact(ctx, busyFact); err != nil {
		t.Fatal(err)
	}
	staleFact := newObservationFact(t, entity, runtimeTestNow.Add(-31*time.Second))
	if _, err := service.ReceiveDeviceFact(ctx, staleFact); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitForRuns(t, service)

	skips := automationLogEvents(writer.records(t), "automation.skipped")
	if len(skips) != 2 {
		t.Fatalf("automation.skipped events = %d, want 2:\n%s", len(skips), writer.output())
	}
	byFact := map[string]map[string]any{}
	for _, skip := range skips {
		id, _ := skip["fact_id"].(string)
		byFact[id] = skip
	}

	busyRecord, ok := byFact[string(busyFact.Observation.FactID)]
	if !ok {
		t.Fatalf("no busy skip for fact %s:\n%s", busyFact.Observation.FactID, writer.output())
	}
	requireAutomationLogField(t, busyRecord, "reason", string(automations.AutomationSkipBusy))
	requireAutomationLogField(t, busyRecord, "automation_id", string(record.ID))
	requireAutomationLogField(t, busyRecord, "family", string(automations.DeviceFactObservation))
	requireAutomationLogField(t, busyRecord, "variant", string(devices.DispositionApplied))
	requireAutomationLogInt(t, busyRecord, "revision", 1)
	if skipID, _ := busyRecord["skip_id"].(string); !strings.HasPrefix(skipID, "ask_") {
		t.Fatalf("busy skip_id = %#v, want an ask_ identity", busyRecord["skip_id"])
	}

	staleRecord, ok := byFact[string(staleFact.Observation.FactID)]
	if !ok {
		t.Fatalf("no stale skip for fact %s:\n%s", staleFact.Observation.FactID, writer.output())
	}
	requireAutomationLogField(t, staleRecord, "reason", string(automations.AutomationSkipStaleFact))
	requireAutomationLogField(t, staleRecord, "automation_id", string(record.ID))

	runStarted := automationLogEvents(writer.records(t), "automation.run_started")
	if len(runStarted) != 1 {
		t.Fatalf("automation.run_started events = %d, want 1 (a skip starts no Run):\n%s",
			len(runStarted), writer.output())
	}
}

// Decision logs must retain stable events without leaking definition JSON,
// Fact values, Command parameters, or upstream error text.
func TestAutomationDecisionLogsCarryNoSensitiveMaterial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer, logger := newAutomationLogSink()
	dependencies := fixedAutomationLogDependencies(logger)
	scripted := newScriptedDevices()
	repository := automations.NewSQLiteRepository(openAutomationDatabase(t), dependencies)
	pruneFailure := &failingAutomationPruneRepository{
		SQLiteRepository: repository,
		pruneErr:         errors.New("s3cr3t-prune-error"),
	}
	service := automations.NewService(pruneFailure, scripted, dependencies)

	const (
		definitionSentinel = "s3cr3t-definition-name"
		parameterSentinel  = "s3cr3t-parameter-value"
		factSentinel       = "s3cr3t-fact-value"
		faultSentinel      = "s3cr3t-upstream-error"
	)

	// Every Fact value still satisfies the trigger's numeric /temperature
	// comparison, so only the sentinel string distinguishes the payload.
	sentinelFactValue := func(suffix string) devices.Value {
		return devices.Value(fmt.Sprintf(`{"temperature":25,"sentinel":%q}`, factSentinel+suffix))
	}

	entity := newEntityID(t)
	definition := runtimeDefinitionFor(t, entity)
	definition.Name = definitionSentinel
	definition.Steps[0].Parameters = devices.CommandParameters(
		fmt.Sprintf(`{"value":%q}`, parameterSentinel),
	)
	record := createRuntimeAutomation(t, service, definition)
	replaced := runtimeDefinitionFor(t, entity)
	replaced.Name = definitionSentinel
	replaced.Steps[0].Parameters = devices.CommandParameters(
		fmt.Sprintf(`{"value":%q}`, parameterSentinel+"-two"),
	)
	if _, err := service.ReplaceAutomation(ctx, record.ID, record.Revision, replaced); err != nil {
		t.Fatal(err)
	}

	if _, err := service.StartManualRun(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	fact := newObservationFact(t, entity, runtimeTestNow)
	fact.Observation.Value = sentinelFactValue("")
	if _, err := service.ReceiveDeviceFact(ctx, fact); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	gate := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	scripted.block = gate
	scripted.onStart = func(devices.CommandInput) { once.Do(func() { close(started) }) }
	blockingFact := newObservationFact(t, entity, runtimeTestNow)
	blockingFact.Observation.Value = sentinelFactValue("-blocking")
	if _, err := service.ReceiveDeviceFact(ctx, blockingFact); err != nil {
		t.Fatal(err)
	}
	<-started
	busyFact := newObservationFact(t, entity, runtimeTestNow)
	busyFact.Observation.Value = sentinelFactValue("-busy")
	if _, err := service.ReceiveDeviceFact(ctx, busyFact); err != nil {
		t.Fatal(err)
	}
	staleFact := newObservationFact(t, entity, runtimeTestNow.Add(-31*time.Second))
	staleFact.Observation.Value = sentinelFactValue("-stale")
	if _, err := service.ReceiveDeviceFact(ctx, staleFact); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitForRuns(t, service)

	// A Run left running with no worker, exactly like a crash, then classified by
	// startup interruption.
	interrupted := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, newEntityID(t)))
	if _, err := repository.AdmitManualRun(ctx, interrupted.ID, runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	if err := service.InterruptActiveRuns(ctx, runtimeTestNow); err != nil {
		t.Fatal(err)
	}

	// A Step whose Command cannot be verified truthfully latches the executor fault.
	scripted.getCommand = func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
		return devices.CommandRecord{}, errors.New(faultSentinel)
	}
	faultDefinition := runtimeDefinitionFor(t, newEntityID(t))
	faultDefinition.Name = definitionSentinel
	faultDefinition.Steps[0].Parameters = devices.CommandParameters(
		fmt.Sprintf(`{"value":%q}`, parameterSentinel+"-fault"),
	)
	faultAutomation := createRuntimeAutomation(t, service, faultDefinition)
	if _, err := service.StartManualRun(ctx, faultAutomation.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	if _, err := service.PruneHistory(ctx, runtimeTestNow, 10); err == nil {
		t.Fatal("prune failure was not reported")
	}
	if err := service.DeleteAutomation(ctx, record.ID, record.Revision+1); err != nil {
		t.Fatal(err)
	}

	records := writer.records(t)
	for _, event := range []string{
		"automation.created",
		"automation.replaced",
		"automation.deleted",
		"automation.run_started",
		"automation.run_completed",
		"automation.run_interrupted",
		"automation.skipped",
		"automation.executor_fault",
		"core.automation_history_prune_failed",
	} {
		if len(automationLogEvents(records, event)) == 0 {
			t.Fatalf("missing stable log event %q:\n%s", event, writer.output())
		}
	}

	output := writer.output()
	for _, sentinel := range []string{
		definitionSentinel,
		parameterSentinel,
		factSentinel,
		faultSentinel,
	} {
		if strings.Contains(output, sentinel) {
			t.Fatalf("automation logs contain sensitive sentinel %q:\n%s", sentinel, output)
		}
	}
}
