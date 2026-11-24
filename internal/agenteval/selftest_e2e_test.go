//go:build e2e

package agenteval //nolint:testpackage // The self-test drives the worker with its scripted model.

import (
	"path/filepath"
	"testing"
)

// TestScenarioReferencesPass replays every scenario's reference trajectory
// through production Core and the scripted simulator. A failure means a
// scenario, its checks, or the household fixture no longer agree.
func TestScenarioReferencesPass(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "testdata", "agenteval")
	scenarios, err := LoadScenarios(filepath.Join(root, "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			t.Parallel()
			record := RunTrial(t.Context(), TrialOptions{
				Scenario: scenario, SimulatorConfig: filepath.Join(root, "household.simulator.yaml"),
				Model: NewScriptedModel(scenario.Reference), Dir: t.TempDir(), Revision: "selftest",
				Settings: ModelSettings{Provider: "openai", Model: "scripted"},
			})
			if record.Outcome != TrialCompleted {
				t.Fatalf("outcome %s: %s", record.Outcome, record.Error)
			}
			if record.Seconds <= 0 {
				t.Fatal("the trial's duration was not recorded")
			}
			scored := Score(scenario, record)
			for _, result := range scored.Results {
				if !result.Passed {
					t.Errorf("%s: %s", result.Label, result.Detail)
				}
			}
		})
	}
}

// TestScenarioChecksRejectInertAnswers proves every scenario's checks
// discriminate: an Agent that only replies "OK." must fail each one.
func TestScenarioChecksRejectInertAnswers(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "testdata", "agenteval")
	scenarios, err := LoadScenarios(filepath.Join(root, "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range scenarios {
		t.Run(scenario.ID, func(t *testing.T) {
			t.Parallel()
			inert := "OK."
			steps := make([]ScriptStep, len(scenario.Turns))
			for index := range steps {
				steps[index] = ScriptStep{Reply: &inert}
			}
			record := RunTrial(t.Context(), TrialOptions{
				Scenario: scenario, SimulatorConfig: filepath.Join(root, "household.simulator.yaml"),
				Model: NewScriptedModel(steps), Dir: t.TempDir(), Revision: "selftest",
				Settings: ModelSettings{Provider: "openai", Model: "scripted"},
			})
			if record.Outcome != TrialCompleted {
				t.Fatalf("outcome %s: %s", record.Outcome, record.Error)
			}
			if Score(scenario, record).Passed {
				t.Fatal("an inert reply passed every check")
			}
		})
	}
}
