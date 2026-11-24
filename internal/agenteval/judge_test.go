package agenteval //nolint:testpackage // Uses unexported judge construction and facts.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// answerModel answers each judge request with the reply its function picks.
type answerModel func(prompt string) (string, error)

func (answer answerModel) Generate(
	_ context.Context,
	in []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	content, err := answer(in[len(in)-1].Content)
	if err != nil {
		return nil, err
	}
	return schema.AssistantMessage(content, nil), nil
}

func (answerModel) Stream(
	context.Context,
	[]*schema.Message,
	...model.Option,
) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("unused")
}

func fakeJudge(answer answerModel) *Judge {
	return &Judge{Settings: ModelSettings{Provider: "openai", Model: "judge"}, model: answer}
}

func TestCheckJudgeModelRefusesArmModels(t *testing.T) {
	t.Parallel()
	arms := []Arm{
		{Name: ArmBaseline, Settings: ModelSettings{Provider: "opencode-go", Model: "glm-5.3"}},
		{Name: ArmCandidate, Settings: ModelSettings{Provider: "opencode-go", Model: "kimi-k3"}},
	}
	if err := CheckJudgeModel(ModelSettings{Provider: "opencode-go", Model: "kimi-k3"}, arms); err == nil ||
		!strings.Contains(err.Error(), "candidate") {
		t.Fatalf("the candidate's model was accepted as judge: %v", err)
	}
	if err := CheckJudgeModel(ModelSettings{Provider: "openai", Model: "glm-5.3"}, arms); err != nil {
		t.Fatalf("a different provider was refused: %v", err)
	}
}

func TestJudgeGradesAndFailsSoft(t *testing.T) {
	t.Parallel()
	input := JudgeInput{Criteria: []string{"names the light", "makes no other claim", "is short"}, Reply: "On."}
	fenced := "```json\n{\"grades\":[{\"criterion\":1,\"grade\":\"PASS\",\"reason\":\"ok\"}," +
		"{\"criterion\":2,\"grade\":\"maybe\"},{\"criterion\":9,\"grade\":\"fail\"}]}\n```"
	judgment := fakeJudge(func(prompt string) (string, error) {
		if !strings.Contains(prompt, "3. is short") || !strings.Contains(prompt, "On.") {
			t.Errorf("prompt lacks the rubric or reply:\n%s", prompt)
		}
		return fenced, nil
	}).Grade(t.Context(), input)
	if judgment.Error != "" || len(judgment.Grades) != 3 {
		t.Fatalf("judgment = %+v", judgment)
	}
	want := []Grade{GradePass, GradeUnsure, GradeUnsure}
	for index, grade := range judgment.Grades {
		if grade.Grade != want[index] || grade.Criterion != input.Criteria[index] {
			t.Errorf("grade %d = %+v, want %s", index, grade, want[index])
		}
	}
	failed := fakeJudge(
		func(string) (string, error) { return "", errors.New("secret provider text") },
	).Grade(t.Context(), input)
	if failed.Error != "judge request failed: transport" {
		t.Fatalf("provider failure = %q", failed.Error)
	}
	garbled := fakeJudge(func(string) (string, error) { return "I think it passes.", nil }).Grade(t.Context(), input)
	if !strings.HasPrefix(garbled.Error, "unreadable judge response") {
		t.Fatalf("garbled response = %+v", garbled)
	}
}

func TestJudgmentsNeverChangeTheVerdict(t *testing.T) {
	t.Parallel()
	failing := &Judgment{Grades: []CriterionGrade{{Criterion: "c", Grade: GradeFail}}}
	trials := append(armTrials(ArmBaseline, 8, 10, 0, 0), armTrials(ArmCandidate, 8, 10, 0, 0)...)
	for index := range trials {
		if trials[index].Arm == ArmCandidate {
			trials[index].Judgment = failing
		}
	}
	summary := Summarize([]Scenario{{ID: "a"}}, trials, DefaultRule(10), false, 0)
	if summary.Verdict != VerdictPass {
		t.Fatalf("judge grades changed the verdict to %s", summary.Verdict)
	}
	tally := summary.Scenarios[0].Arms[ArmCandidate].Judge
	if len(tally) != 1 || tally[0].Fail != 10 {
		t.Fatalf("candidate judge tally = %+v", tally)
	}
}

func TestJudgeInputSummarizesOutcomes(t *testing.T) {
	t.Parallel()
	record := trialRecord(trialSpec{
		reply: "Turned it on.", artifacts: []string{view}, tools: []string{"execute_entity_command"},
		commands: []string{`{"id":"cmd_1","entity_id":"ent_lamp","operation":"set","parameters":{"value":true}}`},
	})
	record.Turns[0].Text = "Turn on the lamp."
	if _, ok := JudgeInputFor(Scenario{}, record); ok {
		t.Fatal("a scenario without a rubric was judged")
	}
	input, ok := JudgeInputFor(Scenario{Judge: &Rubric{Criteria: []string{"c"}}}, record)
	if !ok {
		t.Fatal("a completed trial was not judged")
	}
	summary := strings.Join(input.Trajectory, "\n")
	for _, want := range []string{
		"called tools: execute_entity_command",
		`sent Command set to sim-healthy/lamp/power with {"value":true}`,
		`presented a view: "Temps" with cards TimeSeries, Grid, Heading, Metric, Stack`,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("trajectory lacks %q:\n%s", want, summary)
		}
	}
	if input.Reply != "Turned it on." || input.Turns[0] != "Turn on the lamp." {
		t.Fatalf("input = %+v", input)
	}
}

func TestCalibrateReportsAgreementPerCriterion(t *testing.T) {
	t.Parallel()
	labels := []LabeledTrial{
		{ID: "x/good", Scenario: "x", Criteria: []string{"a", "b"}, Reply: "good",
			Labels: []Grade{GradePass, GradePass}},
		{ID: "x/bad", Scenario: "x", Criteria: []string{"a", "b"}, Reply: "bad",
			Labels: []Grade{GradeFail, GradeFail}},
	}
	// The judge passes everything on criterion a and is unsure on b.
	judge := fakeJudge(func(string) (string, error) {
		return `{"grades":[{"criterion":1,"grade":"pass"},{"criterion":2,"grade":"unsure"}]}`, nil
	})
	result := Calibrate(t.Context(), judge, labels, 0)
	if result.Labeled != 4 || result.Agreed != 1 || result.Unsure != 2 || len(result.Mismatches) != 1 {
		t.Fatalf("calibration = %+v", result)
	}
	if a := result.Criteria[0]; a.Criterion != "a" || a.Agreed != 1 || a.Disagreed != 1 {
		t.Fatalf("criterion a = %+v", a)
	}
}

func TestJudgeLabelsMatchRubrics(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "testdata", "agenteval")
	labels, err := LoadLabels(filepath.Join(root, "judge-labels.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := LoadScenarios(filepath.Join(root, "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckLabels(labels, scenarios); err != nil {
		t.Fatal(err)
	}
	labels[0].Criteria = []string{"reworded"}
	if err = CheckLabels(labels, scenarios); err == nil || !strings.Contains(err.Error(), "relabel") {
		t.Fatalf("a changed rubric was accepted: %v", err)
	}
}
