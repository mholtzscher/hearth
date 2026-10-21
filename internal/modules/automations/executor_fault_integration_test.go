//go:build integration

package automations_test

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

// ADR 0026: every uncertain operation stops, but neither an active sibling nor
// later independent work loses admission. Counts protect against external replay.
func TestExecutorWriteFaultStopsOnlyAffectedRun(t *testing.T) {
	t.Parallel()
	for _, test := range []executorFaultCase{
		{"admission failed", "admission", false, false, 0},
		{"start failed", "start", false, false, 0},
		{"start ambiguous", "start", true, false, 0},
		{"step failed", "step", false, false, 1},
		{"step ambiguous", "step", true, false, 1},
		{"run failed", "run", false, false, 2},
		{"run ambiguous", "run", true, false, 2},
		{"cleanup unavailable", "step", false, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runExecutorFaultScenario(t, test)
		})
	}
}

func runExecutorFaultScenario(t *testing.T, test executorFaultCase) {
	t.Helper()
	writer, logger := newAutomationLogSink()
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	repo := &executorFaultRepository{
		Repository: automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
		boundary:   test.boundary, commit: test.commit, cleanupFails: test.cleanupFails,
		cleanupReached: make(chan struct{}),
	}
	repo.runID.Store(automations.RunID(""))
	scripted := newScriptedDevices()
	service := automations.NewService(repo, scripted, dependencies)
	target := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))
	repo.target = target.ID
	sibling := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))
	siblingEntity := sibling.Definition.Steps[0].Body.(automations.CommandStep).EntityID
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	blockFaultSiblingCommand(t, scripted, siblingEntity, entered, release)
	siblingRun := admitDelayRun(t, service, sibling)
	awaitDelaySignal(t, entered)
	run, err := service.StartManualRun(t.Context(), automations.ManualRunInput{AutomationID: target.ID})
	awaitExecutorFault(t, repo, err)
	release <- struct{}{}
	waitForRuns(t, service)
	if !service.AdmissionOpen() || scripted.executionCount() != test.commands+2 {
		t.Fatalf(
			"module stopped or execution replayed: admission=%v Commands=%d",
			service.AdmissionOpen(),
			scripted.executionCount(),
		)
	}
	assertFaultSiblingSucceeded(t, service, sibling, siblingRun)
	assertExecutorFaultCleanup(t, repo, service, target, run, writer)
	later := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))
	laterRun := admitDelayRun(t, service, later)
	waitForRuns(t, service)
	assertFaultSiblingSucceeded(t, service, later, laterRun)
	if scripted.executionCount() != test.commands+3 ||
		strings.Contains(writer.output(), errExecutorWrite.Error()) {
		t.Fatal("later work replayed a Command or logs exposed database error")
	}
	if test.cleanupFails {
		assertExecutorCleanupBackstop(t, service, target, run, writer)
		if scripted.executionCount() != test.commands+3 {
			t.Fatal("startup recovery replayed external work")
		}
	}
}
