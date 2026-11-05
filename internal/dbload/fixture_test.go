package dbload //nolint:testpackage // Unit tests cover unexported scenario planning and template builders.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

func TestFixtureIDIsCanonicalUUIDv7(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, 2048)
	for index := range 2048 {
		id := fixtureID("ent_", uint64(index))
		if seen[id] {
			t.Fatalf("fixtureID repeated %s", id)
		}
		seen[id] = true
		parsed, err := uuid.Parse(id[len("ent_"):])
		if err != nil {
			t.Fatalf("fixtureID returned invalid UUID %q: %v", id, err)
		}
		if parsed.Version() != 7 || parsed.Variant() != uuid.RFC4122 {
			t.Fatalf("fixtureID %q is not a canonical UUIDv7", id)
		}
		if id != "ent_"+parsed.String() {
			t.Fatalf("fixtureID %q is not canonical text", id)
		}
	}
}

func TestFixtureTimestampIsFixedWidthUTC(t *testing.T) {
	t.Parallel()
	stamp := fixtureStamp(-90 * time.Minute)
	if len(stamp) != len("2026-11-04T10:30:00.000000000Z") {
		t.Fatalf("fixture timestamp %q is not fixed width", stamp)
	}
	parsed, err := time.Parse(fixtureTimestampLayout, stamp)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Location() != time.UTC || !parsed.Before(fixtureClock) {
		t.Fatalf("fixture timestamp %q is not UTC before the logical clock", stamp)
	}
}

//nolint:gocognit // One table states the complete expected matrix for every sweep.
func TestPlanCountsAreIndependentSweeps(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		scenario     Scenario
		adapters     int
		devices      int
		entities     int
		observations int
		automations  int
		runs         int
		messages     int
		stateHistory int
	}{
		{
			name:     "baseline",
			scenario: Scenario{Name: "population-1", Dimension: "population", Scale: 1},
			adapters: 2, devices: 20, entities: 80, observations: 1444,
			automations: 20, runs: 160, messages: 320, stateHistory: 24,
		},
		{
			name:     "population-1000",
			scenario: Scenario{Name: "population-1000", Dimension: "population", Scale: 1000},
			adapters: 2000, devices: 20000, entities: 80000, observations: 1440004,
			automations: 20000, runs: 160000, messages: 320000, stateHistory: 24,
		},
		{
			name:     "fanout-devices-1000",
			scenario: Scenario{Name: "fanout-devices-1000", Dimension: "fanout", Scale: 1000, Relation: "devices"},
			adapters: 2, devices: 20000, entities: 80000, observations: 1440004,
			automations: 20, runs: 160, messages: 320, stateHistory: 24,
		},
		{
			name:     "fanout-messages-1000",
			scenario: Scenario{Name: "fanout-messages-1000", Dimension: "fanout", Scale: 1000, Relation: "messages"},
			adapters: 2, devices: 20, entities: 80, observations: 1444,
			automations: 20, runs: 160, messages: 320000, stateHistory: 24,
		},
		{
			name:     "fanout-automations-1000",
			scenario: Scenario{Name: "fanout-automations-1000", Dimension: "fanout", Scale: 1000, Relation: "automations"},
			adapters: 2, devices: 20, entities: 80, observations: 1444,
			automations: 20000, runs: 160000, messages: 320, stateHistory: 24,
		},
		{
			name:     "history-1000",
			scenario: Scenario{Name: "history-1000", Dimension: "history", Scale: 1000},
			adapters: 2, devices: 20, entities: 80, observations: 1440004,
			automations: 20, runs: 160000, messages: 320, stateHistory: 24000,
		},
		{
			name:     "combined-10",
			scenario: Scenario{Name: "combined-10", Dimension: "combined", Scale: 10},
			adapters: 20, devices: 2000, entities: 8000, observations: 1440004,
			automations: 200, runs: 16000, messages: 3200, stateHistory: 240,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := planFor(test.scenario)
			if err != nil {
				t.Fatal(err)
			}
			if plan.adapters != test.adapters || plan.devices != test.devices || plan.entities != test.entities {
				t.Fatalf("inventory = adapters %d devices %d entities %d",
					plan.adapters, plan.devices, plan.entities)
			}
			if plan.observationCount() != test.observations {
				t.Fatalf("observations = %d, want %d", plan.observationCount(), test.observations)
			}
			if plan.automations != test.automations || plan.automations*plan.runsPerAutomation != test.runs {
				t.Fatalf("automations %d runs %d", plan.automations, plan.automations*plan.runsPerAutomation)
			}
			if plan.messageCount() != test.messages {
				t.Fatalf("messages = %d, want %d", plan.messageCount(), test.messages)
			}
			if plan.stateHistory != test.stateHistory || plan.historyRows() != test.stateHistory {
				t.Fatalf("state history = %d, want %d", plan.stateHistory, test.stateHistory)
			}
		})
	}
}

func TestPlanRejectsInvalidScenarios(t *testing.T) {
	t.Parallel()
	for _, scenario := range []Scenario{
		{Name: "bad-scale", Dimension: "population", Scale: 3},
		{Name: "bad-dimension", Dimension: "everything", Scale: 1},
		{Name: "missing-relation", Dimension: "fanout", Scale: 10},
		{Name: "bad-relation", Dimension: "fanout", Scale: 10, Relation: "things"},
		{Name: "combined-scale", Dimension: "combined", Scale: 1},
		{Name: ""},
	} {
		if _, err := planFor(scenario); err == nil {
			t.Fatalf("planFor(%+v) accepted an invalid scenario", scenario)
		}
	}
}

func TestFixtureTemplatesPassProductionCodecs(t *testing.T) {
	t.Parallel()
	definition, err := fixtureAutomationDefinition(0, entityIDFor(0, rolePower))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(definition, &document); err != nil {
		t.Fatal(err)
	}
	if len(document["triggers"].([]any)) != fixtureDefinitionTriggers ||
		len(document["steps"].([]any)) != fixtureDefinitionSteps {
		t.Fatal("automation template has unexpected dimensions")
	}
	dashboard, err := fixtureDashboardDefinition(0, entityTemperatureID(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard) == 0 {
		t.Fatal("dashboard template is empty")
	}
	catalog, catalogJSON, err := fixtureFunctionCatalog(0, adapterIDFor(0), runtimeIDFor(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Functions) != functionsPerCatalog || len(catalogJSON) == 0 {
		t.Fatal("function catalog template is incomplete")
	}
	body, err := fixtureMessageJSON(schema.User, "fixture question")
	if err != nil {
		t.Fatal(err)
	}
	var message schema.Message
	if err = json.Unmarshal(body, &message); err != nil {
		t.Fatal(err)
	}
	if message.Role != schema.User || message.Content != "fixture question" {
		t.Fatal("agent message template did not round-trip through the Eino codec")
	}
}
