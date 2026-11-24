package agenteval

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mholtzscher/hearth/internal/modules/agent"
)

// Grade is a judge's assessment of one rubric criterion.
type Grade string

const (
	GradePass   Grade = "pass"
	GradeFail   Grade = "fail"
	GradeUnsure Grade = "unsure"
)

// CriterionGrade is one rubric criterion's grade and the judge's reason.
type CriterionGrade struct {
	Criterion string `json:"criterion"`
	Grade     Grade  `json:"grade"`
	Reason    string `json:"reason,omitempty"`
}

// Judgment is the judge's report-only assessment of one trial. Verdicts never
// read it.
type Judgment struct {
	Grades []CriterionGrade `json:"grades,omitempty"`
	// Error is a safe description of a failed judge request; never provider text.
	Error string `json:"error,omitempty"`
}

// JudgeInput is everything the judge sees for one conversation.
type JudgeInput struct {
	Criteria   []string `json:"criteria"`
	Turns      []string `json:"turns"`
	Trajectory []string `json:"trajectory"`
	Reply      string   `json:"reply"`
}

// Judge grades replies against scenario rubrics with a model that differs
// from every Agent model in the run.
type Judge struct {
	Settings ModelSettings
	model    model.BaseChatModel
	// session groups this judge's requests for providers that route by
	// conversation; no Agent conversation shares it.
	session string
}

// NewJudge connects to the judge model with the same provider clients and
// credentials the Agent uses.
func NewJudge(settings ModelSettings) (*Judge, error) {
	chat, err := providerModel(settings)
	if err != nil {
		return nil, fmt.Errorf("judge model: %w", err)
	}
	return &Judge{Settings: settings, model: chat, session: "agenteval-judge-" + rand.Text()}, nil
}

// CheckJudgeModel refuses a judge that shares a provider and model with any arm,
// since a model grading its own replies inflates agreement.
func CheckJudgeModel(judge ModelSettings, arms []Arm) error {
	for _, arm := range arms {
		if arm.Settings.Provider == judge.Provider && arm.Settings.Model == judge.Model {
			return fmt.Errorf("judge model %s/%s is the %s arm's Agent model; choose a different judge",
				judge.Provider, judge.Model, arm.Name)
		}
	}
	return nil
}

const judgeInstructions = `You are the Hearth agent-evaluation judge. You grade one conversation between a
household member and Hearth's home-automation Agent against a rubric.

You see the user's messages, a summary of what the Agent did (tools called,
Commands sent to devices, views and Automation drafts presented), and the
Agent's final reply. Grade each numbered criterion independently:
- "pass" when the conversation clearly satisfies it,
- "fail" when it clearly does not,
- "unsure" when the material shown is not enough to decide.
Judge only what is shown. Do not reward length or politeness.

Answer with JSON only, no prose and no code fence:
{"grades":[{"criterion":1,"grade":"pass","reason":"one short sentence"}]}`

// Grade asks the judge model to grade every criterion. It never returns an
// error: failures are recorded in the Judgment so the run continues.
func (judge *Judge) Grade(ctx context.Context, input JudgeInput) Judgment {
	response, err := judge.model.Generate(agent.WithModelConversation(ctx, judge.session), judgeMessages(input))
	if err != nil {
		if ctx.Err() != nil {
			return Judgment{Error: "judge request canceled"}
		}
		return Judgment{Error: "judge request failed: " + providerErrorClass(err)}
	}
	grades, err := parseGrades(response.Content, input.Criteria)
	if err != nil {
		return Judgment{Error: "unreadable judge response: " + err.Error()}
	}
	return Judgment{Grades: grades}
}

func judgeMessages(input JudgeInput) []*schema.Message {
	return []*schema.Message{schema.SystemMessage(judgeInstructions), schema.UserMessage(input.prompt())}
}

func (input JudgeInput) prompt() string {
	var builder strings.Builder
	builder.WriteString("Rubric:\n")
	for index, criterion := range input.Criteria {
		fmt.Fprintf(&builder, "%d. %s\n", index+1, criterion)
	}
	builder.WriteString("\nUser messages:\n")
	for index, turn := range input.Turns {
		fmt.Fprintf(&builder, "%d. %s\n", index+1, turn)
	}
	builder.WriteString("\nWhat the Agent did:\n")
	if len(input.Trajectory) == 0 {
		builder.WriteString("- nothing besides replying\n")
	}
	for _, step := range input.Trajectory {
		fmt.Fprintf(&builder, "- %s\n", step)
	}
	builder.WriteString("\nFinal reply:\n")
	if input.Reply == "" {
		builder.WriteString("(empty)\n")
	} else {
		builder.WriteString(input.Reply + "\n")
	}
	return builder.String()
}

// parseGrades reads the judge's JSON, tolerating a surrounding code fence.
// Criteria the judge skipped or graded with an unknown value become unsure.
func parseGrades(content string, criteria []string) ([]CriterionGrade, error) {
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return nil, errors.New("no JSON object")
	}
	var body struct {
		Grades []struct {
			Criterion int    `json:"criterion"`
			Grade     string `json:"grade"`
			Reason    string `json:"reason"`
		} `json:"grades"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &body); err != nil {
		return nil, errors.New("invalid JSON")
	}
	grades := make([]CriterionGrade, len(criteria))
	for index, criterion := range criteria {
		grades[index] = CriterionGrade{Criterion: criterion, Grade: GradeUnsure, Reason: "not graded"}
	}
	for _, item := range body.Grades {
		if item.Criterion < 1 || item.Criterion > len(criteria) {
			continue
		}
		grade := Grade(strings.ToLower(strings.TrimSpace(item.Grade)))
		if !slices.Contains([]Grade{GradePass, GradeFail, GradeUnsure}, grade) {
			grade = GradeUnsure
		}
		grades[item.Criterion-1].Grade, grades[item.Criterion-1].Reason = grade, item.Reason
	}
	return grades, nil
}

// JudgeInputFor summarizes a trial for the judge. It reports false when the
// scenario has no rubric or the trial did not complete.
func JudgeInputFor(scenario Scenario, record TrialRecord) (JudgeInput, bool) {
	if scenario.Judge == nil || record.Outcome != TrialCompleted {
		return JudgeInput{}, false
	}
	f, err := extractFacts(record)
	if err != nil {
		return JudgeInput{}, false
	}
	input := JudgeInput{Criteria: scenario.Judge.Criteria, Reply: f.reply}
	for _, turn := range record.Turns {
		input.Turns = append(input.Turns, turn.Text)
	}
	if len(f.toolCalls) > 0 {
		input.Trajectory = append(input.Trajectory, "called tools: "+strings.Join(f.toolCalls, ", "))
	}
	names := reverseEntities(record.Inventory)
	for _, command := range f.newCommands {
		entity := fmt.Sprint(command["entity_id"])
		if name, ok := names[entity]; ok {
			entity = name
		}
		parameters, _ := json.Marshal(command["parameters"])
		input.Trajectory = append(input.Trajectory,
			fmt.Sprintf("sent Command %v to %s with %s", command["operation"], entity, parameters))
	}
	for _, view := range f.views {
		input.Trajectory = append(input.Trajectory, "presented a view: "+describeView(view))
	}
	for _, automation := range f.automations {
		input.Trajectory = append(
			input.Trajectory,
			fmt.Sprintf("presented an Automation card in %v mode", automation["mode"]),
		)
	}
	return input, true
}

// describeView names a view's title and card types, in document order.
func describeView(artifact map[string]any) string {
	definition, _ := artifact["definition"].(map[string]any)
	title := fmt.Sprint(definition["title"])
	spec, _ := definition["spec"].(map[string]any)
	elements, _ := spec["elements"].(map[string]any)
	keys := make([]string, 0, len(elements))
	for key := range elements {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	types := make([]string, 0, len(keys))
	for _, key := range keys {
		if element, ok := elements[key].(map[string]any); ok {
			types = append(types, fmt.Sprint(element["type"]))
		}
	}
	return fmt.Sprintf("%q with cards %s", title, strings.Join(types, ", "))
}

// CriterionTally counts one criterion's grades across an arm's trials.
type CriterionTally struct {
	Criterion string `json:"criterion"`
	Pass      int    `json:"pass"`
	Fail      int    `json:"fail"`
	Unsure    int    `json:"unsure"`
}

// tallyJudgments counts grades per criterion, in first-seen order, and failed
// judge requests separately.
func tallyJudgments(trials []ArmTrial) ([]CriterionTally, int) {
	var tallies []CriterionTally
	positions := map[string]int{}
	errored := 0
	for _, trial := range trials {
		if trial.Judgment == nil {
			continue
		}
		if trial.Judgment.Error != "" {
			errored++
			continue
		}
		for _, grade := range trial.Judgment.Grades {
			index, ok := positions[grade.Criterion]
			if !ok {
				index = len(tallies)
				positions[grade.Criterion] = index
				tallies = append(tallies, CriterionTally{Criterion: grade.Criterion})
			}
			switch grade.Grade {
			case GradePass:
				tallies[index].Pass++
			case GradeFail:
				tallies[index].Fail++
			case GradeUnsure:
				tallies[index].Unsure++
			}
		}
	}
	return tallies, errored
}

// LabeledTrial is one hand-labeled conversation for calibration.
type LabeledTrial struct {
	JudgeInput

	ID       string `json:"id"`
	Scenario string `json:"scenario"`
	// Labels holds the human grade for each criterion: pass or fail.
	Labels []Grade `json:"labels"`
	// Note says where the conversation came from.
	Note string `json:"note,omitempty"`
}

// CriterionAgreement compares the judge with hand labels for one criterion.
type CriterionAgreement struct {
	Scenario  string `json:"scenario"`
	Criterion string `json:"criterion"`
	Labeled   int    `json:"labeled"`
	Agreed    int    `json:"agreed"`
	Disagreed int    `json:"disagreed"`
	Unsure    int    `json:"unsure"`
	Errors    int    `json:"errors"`
}

// Calibration is the judge's agreement with hand labels.
type Calibration struct {
	Judge      ModelSettings        `json:"judge"`
	Criteria   []CriterionAgreement `json:"criteria"`
	Labeled    int                  `json:"labeled"`
	Agreed     int                  `json:"agreed"`
	Unsure     int                  `json:"unsure"`
	Errors     int                  `json:"errors"`
	Mismatches []string             `json:"mismatches,omitempty"`
}

// LoadLabels reads a JSON Lines file of hand-labeled conversations.
func LoadLabels(path string) ([]LabeledTrial, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var labels []LabeledTrial
	for number, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var label LabeledTrial
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&label); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, number+1, err)
		}
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("%s: no labeled conversations", path)
	}
	return labels, nil
}

// CheckLabels verifies each labeled trial still matches its scenario's rubric,
// so relabeling is required whenever a rubric changes.
func CheckLabels(labels []LabeledTrial, scenarios []Scenario) error {
	byID := map[string]Scenario{}
	for _, scenario := range scenarios {
		byID[scenario.ID] = scenario
	}
	for _, label := range labels {
		scenario, ok := byID[label.Scenario]
		switch {
		case !ok:
			return fmt.Errorf("label %s: unknown scenario %q", label.ID, label.Scenario)
		case scenario.Judge == nil || !slices.Equal(scenario.Judge.Criteria, label.Criteria):
			return fmt.Errorf("label %s: criteria differ from %s's rubric; relabel it", label.ID, label.Scenario)
		case len(label.Labels) != len(label.Criteria):
			return fmt.Errorf("label %s: %d labels for %d criteria", label.ID, len(label.Labels), len(label.Criteria))
		}
		for _, grade := range label.Labels {
			if grade != GradePass && grade != GradeFail {
				return fmt.Errorf("label %s: labels must be pass or fail", label.ID)
			}
		}
	}
	return nil
}

// Calibrate grades every labeled trial, waiting minGap between requests, and
// reports agreement per criterion. It stops early when ctx ends.
func Calibrate(ctx context.Context, judge *Judge, labels []LabeledTrial, minGap time.Duration) Calibration {
	result := Calibration{Judge: judge.Settings}
	index := map[string]int{}
	for position, label := range labels {
		if position > 0 && sleep(ctx, minGap) != nil {
			break
		}
		judgment := judge.Grade(ctx, label.JudgeInput)
		for position, criterion := range label.Criteria {
			key := label.Scenario + "\x00" + criterion
			if _, ok := index[key]; !ok {
				index[key] = len(result.Criteria)
				result.Criteria = append(
					result.Criteria,
					CriterionAgreement{Scenario: label.Scenario, Criterion: criterion},
				)
			}
			item := &result.Criteria[index[key]]
			item.Labeled++
			result.Labeled++
			switch {
			case judgment.Error != "":
				item.Errors++
				result.Errors++
			case judgment.Grades[position].Grade == GradeUnsure:
				item.Unsure++
				result.Unsure++
			case judgment.Grades[position].Grade == label.Labels[position]:
				item.Agreed++
				result.Agreed++
			default:
				item.Disagreed++
				result.Mismatches = append(result.Mismatches, fmt.Sprintf("%s #%d: labeled %s, judge %s (%s)",
					label.ID, position+1, label.Labels[position], judgment.Grades[position].Grade,
					judgment.Grades[position].Reason))
			}
		}
	}
	return result
}
