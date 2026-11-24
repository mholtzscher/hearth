// Package agenteval runs the built-in Agent through real Core against the
// scripted simulator, records every model request, and scores the outcome.
// See docs/guides/agent-evaluation.md.
package agenteval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultAdapter owns Entities a scenario names without an adapter.
const DefaultAdapter = "sim-healthy"

// maxTurns bounds a scenario's user messages.
const maxTurns = 5

// Scenario is one checked-in evaluation case.
type Scenario struct {
	ID          string       `yaml:"id"`
	Title       string       `yaml:"title"`
	Tags        []string     `yaml:"tags"`
	Setup       Setup        `yaml:"setup"`
	Turns       []Turn       `yaml:"turns"`
	Checks      []Check      `yaml:"checks"`
	Judge       *Rubric      `yaml:"judge"`
	MaxRequests int          `yaml:"max_requests"`
	Reference   []ScriptStep `yaml:"reference"`

	// SHA256 identifies the scenario file's exact bytes.
	SHA256 string `yaml:"-"`
}

// EntityRef names a simulator Entity independently of per-run canonical IDs.
type EntityRef struct {
	Adapter string `yaml:"adapter" json:"adapter"`
	Device  string `yaml:"device"  json:"device"`
	Key     string `yaml:"key"     json:"key"`
}

// InventoryKey is the record key "adapter/device/key".
func (ref EntityRef) InventoryKey() string {
	adapter := ref.Adapter
	if adapter == "" {
		adapter = DefaultAdapter
	}
	return adapter + "/" + ref.Device + "/" + ref.Key
}

// Setup prepares the household before the first turn.
type Setup struct {
	Pin         []Pin           `yaml:"pin"`
	Dashboards  []SeedDashboard `yaml:"dashboards"`
	Automations []SeedJSON      `yaml:"automations"`
}

// Pin pauses an Entity's scripted output and publishes a fixed canonical value.
type Pin struct {
	Entity EntityRef `yaml:"entity"`
	Value  any       `yaml:"value"`
}

// SeedDashboard is saved before the turn and may be selected as a draft.
type SeedDashboard struct {
	Name       string `yaml:"name"`
	Definition string `yaml:"definition"` // portable JSON with placeholders
}

// SeedJSON is a JSON document with placeholders, such as an Automation definition.
type SeedJSON struct {
	Name       string `yaml:"name"`
	Definition string `yaml:"definition"`
}

// Turn is one user message.
type Turn struct {
	Text            string `yaml:"text"`
	SelectDashboard string `yaml:"select_dashboard"`
}

// CheckKind selects one deterministic check.
type CheckKind string

const (
	CheckReplyMatches        CheckKind = "reply_matches"
	CheckReplyNotMatches     CheckKind = "reply_not_matches"
	CheckReplyMaxSentences   CheckKind = "reply_max_sentences"
	CheckNoCommands          CheckKind = "no_commands"
	CheckCommandsExactly     CheckKind = "commands_exactly"
	CheckNoSavedChanges      CheckKind = "no_saved_changes"
	CheckView                CheckKind = "view"
	CheckNoView              CheckKind = "no_view"
	CheckViewUnchangedExcept CheckKind = "view_unchanged_except"
	CheckToolCalled          CheckKind = "tool_called"
	CheckToolNotCalled       CheckKind = "tool_not_called"
	CheckAutomationDraft     CheckKind = "automation_draft"
	CheckNoAutomationDraft   CheckKind = "no_automation_draft"
	CheckAutomationReference CheckKind = "automation_reference"
	// CheckAnyOf passes when at least one nested check passes, for requests
	// with more than one good answer, such as a reply or a view.
	CheckAnyOf CheckKind = "any_of"
	// CheckViewWithoutRejections passes when a view was presented and the
	// tool that presented it rejected none of its calls, so a slow,
	// retried presentation fails even when the final view is right.
	CheckViewWithoutRejections CheckKind = "view_without_rejections"
)

// Check is a tagged union on Kind.
type Check struct {
	Kind       CheckKind       `yaml:"kind"`
	Safety     bool            `yaml:"safety"`
	Pattern    string          `yaml:"pattern"`
	Max        int             `yaml:"max"`
	Commands   []CommandWant   `yaml:"commands"`
	View       *ViewWant       `yaml:"view"`
	Dashboard  string          `yaml:"dashboard"`
	Changes    []string        `yaml:"changes"`
	Tool       string          `yaml:"tool"`
	Automation *AutomationWant `yaml:"automation"`
	Name       string          `yaml:"name"`
	AnyOf      []Check         `yaml:"any_of"`

	// Default marks a check added by the loader rather than the scenario.
	Default bool `yaml:"-"`
}

// Label names the check in reports.
func (check Check) Label() string {
	if check.Pattern != "" {
		return fmt.Sprintf("%s %q", check.Kind, check.Pattern)
	}
	if check.Kind == CheckReplyMaxSentences {
		return fmt.Sprintf("%s %d", check.Kind, check.Max)
	}
	if check.Tool != "" {
		return fmt.Sprintf("%s %s", check.Kind, check.Tool)
	}
	if check.Kind == CheckAnyOf {
		labels := make([]string, len(check.AnyOf))
		for index, nested := range check.AnyOf {
			labels[index] = nested.Label()
		}
		return fmt.Sprintf("%s(%s)", check.Kind, strings.Join(labels, " | "))
	}
	return string(check.Kind)
}

// CommandWant is one expected Command.
type CommandWant struct {
	Entity    EntityRef `yaml:"entity"`
	Operation string    `yaml:"operation"`
	Params    any       `yaml:"params"`
}

// ViewWant matches the cards of the last presented view.
type ViewWant struct {
	Cards  []CardWant `yaml:"cards"`
	Layout string     `yaml:"layout"`
}

// CardWant matches one card's type, Entity bindings, and props.
type CardWant struct {
	Type     string               `yaml:"type"`
	Bindings map[string]EntityRef `yaml:"bindings"`
	Props    map[string]any       `yaml:"props"`
}

// AutomationWant matches the last presented Automation draft.
type AutomationWant struct {
	TriggerEntity EntityRef     `yaml:"trigger_entity"`
	Commands      []CommandWant `yaml:"commands"`
}

// Rubric lists judge criteria.
type Rubric struct {
	Criteria []string `yaml:"criteria"`
}

// ScriptStep is one scripted-model response in the self-test. Steps are
// consumed in order; a reply step ends the current user turn.
type ScriptStep struct {
	Call      string  `yaml:"call"`
	Arguments string  `yaml:"arguments"`
	Reply     *string `yaml:"reply"`
}

var scenarioID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// LoadScenarios reads every *.yaml file in dir, sorted by ID.
func LoadScenarios(dir string) ([]Scenario, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no scenarios in %s", dir)
	}
	scenarios := make([]Scenario, 0, len(paths))
	seen := map[string]string{}
	for _, path := range paths {
		scenario, loadErr := LoadScenario(path)
		if loadErr != nil {
			return nil, loadErr
		}
		if filepath.Base(path) != scenario.ID+".yaml" {
			return nil, fmt.Errorf("%s: file name must be %s.yaml", path, scenario.ID)
		}
		if other, ok := seen[scenario.ID]; ok {
			return nil, fmt.Errorf("%s: id %q is also used by %s", path, scenario.ID, other)
		}
		seen[scenario.ID] = path
		scenarios = append(scenarios, scenario)
	}
	slices.SortFunc(scenarios, func(a, b Scenario) int { return strings.Compare(a.ID, b.ID) })
	return scenarios, nil
}

// LoadScenario reads, validates, and completes one scenario file.
func LoadScenario(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	var scenario Scenario
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err = decoder.Decode(&scenario); err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	if err = scenario.validate(); err != nil {
		return Scenario{}, fmt.Errorf("%s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	scenario.SHA256 = hex.EncodeToString(sum[:])
	return withDefaultChecks(scenario), nil
}

// withDefaultChecks adds the default safety checks and marks
// commands_exactly as a safety check.
func withDefaultChecks(scenario Scenario) Scenario {
	scenario.Checks = slices.Clone(scenario.Checks)
	expectsCommands := slices.ContainsFunc(
		scenario.Checks,
		func(c Check) bool { return c.Kind == CheckCommandsExactly },
	)
	hasNoCommands := slices.ContainsFunc(scenario.Checks, func(c Check) bool { return c.Kind == CheckNoCommands })
	if !expectsCommands && !hasNoCommands {
		scenario.Checks = append(scenario.Checks, Check{Kind: CheckNoCommands, Safety: true, Default: true})
	}
	if !slices.ContainsFunc(scenario.Checks, func(c Check) bool { return c.Kind == CheckNoSavedChanges }) {
		scenario.Checks = append(scenario.Checks, Check{Kind: CheckNoSavedChanges, Safety: true, Default: true})
	}
	for index := range scenario.Checks {
		if scenario.Checks[index].Kind == CheckCommandsExactly {
			scenario.Checks[index].Safety = true
		}
	}
	return scenario
}

func (scenario Scenario) validate() error {
	if !scenarioID.MatchString(scenario.ID) {
		return errors.New("id: must be kebab-case")
	}
	if strings.TrimSpace(scenario.Title) == "" {
		return errors.New("title: required")
	}
	dashboards, err := scenario.Setup.validate()
	if err != nil {
		return err
	}
	if err = validateTurns(scenario.Turns, dashboards); err != nil {
		return err
	}
	if len(scenario.Checks) == 0 {
		return errors.New("checks: at least one required")
	}
	for index, check := range scenario.Checks {
		if err = check.validate(dashboards); err != nil {
			return fmt.Errorf("checks[%d] (%s): %w", index, check.Kind, err)
		}
	}
	if scenario.MaxRequests < 0 {
		return errors.New("max_requests: must not be negative")
	}
	if scenario.Judge != nil && len(scenario.Judge.Criteria) == 0 {
		return errors.New("judge.criteria: at least one required")
	}
	return validateReference(scenario.Reference, len(scenario.Turns))
}

// validate checks seeds and pins, returning the seeded dashboard names.
func (setup Setup) validate() (map[string]bool, error) {
	dashboards := map[string]bool{}
	for index, seed := range setup.Dashboards {
		if seed.Name == "" || dashboards[seed.Name] {
			return nil, fmt.Errorf("setup.dashboards[%d].name: required and unique", index)
		}
		dashboards[seed.Name] = true
		if !json.Valid([]byte(expandShape(seed.Definition))) {
			return nil, fmt.Errorf("setup.dashboards[%d].definition: invalid JSON", index)
		}
	}
	for index, seed := range setup.Automations {
		if seed.Name == "" || !json.Valid([]byte(expandShape(seed.Definition))) {
			return nil, fmt.Errorf("setup.automations[%d]: name and JSON definition required", index)
		}
	}
	for index, pin := range setup.Pin {
		if err := validateRef(pin.Entity); err != nil {
			return nil, fmt.Errorf("setup.pin[%d].entity: %w", index, err)
		}
		if pin.Value == nil {
			return nil, fmt.Errorf("setup.pin[%d].value: required", index)
		}
	}
	return dashboards, nil
}

func validateTurns(turns []Turn, dashboards map[string]bool) error {
	if len(turns) == 0 || len(turns) > maxTurns {
		return fmt.Errorf("turns: 1 to %d required", maxTurns)
	}
	for index, turn := range turns {
		if strings.TrimSpace(turn.Text) == "" {
			return fmt.Errorf("turns[%d].text: required", index)
		}
		if turn.SelectDashboard != "" && !dashboards[turn.SelectDashboard] {
			return fmt.Errorf("turns[%d].select_dashboard: unknown dashboard %q", index, turn.SelectDashboard)
		}
	}
	return nil
}

func validateReference(steps []ScriptStep, turns int) error {
	if len(steps) == 0 {
		return errors.New("reference: a scripted trajectory is required")
	}
	replies := 0
	for index, step := range steps {
		if (step.Call == "") == (step.Reply == nil) {
			return fmt.Errorf("reference[%d]: exactly one of call or reply", index)
		}
		if step.Call != "" && !json.Valid([]byte(expandShape(step.Arguments))) {
			return fmt.Errorf("reference[%d].arguments: invalid JSON", index)
		}
		if step.Reply != nil {
			replies++
		}
	}
	if replies != turns || steps[len(steps)-1].Reply == nil {
		return errors.New("reference: each turn must end with exactly one reply step")
	}
	return nil
}

func validateRef(ref EntityRef) error {
	if ref.Device == "" || ref.Key == "" {
		return errors.New("device and key required")
	}
	return nil
}

func (check Check) validate(dashboards map[string]bool) error {
	validator, ok := checkValidators()[check.Kind]
	if !ok {
		return fmt.Errorf("unknown kind %q", check.Kind)
	}
	return validator(check, dashboards)
}

func noFields(Check, map[string]bool) error { return nil }

func validatePattern(check Check, _ map[string]bool) error {
	if check.Pattern == "" {
		return errors.New("pattern: required")
	}
	if _, err := regexp.Compile(check.Pattern); err != nil {
		return fmt.Errorf("pattern: %w", err)
	}
	return nil
}

func validateTool(check Check, _ map[string]bool) error {
	if check.Tool == "" {
		return errors.New("tool: required")
	}
	return nil
}

// checkValidators checks each kind's fields at load time.
func checkValidators() map[CheckKind]func(Check, map[string]bool) error {
	return map[CheckKind]func(Check, map[string]bool) error{
		CheckReplyMatches:      validatePattern,
		CheckReplyNotMatches:   validatePattern,
		CheckToolCalled:        validateTool,
		CheckToolNotCalled:     validateTool,
		CheckNoCommands:        noFields,
		CheckNoSavedChanges:    noFields,
		CheckNoView:            noFields,
		CheckNoAutomationDraft: noFields,
		CheckReplyMaxSentences: func(check Check, _ map[string]bool) error {
			if check.Max < 1 {
				return errors.New("max: at least 1")
			}
			return nil
		},
		CheckCommandsExactly: func(check Check, _ map[string]bool) error {
			if len(check.Commands) == 0 {
				return errors.New("commands: at least one required; use no_commands for none")
			}
			return validateCommandWants("commands", check.Commands)
		},
		CheckView: func(check Check, _ map[string]bool) error {
			if check.View == nil || len(check.View.Cards) == 0 {
				return errors.New("view.cards: at least one required")
			}
			return check.View.validate()
		},
		CheckViewUnchangedExcept: func(check Check, dashboards map[string]bool) error {
			if !dashboards[check.Dashboard] {
				return fmt.Errorf("dashboard: unknown %q", check.Dashboard)
			}
			for _, pointer := range check.Changes {
				if !strings.HasPrefix(pointer, "/") {
					return fmt.Errorf("changes: %q is not a JSON Pointer", pointer)
				}
			}
			return nil
		},
		CheckAutomationDraft: func(check Check, _ map[string]bool) error {
			if check.Automation == nil {
				return errors.New("automation: required")
			}
			if err := validateRef(check.Automation.TriggerEntity); err != nil {
				return fmt.Errorf("automation.trigger_entity: %w", err)
			}
			return validateCommandWants("automation.commands", check.Automation.Commands)
		},
		CheckAnyOf: validateAnyOf,
		CheckAutomationReference: func(check Check, _ map[string]bool) error {
			if check.Name == "" {
				return errors.New("name: seeded Automation name required")
			}
			return nil
		},
		CheckViewWithoutRejections: noFields,
	}
}

// minAlternatives is the fewest checks an any_of needs to offer a choice.
const minAlternatives = 2

func validateAnyOf(check Check, dashboards map[string]bool) error {
	if len(check.AnyOf) < minAlternatives {
		return errors.New("any_of: at least two checks required")
	}
	for index, nested := range check.AnyOf {
		if nested.Safety {
			return fmt.Errorf("any_of[%d]: mark the any_of check as safety, not a nested one", index)
		}
		if err := nested.validate(dashboards); err != nil {
			return fmt.Errorf("any_of[%d] (%s): %w", index, nested.Kind, err)
		}
	}
	return nil
}

func validateCommandWants(field string, wants []CommandWant) error {
	for index, want := range wants {
		if err := validateRef(want.Entity); err != nil || want.Operation == "" {
			return fmt.Errorf("%s[%d]: entity and operation required", field, index)
		}
	}
	return nil
}

func (view ViewWant) validate() error {
	if view.Layout != "" {
		if _, err := parseLayout(view.Layout); err != nil {
			return fmt.Errorf("view.layout: %w", err)
		}
	}
	for index, card := range view.Cards {
		if card.Type == "" {
			return fmt.Errorf("view.cards[%d].type: required", index)
		}
		for path, ref := range card.Bindings {
			if err := validateRef(ref); err != nil {
				return fmt.Errorf("view.cards[%d].bindings[%s]: %w", index, path, err)
			}
		}
	}
	return nil
}

// placeholder matches {{entity:...}}, {{device:...}}, {{dashboard:...}}, and
// {{automation:...}} references resolved per trial.
var placeholder = regexp.MustCompile(`\{\{(entity|device|dashboard|automation):([a-z0-9_/-]+)\}\}`)

// expandShape replaces placeholders with a syntactically valid stand-in so
// templates can be JSON-checked at load time.
func expandShape(template string) string {
	return placeholder.ReplaceAllString(template, "placeholder")
}
