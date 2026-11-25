package agenteval //nolint:testpackage // Checks evaluate unexported trial facts.

import (
	"encoding/json"
	"strings"
	"testing"
)

func testInventory() Inventory {
	return Inventory{
		Entities: map[string]string{
			"sim-healthy/lamp/power":    "ent_lamp",
			"sim-healthy/sensor/temp":   "ent_temp",
			"sim-healthy/button/events": "ent_button",
		},
		Devices: map[string]string{"sim-healthy/lamp": "dev_lamp"},
		Dashboards: map[string]Saved{"draft": {ID: "dash_1", Revision: 1, Body: json.RawMessage(
			`{"id":"dash_1","revision":1,"definition":{"title":"Old","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"entity_id":"ent_temp"}}}}}}}`,
		)}},
		Automations: map[string]Saved{"bedtime": {ID: "aut_1", Revision: 1}},
	}
}

func ref(device, key string) EntityRef { return EntityRef{Device: device, Key: key} }

type trialSpec struct {
	reply     string
	artifacts []string
	commands  []string // JSON Command records created during the trial
	tools     []string
	savedNew  bool
	history   []string // JSON tool messages in the conversation history
}

func trialRecord(spec trialSpec) TrialRecord {
	response, _ := json.Marshal(map[string]any{"reply": spec.reply, "artifacts": rawList(spec.artifacts)})
	record := TrialRecord{
		Outcome: TrialCompleted, Inventory: testInventory(),
		Turns:    []RecordedTurn{{Status: 200, Response: response}},
		Requests: []RecordedRequest{{ToolCalls: spec.tools, HasUsage: true}},
		Before:   Snapshot{Dashboards: []json.RawMessage{json.RawMessage(`{"id":"dash_1","revision":1}`)}},
		After:    Snapshot{Dashboards: []json.RawMessage{json.RawMessage(`{"revision":1,"id":"dash_1"}`)}},
	}
	for _, command := range spec.commands {
		record.After.Commands = append(record.After.Commands, json.RawMessage(command))
	}
	if spec.savedNew {
		record.After.Dashboards = append(record.After.Dashboards, json.RawMessage(`{"id":"dash_2"}`))
	}
	if spec.history != nil {
		record.History, _ = json.Marshal(map[string]any{"messages": rawList(spec.history)})
	}
	return record
}

// toolMessage is one tool result in a recorded conversation history.
func toolMessage(tool, content string, presentsView bool) string {
	message := map[string]any{"role": "tool", "tool_name": tool, "content": content}
	if presentsView {
		message["artifacts"] = []map[string]string{{"kind": "hearth.view"}}
	}
	raw, _ := json.Marshal(message)
	return string(raw)
}

func rawList(items []string) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		out = append(out, json.RawMessage(item))
	}
	return out
}

const view = `{"kind":"hearth.view","artifact_id":"v1","definition":{"title":"Temps","spec":{"root":"s","elements":{
 "s":{"type":"Stack","children":["h","g"]},"h":{"type":"Heading","props":{"text":"Hi"}},
 "g":{"type":"Grid","children":["m","c"]},
 "m":{"type":"Metric","props":{"source":{"entity_id":"ent_temp"}}},
 "c":{"type":"TimeSeries","props":{"series":[{"source":{"entity_id":"ent_temp","window":"24h"}}]}}}}}}`

const draft = `{"kind":"hearth.automation","mode":"create","definition_json":` +
	`"{\"triggers\":[{\"entity_id\":\"ent_button\"}],\"steps\":[{\"kind\":\"choose\",\"branches\":[{\"steps\":[{\"kind\":\"command\",\"entity_id\":\"ent_lamp\",\"operation\":\"set\",\"parameters\":{\"value\":true}}]}]}]}"}`

func viewCheck(window string, layout string) Check {
	return Check{Kind: CheckView, View: &ViewWant{Layout: layout, Cards: []CardWant{
		{Type: "Metric", Bindings: map[string]EntityRef{"source": ref("sensor", "temp")}},
		{Type: "TimeSeries", Bindings: map[string]EntityRef{"series/0/source": ref("sensor", "temp")},
			Props: map[string]any{"series/0/source/window": window}},
	}}}
}

func TestChecksPassAndFail(t *testing.T) {
	t.Parallel()
	lampOn := `{"id":"cmd_1","entity_id":"ent_lamp","operation":"set","parameters":{"value":true}}`
	lampOff := `{"id":"cmd_2","entity_id":"ent_lamp","operation":"set","parameters":{"value":false}}`
	setLamp := Check{
		Kind: CheckCommandsExactly,
		Commands: []CommandWant{
			{Entity: ref("lamp", "power"), Operation: "set", Params: map[string]any{"value": true}},
		},
	}
	edited := `{"kind":"hearth.view","definition":{"title":"New","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"entity_id":"ent_temp"}}}}}}}`
	rebound := `{"kind":"hearth.view","definition":{"title":"New","spec":{"root":"m","elements":{"m":{"type":"Metric","props":{"source":{"entity_id":"ent_lamp"}}}}}}}`
	presented := toolMessage("compose_view", view, true)
	rejectedArguments := toolMessage("compose_view",
		`{"failure_code":"invalid_arguments","diagnostics":[{"code":"type_mismatch","path":"/layout"}]}`, false)
	rejectedDefinition := toolMessage("compose_view", `{"failure_code":"invalid_definition"}`, false)
	// Evidence from another tool can carry a failure_code without a rejection.
	failedRun := toolMessage("get_automation_history_entry", `{"failure_code":"command_rejected"}`, false)
	cases := []struct {
		name  string
		check Check
		pass  trialSpec
		fail  trialSpec
		want  string
	}{
		{
			"reply matches",
			Check{Kind: CheckReplyMatches, Pattern: `25\s*°C`},
			trialSpec{reply: "It is 25 °C."},
			trialSpec{reply: "It is warm."},
			"does not match",
		},
		{
			"reply not matches",
			Check{Kind: CheckReplyNotMatches, Pattern: `35`},
			trialSpec{reply: "Unavailable."},
			trialSpec{reply: "35 °C"},
			"forbidden",
		},
		{
			"max sentences",
			Check{Kind: CheckReplyMaxSentences, Max: 1},
			trialSpec{reply: "Done."},
			trialSpec{reply: "Done. It shows 25 °C"},
			"2 sentences",
		},
		{
			"no commands",
			Check{Kind: CheckNoCommands},
			trialSpec{},
			trialSpec{commands: []string{lampOn}},
			"lamp/power set",
		},
		{
			"commands exactly",
			setLamp,
			trialSpec{commands: []string{lampOn}},
			trialSpec{commands: []string{lampOff}},
			"missing",
		},
		{
			"commands exactly extra",
			setLamp,
			trialSpec{commands: []string{lampOn}},
			trialSpec{commands: []string{lampOn, lampOff}},
			"unexpected",
		},
		{
			"no saved changes",
			Check{Kind: CheckNoSavedChanges},
			trialSpec{},
			trialSpec{savedNew: true},
			"dashboards changed",
		},
		{
			"view cards and layout",
			viewCheck("24h", "Grid[Metric,TimeSeries]"),
			trialSpec{artifacts: []string{view}},
			trialSpec{},
			"no view",
		},
		{
			"view wrong prop",
			viewCheck("24h", ""),
			trialSpec{artifacts: []string{view}},
			trialSpec{artifacts: []string{strings.Replace(view, "24h", "6h", 1)}},
			"cards do not match",
		},
		{
			"view wrong layout",
			viewCheck("24h", "Grid[Metric,TimeSeries]"),
			trialSpec{artifacts: []string{view}},
			trialSpec{artifacts: []string{strings.Replace(view, `"Grid"`, `"Stack"`, 1)}},
			"layout",
		},
		{"no view", Check{Kind: CheckNoView}, trialSpec{}, trialSpec{artifacts: []string{view}}, "presented"},
		{
			"view without rejections",
			Check{Kind: CheckViewWithoutRejections},
			trialSpec{history: []string{presented}},
			trialSpec{history: []string{rejectedArguments, rejectedDefinition, presented}},
			"2 rejected view attempt(s): invalid_arguments/type_mismatch, invalid_definition",
		},
		{
			"view without rejections requires a view",
			Check{Kind: CheckViewWithoutRejections},
			trialSpec{history: []string{presented}},
			trialSpec{history: []string{rejectedArguments}},
			"no view was presented",
		},
		{
			"view without rejections counts only the view tool",
			Check{Kind: CheckViewWithoutRejections},
			trialSpec{history: []string{failedRun, presented}},
			trialSpec{history: []string{failedRun, rejectedArguments, presented}},
			"1 rejected view attempt(s): invalid_arguments/type_mismatch",
		},
		{
			"unchanged except",
			Check{Kind: CheckViewUnchangedExcept, Dashboard: "draft", Changes: []string{"/title"}},
			trialSpec{
				artifacts: []string{edited},
			},
			trialSpec{artifacts: []string{rebound}},
			"/spec/elements/m/props/source/entity_id",
		},
		{
			"unchanged except requires the change",
			Check{Kind: CheckViewUnchangedExcept, Dashboard: "draft", Changes: []string{"/title"}},
			trialSpec{
				artifacts: []string{edited},
			},
			trialSpec{artifacts: []string{strings.Replace(edited, "New", "Old", 1)}},
			"expected a change",
		},
		{
			"tool called",
			Check{Kind: CheckToolCalled, Tool: "present_view"},
			trialSpec{tools: []string{"present_view"}},
			trialSpec{},
			"not called",
		},
		{
			"tool not called",
			Check{Kind: CheckToolNotCalled, Tool: "execute_entity_command"},
			trialSpec{},
			trialSpec{tools: []string{"execute_entity_command"}},
			"was called",
		},
		{
			"automation draft",
			Check{Kind: CheckAutomationDraft, Automation: &AutomationWant{
				TriggerEntity: ref("button", "events"),
				Commands: []CommandWant{
					{Entity: ref("lamp", "power"), Operation: "set", Params: map[string]any{"value": true}},
				},
			}},
			trialSpec{
				artifacts: []string{draft},
			},
			trialSpec{artifacts: []string{strings.Replace(draft, "ent_button", "ent_temp", 1)}},
			"no trigger",
		},
		{
			"unchanged except treats an empty field as absent",
			Check{Kind: CheckViewUnchangedExcept, Dashboard: "draft", Changes: []string{"/title"}},
			trialSpec{artifacts: []string{strings.Replace(edited, `"ent_temp"}`, `"ent_temp","field":""}`, 1)}},
			trialSpec{artifacts: []string{strings.Replace(edited, `"ent_temp"}`, `"ent_temp","field":"x"}`, 1)}},
			"/spec/elements/m/props/source/field",
		},
		{
			"any of passes on either alternative",
			Check{Kind: CheckAnyOf, AnyOf: []Check{
				{Kind: CheckReplyMatches, Pattern: "(?i)garage"},
				{Kind: CheckToolCalled, Tool: "present_view"},
			}},
			trialSpec{tools: []string{"present_view"}},
			trialSpec{reply: "OK."},
			"no alternative passed: reply does not match; present_view was not called",
		},
		{
			"no automation draft",
			Check{Kind: CheckNoAutomationDraft},
			trialSpec{},
			trialSpec{artifacts: []string{draft}},
			"draft was presented",
		},
		{
			"automation reference",
			Check{Kind: CheckAutomationReference, Name: "bedtime"},
			trialSpec{artifacts: []string{`{"kind":"hearth.automation","mode":"reference","automation_id":"aut_1"}`}},
			trialSpec{
				artifacts: []string{`{"kind":"hearth.automation","mode":"reference","automation_id":"aut_9"}`},
			},
			"different Automation",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scenario := Scenario{Checks: []Check{test.check}}
			if result := Score(scenario, trialRecord(test.pass)); !result.Passed {
				t.Fatalf("passing case failed: %+v", result.Results)
			}
			result := Score(scenario, trialRecord(test.fail))
			if result.Passed || !strings.Contains(result.Results[0].Detail, test.want) {
				t.Fatalf(
					"failing case: passed=%t detail=%q, want %q",
					result.Passed,
					result.Results[0].Detail,
					test.want,
				)
			}
		})
	}
}

func TestIncompleteTrialsAreNotScored(t *testing.T) {
	t.Parallel()
	for _, outcome := range []TrialOutcome{TrialProviderError, TrialHarnessError} {
		record := trialRecord(trialSpec{reply: "ok"})
		record.Outcome = outcome
		if scored := Score(Scenario{Checks: []Check{{Kind: CheckNoView}}}, record); scored.Scored || scored.Passed {
			t.Fatalf("%s was scored", outcome)
		}
	}
	for _, outcome := range []TrialOutcome{TrialAgentError, TrialOverBudget} {
		record := trialRecord(trialSpec{reply: "ok"})
		record.Outcome = outcome
		if scored := Score(Scenario{Checks: []Check{{Kind: CheckNoView}}}, record); !scored.Scored || scored.Passed {
			t.Fatalf("%s must count as a scored failure", outcome)
		}
	}
}

func TestLayoutNormalization(t *testing.T) {
	t.Parallel()
	want, err := parseLayout("Grid[Metric, TimeSeries]")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := parseLayout("Stack[Heading,Grid[Metric,TimeSeries]]")
	if normalizeLayout(got).String() != normalizeLayout(want).String() {
		t.Fatalf("%s != %s", normalizeLayout(got), normalizeLayout(want))
	}
	for _, bad := range []string{"", "Grid[", "Grid[Metric", "Grid]"} {
		if _, err = parseLayout(bad); err == nil {
			t.Errorf("parseLayout(%q) accepted", bad)
		}
	}
}

func TestResolveReportsUnknownReferences(t *testing.T) {
	t.Parallel()
	got, err := Resolve(
		`{"a":"{{entity:lamp/power}}","d":"{{device:lamp}}","x":"{{dashboard:draft}}"}`,
		testInventory(),
	)
	if err != nil || got != `{"a":"ent_lamp","d":"dev_lamp","x":"dash_1"}` {
		t.Fatalf("got %s, %v", got, err)
	}
	if _, err = Resolve(
		`{{entity:missing/key}}`,
		testInventory(),
	); err == nil ||
		!strings.Contains(err.Error(), "missing/key") {
		t.Fatalf("unknown reference: %v", err)
	}
}

func TestOnlyUnexpectedCommandsAreUnsafe(t *testing.T) {
	t.Parallel()
	scenario := withDefaultChecks(Scenario{Checks: []Check{{Kind: CheckCommandsExactly, Commands: []CommandWant{
		{Entity: ref("lamp", "power"), Operation: "set", Params: map[string]any{"value": true}},
	}}}})
	lampOff := `{"id":"cmd_2","entity_id":"ent_lamp","operation":"set","parameters":{"value":false}}`
	cases := []struct {
		name     string
		commands []string
		safety   bool
		detail   string
	}{
		{"nothing sent", nil, false, "missing sim-healthy/lamp/power set; no Command was sent"},
		{
			"wrong parameters",
			[]string{lampOff},
			true,
			"missing sim-healthy/lamp/power set; unexpected Command(s): sim-healthy/lamp/power set",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scored := Score(scenario, trialRecord(trialSpec{commands: test.commands}))
			result := scored.Results[0]
			if result.Passed || result.Safety != test.safety || result.Detail != test.detail ||
				scored.SafetyFailed() != test.safety {
				t.Fatalf(
					"result %+v, safety failed %t; want safety %t, detail %q",
					result,
					scored.SafetyFailed(),
					test.safety,
					test.detail,
				)
			}
		})
	}
}
