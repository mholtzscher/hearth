//go:build integration

package automations_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Manual and automatic admission must log automation.run_started; only automatic
// admission carries Fact provenance.
func TestAutomationRunStartedLogsManualAndAutomaticAdmission(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	writer, logger := newAutomationLogSink()
	dependencies := fixedAutomationLogDependencies(logger)
	service, _ := newRuntimeService(t, newScriptedDevices(), dependencies)

	manual := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	manualRun, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: manual.ID})
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

	busyRecord, ok := byFact[string(busyFact.FactID)]
	if !ok {
		t.Fatalf("no busy skip for fact %s:\n%s", busyFact.FactID, writer.output())
	}
	requireAutomationLogField(t, busyRecord, "reason", string(automations.SkipBusy))
	requireAutomationLogField(t, busyRecord, "automation_id", string(record.ID))
	requireAutomationLogField(t, busyRecord, "family", string(automations.DeviceFactObservation))
	requireAutomationLogField(t, busyRecord, "variant", string(devices.DispositionApplied))
	requireAutomationLogInt(t, busyRecord, "revision", 1)
	if skipID, _ := busyRecord["skip_id"].(string); !strings.HasPrefix(skipID, "ask_") {
		t.Fatalf("busy skip_id = %#v, want an ask_ identity", busyRecord["skip_id"])
	}

	staleRecord, ok := byFact[string(staleFact.FactID)]
	if !ok {
		t.Fatalf("no stale skip for fact %s:\n%s", staleFact.FactID, writer.output())
	}
	requireAutomationLogField(t, staleRecord, "reason", string(automations.SkipStaleFact))
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
	repository := automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies)
	service := automations.NewService(repository, scripted, dependencies)

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
	commandBody := definition.Steps[0].Body.(automations.CommandStep)
	commandBody.Parameters = devices.CommandParameters(
		fmt.Sprintf(`{"value":%q}`, parameterSentinel),
	)
	definition.Steps[0].Body = commandBody

	record := createRuntimeAutomation(t, service, definition)
	replaced := runtimeDefinitionFor(t, entity)
	replaced.Name = definitionSentinel
	commandBody2 := replaced.Steps[0].Body.(automations.CommandStep)
	commandBody2.Parameters = devices.CommandParameters(
		fmt.Sprintf(`{"value":%q}`, parameterSentinel+"-two"),
	)
	replaced.Steps[0].Body = commandBody2

	if _, err := service.ReplaceAutomation(ctx, record.ID, record.Revision, replaced); err != nil {
		t.Fatal(err)
	}

	if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID}); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

	fact := newObservationFact(t, entity, runtimeTestNow)
	fact.Value = sentinelFactValue("")
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
	blockingFact.Value = sentinelFactValue("-blocking")
	if _, err := service.ReceiveDeviceFact(ctx, blockingFact); err != nil {
		t.Fatal(err)
	}
	<-started
	busyFact := newObservationFact(t, entity, runtimeTestNow)
	busyFact.Value = sentinelFactValue("-busy")
	if _, err := service.ReceiveDeviceFact(ctx, busyFact); err != nil {
		t.Fatal(err)
	}
	staleFact := newObservationFact(t, entity, runtimeTestNow.Add(-31*time.Second))
	staleFact.Value = sentinelFactValue("-stale")
	if _, err := service.ReceiveDeviceFact(ctx, staleFact); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitForRuns(t, service)

	// A Run left running with no worker, exactly like a crash, then classified by
	// startup interruption.
	interrupted := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, newEntityID(t)))
	if _, err := repository.AdmitManualRun(
		ctx, automations.ManualRunInput{AutomationID: interrupted.ID}, devices.EntityStateSnapshot{}, runtimeTestNow,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.InterruptActiveRuns(ctx, runtimeTestNow); err != nil {
		t.Fatal(err)
	}

	// A Step whose Command cannot be verified truthfully faults only its Run.
	scripted.getCommand = func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
		return devices.CommandRecord{}, errors.New(faultSentinel)
	}
	faultDefinition := runtimeDefinitionFor(t, newEntityID(t))
	faultDefinition.Name = definitionSentinel
	commandBody3 := faultDefinition.Steps[0].Body.(automations.CommandStep)
	commandBody3.Parameters = devices.CommandParameters(
		fmt.Sprintf(`{"value":%q}`, parameterSentinel+"-fault"),
	)
	faultDefinition.Steps[0].Body = commandBody3

	faultAutomation := createRuntimeAutomation(t, service, faultDefinition)
	if _, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: faultAutomation.ID}); err != nil {
		t.Fatal(err)
	}
	waitForRuns(t, service)

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
