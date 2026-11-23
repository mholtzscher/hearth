package agenteval

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed report.html
var reportTemplate string

// reportData is embedded in report.html.
type reportData struct {
	Manifest  Manifest         `json:"manifest"`
	Summary   Summary          `json:"summary"`
	Scenarios []reportScenario `json:"scenarios"`
	Trials    []reportTrial    `json:"trials"`
}

type reportScenario struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Turns  []string `json:"turns"`
	Checks []string `json:"checks"`
}

type reportTrial struct {
	Arm      string          `json:"arm"`
	Trial    int             `json:"trial"`
	Scenario string          `json:"scenario"`
	Outcome  TrialOutcome    `json:"outcome"`
	Error    string          `json:"error,omitempty"`
	Scored   bool            `json:"scored"`
	Passed   bool            `json:"passed"`
	Results  []CheckResult   `json:"results"`
	Requests []reportRequest `json:"requests"`
	Reply    string          `json:"reply"`
	History  json.RawMessage `json:"history,omitempty"`
	Record   string          `json:"record"`
	Judgment *Judgment       `json:"judgment,omitempty"`
}

type reportRequest struct {
	Turn         int      `json:"turn"`
	Tools        int      `json:"tools"`
	ToolCalls    []string `json:"tool_calls"`
	InputTokens  int      `json:"input_tokens"`
	CachedTokens int      `json:"cached_tokens"`
	OutputTokens int      `json:"output_tokens"`
	Seconds      float64  `json:"seconds"`
	Error        string   `json:"error,omitempty"`
}

// WriteReport rebuilds summary.json and report.html from a run directory and
// returns the report path. Transcripts are the public conversation history
// from records/; each request's full model input stays in records/.
func WriteReport(dir string) (string, error) {
	manifest, trials, err := LoadRun(dir)
	if err != nil {
		return "", err
	}
	previous := Summary{}
	if raw, readErr := os.ReadFile(filepath.Join(dir, "summary.json")); readErr == nil {
		_ = json.Unmarshal(raw, &previous)
	}
	scenarios := make([]Scenario, 0, len(manifest.Scenarios))
	data := reportData{Manifest: manifest}
	index := map[string]int{}
	for _, item := range manifest.Scenarios {
		scenarios = append(scenarios, Scenario{ID: item.ID, Title: item.Title, Tags: item.Tags})
		index[item.ID] = len(data.Scenarios)
		data.Scenarios = append(data.Scenarios, reportScenario{ID: item.ID, Title: item.Title})
	}
	for _, trial := range trials {
		record := trial.Scored.Record
		if position, ok := index[record.ScenarioID]; ok && len(data.Scenarios[position].Turns) == 0 {
			for _, turn := range record.Turns {
				data.Scenarios[position].Turns = append(data.Scenarios[position].Turns, turn.Text)
			}
			for _, result := range trial.Scored.Results {
				data.Scenarios[position].Checks = append(data.Scenarios[position].Checks, result.Label)
			}
		}
		item, itemErr := reportItem(dir, trial)
		if itemErr != nil {
			return "", itemErr
		}
		data.Trials = append(data.Trials, item)
	}
	notRun := max(0, len(manifest.Scenarios)*manifest.Trials*len(manifest.Arms)-len(trials))
	data.Summary = Summarize(scenarios, trials, manifest.Rule, previous.RunCapReached, notRun)
	raw, err := json.MarshalIndent(data.Summary, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "summary.json"), raw, 0o600); err != nil {
		return "", err
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	html := strings.Replace(reportTemplate, "@@DATA@@", strings.ReplaceAll(string(payload), "</", `<\/`), 1)
	path := filepath.Join(dir, "report.html")
	return path, os.WriteFile(path, []byte(html), 0o600)
}

// reportItem projects one trial, adding its transcript from records/.
func reportItem(dir string, trial ArmTrial) (reportTrial, error) {
	record := trial.Scored.Record
	item := reportTrial{
		Arm: trial.Arm, Trial: trial.Trial, Scenario: record.ScenarioID, Outcome: record.Outcome,
		Error: record.Error, Scored: trial.Scored.Scored, Passed: trial.Scored.Passed, Results: trial.Scored.Results,
		Record:   filepath.Join("records", recordName(record.ScenarioID, trial.Arm, trial.Trial)),
		Judgment: trial.Judgment,
	}
	full, err := readRecord(dir, record.ScenarioID, trial.Arm, trial.Trial)
	switch {
	case err == nil:
		item.History = full.History
		item.Reply = lastReply(full)
	case !errors.Is(err, os.ErrNotExist):
		return item, err
	}
	for _, request := range record.Requests {
		item.Requests = append(item.Requests, reportRequest{
			Turn: request.Turn, Tools: len(request.Tools), ToolCalls: request.ToolCalls,
			InputTokens: request.InputTokens, CachedTokens: request.CachedTokens,
			OutputTokens: request.OutputTokens, Seconds: request.ModelSeconds, Error: request.Error,
		})
	}
	return item, nil
}

func recordName(scenario, arm string, trial int) string {
	return fmt.Sprintf("%s-%s-%02d.json", scenario, arm, trial)
}

func readRecord(dir, scenario, arm string, trial int) (TrialRecord, error) {
	var record TrialRecord
	raw, err := os.ReadFile(filepath.Join(dir, "records", recordName(scenario, arm, trial)))
	if err != nil {
		return record, err
	}
	return record, json.Unmarshal(raw, &record)
}

func lastReply(record TrialRecord) string {
	for _, turn := range slices.Backward(record.Turns) {
		var body struct {
			Reply string `json:"reply"`
		}
		if turn.Status == http.StatusOK && json.Unmarshal(turn.Response, &body) == nil {
			return body.Reply
		}
	}
	return ""
}
