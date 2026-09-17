package automations_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// admissionConditionTree builds one entity_state Condition leaf comparing
// /level with a static operand, so a definition can be made conditional without
// a full tree fixture.
func admissionConditionTree(entityID devices.EntityID, operand string) *automations.Condition {
	return &automations.Condition{
		ID:   "dark",
		Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: entityID,
			Pointer:  "/level",
			Operator: automations.ComparisonLessThan,
			Operand:  json.RawMessage(operand),
		},
	}
}

// admissionDefinitionFor builds one enabled definition whose Observation
// Trigger matches the fixture Fact and whose Conditions are the supplied tree.
func admissionDefinitionFor(
	t *testing.T,
	triggerEntity devices.EntityID,
	conditions *automations.Condition,
) automations.Definition {
	t.Helper()
	definition := runtimeDefinitionFor(t, triggerEntity)
	definition.Conditions = conditions
	return definition
}

// admissionSnapshot assembles one coherent snapshot from explicit entries.
func admissionSnapshot(entries ...devices.EntityStateSnapshotEntry) devices.EntityStateSnapshot {
	snapshot := devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}
	for _, entry := range entries {
		snapshot.Entries[entry.EntityID] = entry
	}
	return snapshot
}

// admissionState covers one Entity with accepted State carrying the supplied
// JSON value.
func admissionState(
	t *testing.T,
	entityID devices.EntityID,
	value string,
	observedAt time.Time,
) devices.EntityStateSnapshotEntry {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.EntityStateSnapshotEntry{
		EntityID: entityID,
		Exists:   true,
		State: &devices.State{
			EntityID:      entityID,
			Value:         devices.Value(value),
			ObservationID: observationID,
			ObservedAt:    observedAt,
		},
	}
}

// One State pre-read must cover exactly the sorted, deduplicated Entity union
// of enabled matching definitions with Conditions; unmatched, disabled, and
// unconditioned definitions must not expand the request.
func TestAutomaticAdmissionReadsOneSnapshotForMatchingConditionUnion(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	database := openAutomationDatabase(t)
	base := newAutomationRepository(t, database)
	observing := &observingAdmissionRepository{Repository: base}
	service := automations.NewService(observing, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	firstEntity := newEntityID(t)
	secondEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(firstEntity, "30"),
	))
	createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(secondEntity, "30"),
	))
	createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(firstEntity, "30"),
	))
	createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	unmatched := admissionDefinitionFor(t, newEntityID(t), admissionConditionTree(newEntityID(t), "30"))
	createRuntimeAutomation(t, service, unmatched)
	disabled := admissionDefinitionFor(t, entity, admissionConditionTree(newEntityID(t), "30"))
	disabled.Enabled = false
	createRuntimeAutomation(t, service, disabled)
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, firstEntity, `{"level":10}`, runtimeTestNow),
		admissionState(t, secondEntity, `{"level":10}`, runtimeTestNow),
	))

	outcome, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.StartedRuns != 4 || outcome.RecordedSkips != 0 {
		t.Fatalf("admission outcome = %#v, want four Runs", outcome)
	}
	want := []devices.EntityID{firstEntity, secondEntity}
	slices.Sort(want)
	requests := scripted.snapshotRequests()
	if len(requests) != 1 || !slices.Equal(requests[0], want) {
		t.Fatalf("snapshot requests = %v, want exactly %v", requests, want)
	}
	if calls := observing.listEnabledCalls.Load(); calls != 1 {
		t.Fatalf("ListEnabledAutomations calls = %d, want 1", calls)
	}
	waitForRuns(t, service)
	if scripted.executionCount() != 4 {
		t.Fatalf("executed Commands = %d, want 4", scripted.executionCount())
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].ConditionMode != automations.ConditionDecisionEvaluated ||
		history[0].ConditionResult == nil || *history[0].ConditionResult != automations.ConditionTrue {
		t.Fatalf("history summary = %#v", history)
	}
}

// A Fact that becomes stale after its State pre-read and before the transaction
// must commit stale_fact without evaluating Conditions.
func TestAutomaticAdmissionMarksStaleWhenFactAgesBeforeTransaction(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	var aged atomic.Bool
	dependencies.Now = func() time.Time {
		if aged.Load() {
			return runtimeTestNow.Add(automations.FactMaximumAge + time.Second)
		}
		return runtimeTestNow
	}
	service, _ := newRuntimeService(t, scripted, dependencies)
	entity := newEntityID(t)
	conditionEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(conditionEntity, "30"),
	))
	scripted.setEntityStateSnapshot(admissionSnapshot())
	// The Fact ages after the State pre-read and before the transaction.
	scripted.onSnapshotRead = func() { aged.Store(true) }

	outcome, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.StartedRuns != 0 || outcome.RecordedSkips != 1 {
		t.Fatalf("aged admission outcome = %#v, want one Skip", outcome)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Reason != automations.SkipStaleFact {
		t.Fatalf("aged admission history = %#v, want stale_fact", history)
	}
	if history[0].ConditionMode != automations.ConditionDecisionNotEvaluated {
		t.Fatalf("stale Skip decision mode = %q, want not_evaluated", history[0].ConditionMode)
	}
	if len(scripted.snapshotRequests()) != 1 {
		t.Fatalf("snapshot reads = %d, want exactly one before the transaction", len(scripted.snapshotRequests()))
	}
}

// Unconditioned admissions must never read Condition State, whatever they decide.
func TestUnconditionedAdmissionNeverReadsState(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	database := openAutomationDatabase(t)
	base := newAutomationRepository(t, database)
	observing := &observingAdmissionRepository{Repository: base}
	service := automations.NewService(observing, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	// Any State read would fail the admission, which proves none happened.
	scripted.setEntityStateSnapshotError(errors.New("State must not be read"))

	fresh, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.StartedRuns != 1 {
		t.Fatalf("unconditioned outcome = %#v, want one Run", fresh)
	}
	busy, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if busy.StartedRuns != 0 || busy.RecordedSkips != 1 {
		t.Fatalf("busy unconditioned outcome = %#v, want one Skip", busy)
	}
	stale, err := service.ReceiveDeviceFact(context.Background(),
		newObservationFact(t, entity, runtimeTestNow.Add(-automations.FactMaximumAge-time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if stale.StartedRuns != 0 || stale.RecordedSkips != 1 {
		t.Fatalf("stale unconditioned outcome = %#v, want one Skip", stale)
	}
	reasons := make(map[automations.SkipReason]int)
	for _, summary := range listHistory(t, service, record.ID) {
		reasons[summary.Reason]++
	}
	if reasons[automations.SkipBusy] != 1 || reasons[automations.SkipStaleFact] != 1 {
		t.Fatalf("skip reasons = %v, want one busy and one stale_fact", reasons)
	}
	if reads := scripted.snapshotRequests(); len(reads) != 0 {
		t.Fatalf("an unconditioned path read State: %v", reads)
	}
	if calls := observing.listEnabledCalls.Load(); calls != 3 {
		t.Fatalf("ListEnabledAutomations calls = %d, want one per admission", calls)
	}
}

// A committed manual condition block returns a typed error carrying the
// committed Skip's history reference, after the transaction wrote it.
func TestManualAdmissionConditionBlockedReturnsTypedErrorAfterCommit(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	conditionEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(conditionEntity, "30"),
	))
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, conditionEntity, `{"level":90}`, runtimeTestNow),
	))

	_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
	var blocked *automations.ConditionsBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("manual blocked error = %v, want AutomationConditionsBlockedError", err)
	}
	if !errors.Is(err, automations.ErrAutomationConditionsBlocked) {
		t.Fatalf("blocked error does not match ErrAutomationConditionsBlocked: %v", err)
	}
	if blocked.AutomationID != record.ID || blocked.SkipID == "" ||
		blocked.Reason != automations.SkipConditionsFalse {
		t.Fatalf("blocked error = %#v", blocked)
	}
	entry := historyEntry(t, service, record.ID, string(blocked.SkipID))
	if entry.Skip == nil || entry.Skip.Reason != automations.SkipConditionsFalse ||
		entry.Skip.Source != automations.RunSourceManual || entry.Skip.Fact != nil {
		t.Fatalf("committed manual Skip = %#v", entry.Skip)
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("blocked manual admission executed %d Commands", scripted.executionCount())
	}
	if len(listHistory(t, service, record.ID)) != 1 {
		t.Fatal("blocked manual admission did not retain exactly one Skip")
	}
}

// An explicit bypass must admit without reading State, and a repeated blocked
// request must create a distinct Skip rather than replaying one.
func TestManualAdmissionBypassSkipsStateAndRepeatsStayDistinct(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	database := openAutomationDatabase(t)
	base := newAutomationRepository(t, database)
	observing := &observingAdmissionRepository{Repository: base}
	service := automations.NewService(observing, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	conditionEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(conditionEntity, "30"),
	))
	scripted.setEntityStateSnapshotError(errors.New("State must not be read for a bypass"))

	run, err := service.StartManualRun(context.Background(), automations.ManualRunInput{
		AutomationID: record.ID, BypassConditions: true,
	})
	if err != nil {
		t.Fatalf("bypassed manual admission = %v, want a committed Run", err)
	}
	if run.ConditionDecision.DecisionMode() != automations.ConditionDecisionBypassed ||
		!run.ConditionDecision.BypassRequested() {
		t.Fatalf("bypassed Run decision = %#v", run.ConditionDecision)
	}
	if reads := scripted.snapshotRequests(); len(reads) != 0 {
		t.Fatalf("bypass read State: %v", reads)
	}
	waitForRuns(t, service)

	// Repeated blocked manual requests create distinct Skips with no Fact.
	scripted.setEntityStateSnapshotError(nil)
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, conditionEntity, `{"level":90}`, runtimeTestNow),
	))
	first := requireManualConditionBlock(t, service, record.ID)
	second := requireManualConditionBlock(t, service, record.ID)
	if first == second {
		t.Fatalf("repeated blocked manual requests reused Skip %s", first)
	}
	if calls := observing.listEnabledCalls.Load(); calls != 0 {
		t.Fatalf("manual admissions called ListEnabledAutomations %d times, want 0", calls)
	}
}

// An unconditioned manual admission does not need a State snapshot or an
// enabled-definition list read, even though it still pre-reads its own definition.
func TestManualAdmissionWithoutConditionsSkipsSnapshotAndEnabledList(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	database := openAutomationDatabase(t)
	base := newAutomationRepository(t, database)
	observing := &observingAdmissionRepository{Repository: base}
	service := automations.NewService(observing, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, newEntityID(t)))
	scripted.setEntityStateSnapshotError(errors.New("State must not be read for unconditioned manual admission"))

	if _, err := service.StartManualRun(context.Background(), automations.ManualRunInput{
		AutomationID: record.ID,
	}); err != nil {
		t.Fatalf("unconditioned manual admission = %v", err)
	}
	if reads := scripted.snapshotRequests(); len(reads) != 0 {
		t.Fatalf("unconditioned manual admission read State: %v", reads)
	}
	if calls := observing.listEnabledCalls.Load(); calls != 0 {
		t.Fatalf("unconditioned manual admission called ListEnabledAutomations %d times", calls)
	}
}

// requireManualConditionBlock admits one manual request that must commit a
// Condition Skip and returns the committed Skip identity.
func requireManualConditionBlock(
	t *testing.T,
	service *automations.Service,
	id automations.AutomationID,
) automations.SkipID {
	t.Helper()
	_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: id})
	var blocked *automations.ConditionsBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("manual admission = %v, want a committed Condition block", err)
	}
	return blocked.SkipID
}

// Corrupt stored State must log one fixed, value-free diagnostic, retain the
// Fact for repair, and never close the executor gate or write an outcome.
func TestConditionStateCorruptionLogsFixedDiagnosticAndRetainsFact(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	writer, logger := newAutomationLogSink()
	dependencies.Logger = logger
	service, _ := newRuntimeService(t, scripted, dependencies)
	entity := newEntityID(t)
	conditionEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(conditionEntity, "30"),
	))
	scripted.setEntityStateSnapshotError(devices.ErrEntityStateSnapshotCorrupt)
	fact := newObservationFact(t, entity, runtimeTestNow)

	if _, err := service.ReceiveDeviceFact(context.Background(), fact); !errors.Is(
		err, devices.ErrEntityStateSnapshotCorrupt,
	) {
		t.Fatalf("corrupt State admission error = %v", err)
	}
	events := automationLogEvents(writer.records(t), "automation.condition_state_corrupt")
	if len(events) != 1 {
		t.Fatalf("condition_state_corrupt events = %d, want 1:\n%s", len(events), writer.output())
	}
	if strings.Contains(writer.output(), string(conditionEntity)) {
		t.Fatalf("corruption diagnostic exposed state evidence:\n%s", writer.output())
	}
	if history := listHistory(t, service, record.ID); len(history) != 0 {
		t.Fatalf("corrupt State admission wrote history: %#v", history)
	}
	if !service.AdmissionOpen() {
		t.Fatal("a State read failure closed the executor gate")
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("corrupt State admission executed %d Commands", scripted.executionCount())
	}

	// Repair reprocesses the still-fresh Fact because no receipt was written.
	scripted.setEntityStateSnapshotError(nil)
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, conditionEntity, `{"level":10}`, runtimeTestNow),
	))
	outcome, err := service.ReceiveDeviceFact(context.Background(), fact)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.StartedRuns != 1 {
		t.Fatalf("repaired admission outcome = %#v, want one Run", outcome)
	}
}

// observingAdmissionRepository records definition pre-reads while delegating
// every admission operation to the real SQLite repository.
type observingAdmissionRepository struct {
	automations.Repository

	listEnabledCalls atomic.Int32
}

func (repository *observingAdmissionRepository) ListEnabledAutomations(
	ctx context.Context,
) ([]automations.Record, error) {
	repository.listEnabledCalls.Add(1)
	return repository.Repository.ListEnabledAutomations(ctx)
}

// replacingAdmissionRepository changes a definition after the Service pre-read
// and before the transaction, deterministically exercising the coverage race.
type replacingAdmissionRepository struct {
	*observingAdmissionRepository

	replace  func()
	attempts atomic.Int32
}

func (repository *replacingAdmissionRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
) (automations.AdmissionResult, error) {
	if repository.attempts.Add(1) == 1 && repository.replace != nil {
		repository.replace()
	}
	return repository.Repository.AdmitDeviceFact(ctx, fact, snapshot, now)
}

// A definition edit that adds a newly required Entity after the pre-read must
// return the coverage race error without retrying, committing, or starting work.
func TestDefinitionReplacementRequiresNewSnapshotReturnsCoverageError(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	database := openAutomationDatabase(t)
	base := newAutomationRepository(t, database)
	racing := &replacingAdmissionRepository{
		observingAdmissionRepository: &observingAdmissionRepository{Repository: base},
	}
	service := automations.NewService(racing, scripted, dependencies)
	entity := newEntityID(t)
	firstEntity := newEntityID(t)
	secondEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(firstEntity, "30"),
	))
	racing.replace = func() {
		updated := admissionDefinitionFor(t, entity, admissionConditionTree(secondEntity, "30"))
		if _, err := base.ReplaceAutomation(context.Background(), record.ID, record.Revision, updated); err != nil {
			t.Errorf("replace definition: %v", err)
		}
	}
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, firstEntity, `{"level":10}`, runtimeTestNow),
	))

	_, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
	if !errors.Is(err, automations.ErrConditionSnapshotRequired) {
		t.Fatalf("definition-race admission error = %v, want ErrConditionSnapshotRequired", err)
	}
	requests := scripted.snapshotRequests()
	if len(requests) != 1 || !slices.Equal(requests[0], []devices.EntityID{firstEntity}) {
		t.Fatalf("snapshot requests = %v, want one pre-read for %v", requests, firstEntity)
	}
	if attempts := racing.attempts.Load(); attempts != 1 {
		t.Fatalf("AdmitDeviceFact calls = %d, want 1", attempts)
	}
	if history := listHistory(t, service, record.ID); len(history) != 0 {
		t.Fatalf("definition-race history = %#v, want nothing committed", history)
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("definition-race admission executed %d Commands", scripted.executionCount())
	}
}

// A definition edit that only changes an operand reusing covered IDs must use
// the same coherent pre-read evidence without another read.
func TestDefinitionOperandEditReusesCoveredEvidence(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	database := openAutomationDatabase(t)
	base := newAutomationRepository(t, database)
	racing := &replacingAdmissionRepository{
		observingAdmissionRepository: &observingAdmissionRepository{Repository: base},
	}
	service := automations.NewService(racing, scripted, dependencies)
	entity := newEntityID(t)
	conditionEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(conditionEntity, "30"),
	))
	racing.replace = func() {
		updated := admissionDefinitionFor(t, entity, admissionConditionTree(conditionEntity, "5"))
		if _, err := base.ReplaceAutomation(context.Background(), record.ID, record.Revision, updated); err != nil {
			t.Errorf("replace definition: %v", err)
		}
	}
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, conditionEntity, `{"level":10}`, runtimeTestNow),
	))

	outcome, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.StartedRuns != 0 || outcome.RecordedSkips != 1 {
		t.Fatalf("admission outcome = %#v, want the replacement operand to block the Run", outcome)
	}
	if reads := len(scripted.snapshotRequests()); reads != 1 {
		t.Fatalf("snapshot reads = %d, want the covered evidence reused", reads)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Reason != automations.SkipConditionsFalse {
		t.Fatalf("history = %#v, want conditions_false from the replacement operand", history)
	}
	entry := historyEntry(t, service, record.ID, history[0].ID)
	if got := string(entry.Skip.ConditionDecision.DecisionSnapshot().EntityState.Operand); got != "5" {
		t.Fatalf("decided operand = %s, want the replacement definition's operand", got)
	}
}

// The admission reservation must span a Condition State read: Drain joins the
// read and the committed Run instead of closing SQLite underneath them.
func TestDrainJoinsConditionSnapshotRead(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	entity := newEntityID(t)
	conditionEntity := newEntityID(t)
	record := createRuntimeAutomation(t, service, admissionDefinitionFor(
		t, entity, admissionConditionTree(conditionEntity, "30"),
	))
	scripted.setEntityStateSnapshot(admissionSnapshot(
		admissionState(t, conditionEntity, `{"level":10}`, runtimeTestNow),
	))
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	scripted.onSnapshotRead = func() {
		once.Do(func() { close(entered) })
		<-release
	}

	admitted := make(chan error, 1)
	go func() {
		_, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
		admitted <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the Condition State read never started")
	}

	service.StopAdmission()
	waited := make(chan error, 1)
	go func() { waited <- service.Drain(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("Drain returned while the Condition State read was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Drain = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not join the admitted Run")
	}
	if err := <-admitted; err != nil {
		t.Fatalf("ReceiveDeviceFact = %v, want a committed admission", err)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Status != automations.RunInterrupted {
		t.Fatalf("post-drain history = %#v, want one interrupted Run", history)
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands", scripted.executionCount())
	}
}

// DecideConditions maps a true, false, or unknown evaluation to the admission
// decision and Skip reason it implies, and rejects an uncovered snapshot before
// evaluating anything.
func TestDecideConditionsOutcome(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	tests := []struct {
		name         string
		snapshot     devices.EntityStateSnapshot
		wantReason   automations.SkipReason
		wantResult   automations.ConditionResult
		wantCoverage bool
	}{
		{
			name:       "true admits",
			snapshot:   admissionSnapshot(admissionState(t, entity, `{"level":10}`, runtimeTestNow)),
			wantResult: automations.ConditionTrue,
		},
		{
			name:       "false blocks",
			snapshot:   admissionSnapshot(admissionState(t, entity, `{"level":90}`, runtimeTestNow)),
			wantReason: automations.SkipConditionsFalse,
			wantResult: automations.ConditionFalse,
		},
		{
			name:       "unknown blocks",
			snapshot:   admissionSnapshot(admissionState(t, entity, `{"level":"high"}`, runtimeTestNow)),
			wantReason: automations.SkipConditionsUnknown,
			wantResult: automations.ConditionUnknown,
		},
		{name: "uncovered rejects", snapshot: admissionSnapshot(), wantCoverage: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			conditions := admissionConditionTree(entity, "30")
			decision, reason, err := automations.DecideConditions(conditions, test.snapshot, runtimeTestNow)
			if test.wantCoverage {
				requireConditionCoverage(t, err, entity)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			requireEvaluatedDecision(t, decision, test.wantResult)
			if reason != test.wantReason {
				t.Fatalf("skip reason = %q, want %q", reason, test.wantReason)
			}
		})
	}
}

// requireConditionCoverage asserts one coverage rejection naming exactly the
// requested Entity.
func requireConditionCoverage(t *testing.T, err error, entity devices.EntityID) {
	t.Helper()
	coverage, found := errors.AsType[*automations.ConditionSnapshotRequiredError](err)
	if !found {
		t.Fatalf("error = %v, want ConditionSnapshotRequiredError", err)
	}
	if len(coverage.RequiredEntityIDs) != 1 || coverage.RequiredEntityIDs[0] != entity {
		t.Fatalf("required entities = %v, want %v", coverage.RequiredEntityIDs, entity)
	}
}

// requireEvaluatedDecision asserts one complete evaluated decision against the
// evaluated tree and result.
func requireEvaluatedDecision(
	t *testing.T,
	decision automations.ConditionDecision,
	wantResult automations.ConditionResult,
) {
	t.Helper()
	if decision.DecisionMode() != automations.ConditionDecisionEvaluated ||
		decision.DecisionEvaluation() == nil ||
		decision.DecisionEvaluation().Result != wantResult {
		t.Fatalf("decision = %#v", decision)
	}
}
