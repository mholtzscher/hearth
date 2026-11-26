package agent //nolint:testpackage // The contract under test is the private built-in guidance.

import (
	"strings"
	"testing"
)

// TestAgentGuidanceKeepsSafetyRules keeps each rule whose loss could cause an
// unsafe Command, a false claim about the home, or an unwanted dashboard
// action. Each rule lives with the contract the model reads when it applies:
// the system prompt on every turn, compose_view's description and schema when
// it authors a view, and the skill reminder for Automations.
func TestAgentGuidanceKeepsSafetyRules(t *testing.T) {
	t.Parallel()
	prompt := defaultSystemPrompt
	composeView := composeViewDescription + " " + string(composeViewSchema)
	automation := automationSkillReminder("skill")
	for _, rule := range []struct{ owner, text, guidance string }{
		// Discovery and values.
		{"prompt", prompt, "Never invent an ID"},
		{"prompt", prompt, "ask the user instead of guessing"},
		{"prompt", prompt, "Missing readings are unknown, not zero or idle"},
		{"prompt", prompt, "A name does not establish what an Entity controls or which units it uses"},
		{"prompt", prompt, "Do not interchange them"},
		{"prompt", prompt, "Show IDs only when the user asks for them"},
		{"prompt", prompt, "advertised by the function schema"},
		{"prompt", prompt, "notification destination display name"},
		// Units.
		{"prompt", prompt, "household-unit values by default"},
		{"prompt", prompt, "units:canonical"},
		{"prompt", prompt, "Never copy a displayed number into a canonical write"},
		{"prompt", prompt, "Automation definitions remain canonical"},
		// Reports and Command outcomes.
		{"prompt", prompt, "never claim a door, lock, or home is secured or verified"},
		{"prompt", prompt, "not that the physical result was independently verified"},
		{"prompt", prompt, "A dispatched Command was sent, not completed"},
		// Dashboards.
		{"prompt", prompt, "never fabricate readings in text"},
		{"prompt", prompt, "do not replace the draft"},
		{"prompt", prompt, "never drop a requested card or source"},
		{"prompt", prompt, "Card controls send Commands only on an explicit browser gesture"},
		{"prompt", prompt, "saving a view never executes Commands"},
		{"prompt", prompt, "only an explicit user save saves a dashboard"},
		{"prompt", prompt, "do not repeat their readings"},
		{"prompt", prompt, "at most one short sentence"},
		// Card binding.
		{"compose_view", composeView, "A card bound to one Entity names it entity_id; TimeSeries lists its Entities in series"},
		{"compose_view", composeView, "prefer OutsideNow, Rain, Wind, Pressure, and Forecast over separate Metrics"},
		{"compose_view", composeView, "mm/h for a rain rate, mm for rain totals, deg for wind direction"},
		{"compose_view", composeView, "never sums of the rate"},
		{"compose_view", composeView, "sensor named for that period"},
		{"compose_view", composeView, "relative versus absolute pressure"},
		{"compose_view", composeView, "Every metric source must belong to the power Entity's Device"},
		{"compose_view", composeView, "Ordinary toggle tiles have no metric"},
		{"compose_view", composeView, "Identify display and child_lock by their Entity names"},
		{"compose_view", composeView, "omit a role you cannot identify"},
		{"compose_view", composeView, "Every bound Entity must belong to the power Entity's Device"},
		{"compose_view", composeView, "omit it for garage doors and doors"},
		{"compose_view", composeView, "Grouped speakers cannot be controlled"},
		{"compose_view", composeView, "not station readings"},
		// Automations.
		{"automation reminder", automation, "present_automation with mode reference and its discovered automation_id"},
		{"automation reminder", automation, "load hearth-automations"},
	} {
		if !strings.Contains(strings.Join(strings.Fields(rule.text), " "), rule.guidance) {
			t.Errorf("%s guidance omits %q", rule.owner, rule.guidance)
		}
	}
}

// TestSystemPromptStaysGeneral keeps per-card binding rules and Entity-type
// specifics out of the guidance sent on every turn.
func TestSystemPromptStaysGeneral(t *testing.T) {
	t.Parallel()
	for _, specific := range []string{
		"AirPurifier", "ToggleTile", "OutsideNow", "confirm_open",
		"hearth.thermostat/v1", "hearth.cover/v1", "set_range", "milli-Celsius", "Rheem", "ratgdo",
	} {
		if strings.Contains(defaultSystemPrompt, specific) {
			t.Errorf("system prompt mentions %q", specific)
		}
	}
}
