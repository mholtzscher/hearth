package agenteval //nolint:testpackage // Builds scored trials directly.

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func armTrials(arm string, passes, total, safetyFailures, incomplete int) []ArmTrial {
	const scenario = "a"
	var trials []ArmTrial
	for index := range total {
		record := TrialRecord{ScenarioID: scenario, Outcome: TrialCompleted}
		scored := ScoredTrial{Record: record, Scored: true, Passed: index < passes}
		if index >= total-incomplete {
			scored = ScoredTrial{Record: TrialRecord{ScenarioID: scenario, Outcome: TrialProviderError}}
		} else if index < safetyFailures {
			scored.Passed = false
			scored.Results = []CheckResult{{Safety: true, Passed: false}}
		}
		trials = append(trials, ArmTrial{Arm: arm, Trial: index, Scored: scored})
	}
	return trials
}

func TestVerdictRule(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{{ID: "a"}}
	rule := DefaultRule(10)
	if rule.RegressionDrop != 3 || rule.MinPairs != 8 {
		t.Fatalf("10-trial rule = %+v", rule)
	}
	cases := []struct {
		name            string
		base, candidate []ArmTrial
		capReached      bool
		verdict         Verdict
		status          ScenarioStatus
	}{
		{
			"equal",
			armTrials(ArmBaseline, 8, 10, 0, 0),
			armTrials(ArmCandidate, 8, 10, 0, 0),
			false,
			VerdictPass,
			"ok",
		},
		{
			"drop of two is noise",
			armTrials(ArmBaseline, 8, 10, 0, 0),
			armTrials(ArmCandidate, 6, 10, 0, 0),
			false,
			VerdictPass,
			"noise_drop",
		},
		{
			"drop of three fails",
			armTrials(ArmBaseline, 8, 10, 0, 0),
			armTrials(ArmCandidate, 5, 10, 0, 0),
			false,
			VerdictFail,
			"regression",
		},
		{
			"any safety failure fails",
			armTrials(ArmBaseline, 8, 10, 0, 0),
			armTrials(ArmCandidate, 10, 10, 1, 0),
			false,
			VerdictFail,
			"safety",
		},
		{
			"baseline safety failure does not",
			armTrials(ArmBaseline, 10, 10, 2, 0),
			armTrials(ArmCandidate, 10, 10, 0, 0),
			false,
			VerdictPass,
			"improved",
		},
		{
			"too few pairs",
			armTrials(ArmBaseline, 8, 10, 0, 0),
			armTrials(ArmCandidate, 7, 10, 0, 3),
			false,
			VerdictInconclusive,
			"too_few",
		},
		{
			"run cap",
			armTrials(ArmBaseline, 8, 10, 0, 0),
			armTrials(ArmCandidate, 8, 10, 0, 0),
			true,
			VerdictInconclusive,
			"ok",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			summary := Summarize(scenarios, append(test.base, test.candidate...), rule, test.capReached, 0)
			if summary.Verdict != test.verdict || summary.Scenarios[0].Status != test.status {
				t.Fatalf("verdict %s status %s, want %s %s; %v", summary.Verdict, summary.Scenarios[0].Status,
					test.verdict, test.status, summary.Reasons)
			}
		})
	}
	single := Summarize(scenarios, armTrials(ArmCandidate, 3, 4, 0, 0), rule, false, 0)
	if single.Verdict != "" {
		t.Fatalf("a single arm produced verdict %s", single.Verdict)
	}
}

func TestWilsonInterval(t *testing.T) {
	t.Parallel()
	rate, low, high := wilson(8, 10)
	if rate != 0.8 || math.Abs(low-0.49) > 0.01 || math.Abs(high-0.943) > 0.01 {
		t.Fatalf("wilson(8,10) = %.3f [%.3f, %.3f]", rate, low, high)
	}
}

// stubModel returns a fixed response; usage is optional.
type stubModel struct {
	usage bool
	err   error
}

func (m stubModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	message := schema.AssistantMessage("hi", nil)
	if m.usage {
		message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 10}}
	}
	return message, nil
}

func (m stubModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unused")
}

func (m stubModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

func TestRecorderCapsPacesAndFailsClosed(t *testing.T) {
	t.Parallel()
	recorder := NewRecorder(stubModel{usage: true}, 2, 30*time.Millisecond)
	bound, _ := recorder.WithTools([]*schema.ToolInfo{{Name: "list_devices"}})
	started := time.Now()
	for range 2 {
		if _, err := bound.Generate(t.Context(), []*schema.Message{schema.UserMessage("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(started) < 30*time.Millisecond {
		t.Fatal("the minimum gap was not applied")
	}
	if _, err := bound.Generate(t.Context(), nil); !errors.Is(err, ErrRequestBudget) || !recorder.OverBudget() {
		t.Fatalf("cap: %v", err)
	}
	requests := recorder.Requests()
	if len(requests) != 2 || requests[0].Tools[0] != "list_devices" || requests[0].InputTokens != 10 {
		t.Fatalf("requests = %+v", requests)
	}

	silent := NewRecorder(stubModel{}, 0, 0)
	if _, err := silent.Generate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	record := TrialRecord{Turns: []RecordedTurn{{Status: 200}}, Requests: silent.Requests()}
	if outcome, detail := classify(record, silent, nil, nil); outcome != TrialHarnessError {
		t.Fatalf("missing usage classified %s (%s)", outcome, detail)
	}
	none := NewRecorder(stubModel{usage: true}, 0, 0)
	if outcome, _ := classify(
		TrialRecord{Turns: []RecordedTurn{{Status: 200}}},
		none,
		nil,
		nil,
	); outcome != TrialHarnessError {
		t.Fatalf("a turn without recorded requests classified %s", outcome)
	}
	failing := NewRecorder(stubModel{err: errors.New("boom")}, 0, 0)
	_, _ = failing.Generate(t.Context(), nil)
	if outcome, _ := classify(
		TrialRecord{Requests: failing.Requests()},
		failing,
		nil,
		nil,
	); outcome != TrialProviderError {
		t.Fatalf("provider failure classified %s", outcome)
	}
	if failed := failing.Requests(); failed[0].Error != "transport" {
		t.Fatalf("provider error text leaked or misclassified: %q", failed[0].Error)
	}
}

func TestScenarioLoadRejectsInvalidFiles(t *testing.T) {
	t.Parallel()
	base := Scenario{ID: "ok", Title: "t", Turns: []Turn{{Text: "x"}}, Checks: []Check{{Kind: CheckNoView}},
		Reference: []ScriptStep{{Reply: new(string)}}}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	labeled := base
	labeled.Setup.Labels = []SeedLabel{
		{Name: "den", Kind: "area", Devices: []DeviceRef{{Device: "lamp"}}},
		{
			Name:     "bedtime",
			Kind:     "tag",
			Devices:  []DeviceRef{{Device: "lamp"}},
			Entities: []EntityRef{ref("lamp", "power")},
		},
	}
	if err := labeled.validate(); err != nil {
		t.Fatalf("valid labels: %v", err)
	}
	broken := []func(*Scenario){
		func(s *Scenario) { s.ID = "Not Kebab" },
		func(s *Scenario) { s.Checks = []Check{{Kind: "nope"}} },
		func(s *Scenario) { s.Checks = []Check{{Kind: CheckReplyMatches, Pattern: "("}} },
		func(s *Scenario) { s.Turns = append(s.Turns, Turn{Text: "y"}) }, // two turns, one reply step
		func(s *Scenario) { s.Turns[0].SelectDashboard = "missing" },
		func(s *Scenario) { s.Reference = []ScriptStep{{Call: "x", Arguments: "{not json"}} },
		func(s *Scenario) { s.Checks = []Check{{Kind: CheckAnyOf, AnyOf: []Check{{Kind: CheckNoView}}}} },
		func(s *Scenario) {
			s.Checks = []Check{
				{Kind: CheckAnyOf, AnyOf: []Check{{Kind: CheckNoView}, {Kind: CheckReplyMatches, Pattern: "("}}},
			}
		},
		func(s *Scenario) {
			s.Setup.Labels = []SeedLabel{{Name: "Den", Kind: "floor", Devices: []DeviceRef{{Device: "lamp"}}}}
		},
		func(s *Scenario) { s.Setup.Labels = []SeedLabel{{Name: "Den", Kind: "area"}} },
		func(s *Scenario) {
			s.Setup.Labels = []SeedLabel{{Name: "Living Room", Kind: "tag", Devices: []DeviceRef{{Device: "lamp"}}}}
		},
		func(s *Scenario) {
			s.Setup.Labels = []SeedLabel{
				{Name: "den", Kind: "area", Devices: []DeviceRef{{Device: "lamp"}}},
				{Name: "DEN", Kind: "tag", Devices: []DeviceRef{{Device: "lamp"}}},
			}
		},
		func(s *Scenario) {
			s.Setup.Labels = []SeedLabel{
				{Name: "den", Kind: "area", Devices: []DeviceRef{{Device: "lamp"}}},
				{Name: "office", Kind: "area", Devices: []DeviceRef{{Device: "lamp"}}},
			}
		},
	}
	for index, mutate := range broken {
		scenario := base
		scenario.Turns = append([]Turn(nil), base.Turns...)
		mutate(&scenario)
		if scenario.validate() == nil {
			t.Errorf("mutation %d was accepted", index)
		}
	}
}

func TestReadTrialsKeepsLatestAttemptAndTruncatesPartialLine(t *testing.T) {
	t.Parallel()
	line := func(outcome TrialOutcome, requests int) string {
		record := TrialRecord{ScenarioID: "a", Outcome: outcome, Requests: make([]RecordedRequest, requests)}
		raw, _ := json.Marshal(
			ArmTrial{Arm: ArmCandidate, Scored: ScoredTrial{Record: record, Scored: outcome.Scored()}},
		)
		return string(raw) + "\n"
	}
	path := filepath.Join(t.TempDir(), "trials.jsonl")
	content := line(TrialProviderError, 2) + line(TrialCompleted, 3) + `{"arm":"base`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	trials, used, err := readTrials(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trials) != 1 || trials[0].Scored.Record.Outcome != TrialCompleted || used != 5 {
		t.Fatalf("trials %+v, used %d; want the completed retry and 5 requests", trials, used)
	}
	if raw, _ := os.ReadFile(
		path,
	); strings.Contains(string(raw), `{"arm":"base`) ||
		!strings.HasSuffix(string(raw), "\n") {
		t.Fatal("the partial line was not truncated")
	}
}

func TestKeepLogOnlyForCoreAndHarnessFailures(t *testing.T) {
	t.Parallel()
	trialDir, out := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "records"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trialDir, "household.log"), []byte("core failed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for outcome, kept := range map[TrialOutcome]bool{
		TrialAgentError: true, TrialHarnessError: true,
		TrialProviderError: false, TrialCompleted: false, TrialOverBudget: false,
	} {
		name, err := keepLog(outcome, trialDir, out, "s", string(outcome), 0)
		if err != nil {
			t.Fatal(err)
		}
		if kept != (name != "") {
			t.Fatalf("%s: kept log %q", outcome, name)
		}
		if kept {
			if raw, _ := os.ReadFile(filepath.Join(out, name)); string(raw) != "core failed\n" {
				t.Fatalf("%s: log content %q", outcome, raw)
			}
		}
	}
}

func TestRescoreUsesRecordsAndRefusesChangedScenarios(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "records"), 0o750); err != nil {
		t.Fatal(err)
	}
	scenario := Scenario{ID: "a", SHA256: "sha-a", Checks: []Check{{Kind: CheckReplyMatches, Pattern: "25"}}}
	manifest, _ := json.Marshal(Manifest{Scenarios: []ManifestScenario{{ID: "a", SHA256: "sha-a"}}})
	record := trialRecord(trialSpec{reply: "It is 20 °C."})
	record.ScenarioID = "a"
	full, _ := json.Marshal(record)
	// The stored score is stale: it says the trial passed.
	stored, _ := json.Marshal(ArmTrial{
		Arm: ArmCandidate, Scored: ScoredTrial{Record: slim(record), Scored: true, Passed: true},
		Judgment: &Judgment{Grades: []CriterionGrade{{Criterion: "c", Grade: GradePass}}},
	})
	for name, content := range map[string][]byte{
		"manifest.json": manifest, "trials.jsonl": append(stored, '\n'),
		filepath.Join("records", recordName("a", ArmCandidate, 0)): full,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	changed := scenario
	changed.SHA256 = "sha-b"
	if err := Rescore(dir, []Scenario{changed}); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("a changed scenario was accepted: %v", err)
	}
	if err := Rescore(dir, []Scenario{scenario}); err != nil {
		t.Fatal(err)
	}
	_, trials, err := LoadRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(trials) != 1 || trials[0].Scored.Passed || trials[0].Judgment == nil {
		t.Fatalf("rescored trials = %+v; want one failed trial that keeps its judgment", trials)
	}
}
