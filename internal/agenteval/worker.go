package agenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	"github.com/mholtzscher/hearth/internal/modules/agent"
)

// WorkerProtocol versions TrialRecord and the worker CLI. The driver refuses
// to compare workers whose protocol differs from its own.
const WorkerProtocol = 1

// turnTimeout bounds one Agent turn, including paced model requests.
const turnTimeout = 15 * time.Minute

// TrialOutcome classifies how a trial ended.
type TrialOutcome string

const (
	// TrialCompleted ran every turn; checks decide pass or fail.
	TrialCompleted TrialOutcome = "completed"
	// TrialAgentError means Core rejected a turn for an Agent failure. Scored as failed.
	TrialAgentError TrialOutcome = "agent_error"
	// TrialOverBudget means the Agent exceeded the request cap. Scored as failed.
	TrialOverBudget TrialOutcome = "over_budget"
	// TrialProviderError is incomplete, not failed: the provider, not the Agent, failed.
	TrialProviderError TrialOutcome = "provider_error"
	// TrialHarnessError is incomplete and needs investigation.
	TrialHarnessError TrialOutcome = "harness_error"
)

// Scored reports whether checks apply to the outcome.
func (outcome TrialOutcome) Scored() bool {
	return outcome == TrialCompleted || outcome == TrialAgentError || outcome == TrialOverBudget
}

// ModelSettings identifies the Agent model under test.
type ModelSettings struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	// BaseURL overrides the provider endpoint, e.g. a local fake provider.
	BaseURL string `json:"base_url,omitempty"`
}

// TrialRecord is everything the driver needs to score one trial.
type TrialRecord struct {
	Protocol    int               `json:"protocol"`
	Revision    string            `json:"revision"`
	ScenarioID  string            `json:"scenario_id"`
	ScenarioSHA string            `json:"scenario_sha256"`
	Trial       int               `json:"trial"`
	Model       ModelSettings     `json:"model"`
	Outcome     TrialOutcome      `json:"outcome"`
	Error       string            `json:"error,omitempty"`
	Inventory   Inventory         `json:"inventory"`
	Turns       []RecordedTurn    `json:"turns"`
	History     json.RawMessage   `json:"history,omitempty"`
	Requests    []RecordedRequest `json:"requests"`
	Before      Snapshot          `json:"before"`
	After       Snapshot          `json:"after"`
	StartedAt   time.Time         `json:"started_at"`
	Seconds     float64           `json:"seconds"`
}

// RecordedTurn is one user message and Core's response.
type RecordedTurn struct {
	Text     string          `json:"text"`
	Request  json.RawMessage `json:"request"`
	Status   int             `json:"status"`
	Response json.RawMessage `json:"response,omitempty"`
}

// Snapshot is household state the checks compare.
type Snapshot struct {
	Commands    []json.RawMessage `json:"commands"`
	Dashboards  []json.RawMessage `json:"dashboards"`
	Automations []json.RawMessage `json:"automations"`
}

// TrialOptions configures one trial.
type TrialOptions struct {
	Scenario        Scenario
	Trial           int
	SimulatorConfig string
	Settings        ModelSettings
	// Model replaces the provider model, as the self-test does with a scripted model.
	Model       model.ToolCallingChatModel
	MaxRequests int
	MinGap      time.Duration
	Dir         string
	Revision    string
}

// RunTrial runs one scenario trial. Failures are classified in the record's
// Outcome rather than returned.
func RunTrial(ctx context.Context, options TrialOptions) TrialRecord {
	record := runTrial(ctx, options, TrialRecord{
		Protocol: WorkerProtocol, Revision: options.Revision, ScenarioID: options.Scenario.ID,
		ScenarioSHA: options.Scenario.SHA256, Trial: options.Trial, Model: options.Settings,
		StartedAt: time.Now().UTC(),
	})
	record.Seconds = time.Since(record.StartedAt).Seconds()
	return record
}

func runTrial(ctx context.Context, options TrialOptions, record TrialRecord) TrialRecord {
	inner := options.Model
	if inner == nil {
		built, err := providerModel(options.Settings)
		if err != nil {
			record.Outcome, record.Error = TrialHarnessError, err.Error()
			return record
		}
		inner = built
	}
	limit := options.MaxRequests
	if options.Scenario.MaxRequests > 0 {
		limit = options.Scenario.MaxRequests
	}
	recorder := NewRecorder(inner, limit, options.MinGap)
	household, err := StartHousehold(ctx, HouseholdOptions{
		SimulatorConfig: options.SimulatorConfig, Dir: options.Dir, Model: recorder,
		Agent: hearthd.AgentConfig{
			Provider: agent.Provider(options.Settings.Provider), Model: options.Settings.Model,
			ReasoningEffort: options.Settings.ReasoningEffort,
		},
	})
	if err != nil {
		record.Outcome, record.Error = TrialHarnessError, "start household: "+err.Error()
		return record
	}
	runErr := runTurns(ctx, household, recorder, options.Scenario, &record)
	record.Requests = recorder.Requests()
	stopErr := household.Stop()
	record.Outcome, record.Error = classify(record, recorder, runErr, stopErr)
	return record
}

func providerModel(settings ModelSettings) (model.ToolCallingChatModel, error) {
	config := hearthd.NormalizeConfig(hearthd.Config{Agent: hearthd.AgentConfig{
		Provider: agent.Provider(settings.Provider), Model: settings.Model, ReasoningEffort: settings.ReasoningEffort,
	}})
	key, err := config.LoadAgentAPIKey()
	if err != nil {
		return nil, fmt.Errorf("model credential: %w", err)
	}
	modelConfig := agent.ModelConfig{
		Provider: config.Agent.Provider.Effective(), APIKey: key, Model: config.EffectiveAgentModel(),
		ReasoningEffort: config.Agent.ReasoningEffort, BaseURL: settings.BaseURL,
	}
	if err = modelConfig.ValidateSettings(); err != nil {
		return nil, err
	}
	return agent.NewChatModel(modelConfig), nil
}

func runTurns(
	ctx context.Context, household *Household, recorder *Recorder, scenario Scenario, record *TrialRecord,
) error {
	for _, pin := range scenario.Setup.Pin {
		if err := household.Pin(ctx, pin); err != nil {
			return err
		}
	}
	if err := seed(ctx, household, scenario); err != nil {
		return err
	}
	record.Inventory = household.Inventory
	// Only the self-test's ScriptedModel resolves placeholders; provider models
	// need no inventory.
	if scripted, ok := recorder.inner.(interface{ SetInventory(Inventory) }); ok {
		scripted.SetInventory(household.Inventory)
	}
	before, err := household.snapshot(ctx)
	if err != nil {
		return err
	}
	record.Before = before
	var conversation struct {
		ID string `json:"id"`
	}
	if err = household.postJSON(ctx, household.CoreURL+"/v1/agent/conversations", nil, &conversation); err != nil {
		return fmt.Errorf("create conversation: %w", err)
	}
	path := household.CoreURL + "/v1/agent/conversations/" + url.PathEscape(conversation.ID) + "/messages"
	for index, turn := range scenario.Turns {
		recorder.SetTurn(index)
		body, bodyErr := turnBody(turn, household.Inventory)
		if bodyErr != nil {
			return bodyErr
		}
		raw, _ := json.Marshal(body)
		recorded := RecordedTurn{Text: turn.Text, Request: raw}
		turnCtx, cancel := context.WithTimeout(ctx, turnTimeout)
		var response json.RawMessage
		sendErr := household.postJSON(turnCtx, path, body, &response)
		cancel()
		recorded.Response = response
		recorded.Status = http.StatusOK
		if status, ok := errors.AsType[*StatusError](sendErr); ok {
			recorded.Status = status.Status
		} else if sendErr != nil {
			record.Turns = append(record.Turns, recorded)
			return fmt.Errorf("send turn %d: %w", index, sendErr)
		}
		record.Turns = append(record.Turns, recorded)
		if recorded.Status != http.StatusOK {
			break
		}
	}
	var history json.RawMessage
	if err = household.getJSON(ctx, path, &history); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	record.History = history
	after, err := household.snapshot(ctx)
	record.After = after
	return err
}

func turnBody(turn Turn, inventory Inventory) (map[string]any, error) {
	body := map[string]any{"text": turn.Text}
	if turn.SelectDashboard == "" {
		return body, nil
	}
	saved, ok := inventory.Dashboards[turn.SelectDashboard]
	if !ok {
		return nil, fmt.Errorf("selected dashboard %q was not seeded", turn.SelectDashboard)
	}
	var stored struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(saved.Body, &stored); err != nil {
		return nil, err
	}
	body["view_context"] = map[string]any{
		"definition": stored.Definition, "source_dashboard_id": saved.ID, "source_revision": saved.Revision,
	}
	return body, nil
}

func seed(ctx context.Context, household *Household, scenario Scenario) error {
	for _, dashboard := range scenario.Setup.Dashboards {
		definition, err := Resolve(dashboard.Definition, household.Inventory)
		if err != nil {
			return fmt.Errorf("seed dashboard %s: %w", dashboard.Name, err)
		}
		var saved json.RawMessage
		body := map[string]json.RawMessage{"definition": json.RawMessage(definition)}
		if err = household.postJSON(ctx, household.CoreURL+"/v1/dashboards", body, &saved); err != nil {
			return fmt.Errorf("seed dashboard %s: %w", dashboard.Name, err)
		}
		household.Inventory.Dashboards[dashboard.Name], err = savedRef(saved)
		if err != nil {
			return err
		}
	}
	for _, automation := range scenario.Setup.Automations {
		definition, err := Resolve(automation.Definition, household.Inventory)
		if err != nil {
			return fmt.Errorf("seed automation %s: %w", automation.Name, err)
		}
		var saved json.RawMessage
		if err = household.postJSON(
			ctx,
			household.CoreURL+"/v1/automations",
			json.RawMessage(definition),
			&saved,
		); err != nil {
			return fmt.Errorf("seed automation %s: %w", automation.Name, err)
		}
		household.Inventory.Automations[automation.Name], err = savedRef(saved)
		if err != nil {
			return err
		}
	}
	return nil
}

func savedRef(raw json.RawMessage) (Saved, error) {
	var head struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.ID == "" {
		return Saved{}, errors.New("seeded record has no id")
	}
	return Saved{ID: head.ID, Revision: head.Revision, Body: raw}, nil
}

func (household *Household) snapshot(ctx context.Context) (Snapshot, error) {
	var snapshot Snapshot
	var err error
	if snapshot.Commands, err = household.page(ctx, "/v1/commands"); err != nil {
		return snapshot, err
	}
	if snapshot.Dashboards, err = household.page(ctx, "/v1/dashboards"); err != nil {
		return snapshot, err
	}
	snapshot.Automations, err = household.page(ctx, "/v1/automations")
	return snapshot, err
}

// page reads every item of a cursor-paginated collection.
func (household *Household) page(ctx context.Context, path string) ([]json.RawMessage, error) {
	items := []json.RawMessage{}
	cursor := ""
	for range 1000 {
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var page struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := household.getJSON(ctx, household.CoreURL+path+"?"+query.Encode(), &page); err != nil {
			return nil, fmt.Errorf("list %s: %w", path, err)
		}
		items = append(items, page.Items...)
		if page.NextCursor == "" {
			return items, nil
		}
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("list %s: too many pages", path)
}

// classify fails closed: a turn the recorder never saw, or a response without
// usage, means the measurement did not happen.
func classify(record TrialRecord, recorder *Recorder, runErr, stopErr error) (TrialOutcome, string) {
	switch {
	case recorder.OverBudget():
		return TrialOverBudget, "model request cap reached"
	case recorder.ProviderError():
		return TrialProviderError, providerErrorSummary(record.Requests)
	case runErr != nil:
		return TrialHarnessError, runErr.Error()
	}
	for index, turn := range record.Turns {
		if !turnHasRequest(record.Requests, index) {
			return TrialHarnessError, fmt.Sprintf("turn %d reached no recorded model request", index)
		}
		if turn.Status != http.StatusOK {
			return TrialAgentError, fmt.Sprintf("turn %d returned HTTP %d", index, turn.Status)
		}
	}
	for _, request := range record.Requests {
		if request.Error == "" && !request.HasUsage {
			return TrialHarnessError, fmt.Sprintf("request %d reported no token usage", request.Index)
		}
	}
	if stopErr != nil {
		return TrialHarnessError, "stop household: " + stopErr.Error()
	}
	return TrialCompleted, ""
}

func turnHasRequest(requests []RecordedRequest, turn int) bool {
	for _, request := range requests {
		if request.Turn == turn {
			return true
		}
	}
	return false
}

func providerErrorSummary(requests []RecordedRequest) string {
	var classes []string
	for _, request := range requests {
		if request.Error != "" {
			classes = append(classes, request.Error)
		}
	}
	return "provider error: " + strings.Join(classes, ", ")
}
