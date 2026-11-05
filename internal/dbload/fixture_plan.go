package dbload

import (
	"errors"
	"fmt"
)

// Scenario dimensions and fan-out relations accepted by Seed.
const (
	dimensionPopulation = "population"
	dimensionFanout     = "fanout"
	dimensionHistory    = "history"
	dimensionCombined   = "combined"

	relationDevices     = "devices"
	relationMessages    = "messages"
	relationAutomations = "automations"
)

const (
	fixtureSuiteRevision = "dbload-fixtures-v1"
	fixtureDatabaseName  = "fixture.db"
	fixtureManifestName  = "manifest.json"
)

// Baseline household constants. Every scale multiplies these independent
// subgraphs rather than multiplying every nested count together.
const (
	baselineAdapters                = 2
	baselineDevicesPerAdapter       = 10
	entitiesPerDevice               = 4
	baselineAutomations             = 20
	baselineConversations           = 20
	baselineDashboards              = 3
	destinationsPerAdapter          = 2
	functionsPerCatalog             = 2
	baselineStateHistory            = 24
	baselineEntityEvents            = 8
	baselineCommands                = 6
	baselineRunsPerAutomation       = 8
	baselineSkipsPerAutomation      = 1
	baselineMessagesPerConversation = 16
	baselineAttemptsPerDestination  = 2
	baselineInvocationsPerFunction  = 2
	baselineHealthTransitions       = 3
	baselineEntityTransitions       = 2
	outboxObservationPerAdapter     = 2
	outboxEventPerAdapter           = 1
	unknownRejectedObservations     = 4
	stateRolesPerDevice             = 3
	rolePower                       = 0
	roleTemperature                 = 1
	roleHumidity                    = 2
	roleEvent                       = 3
	combinedPopulationScale         = 10
	combinedFanoutScale             = 10
	combinedHistoryScale            = 10
	maxEntitiesPerRegistration      = 64
)

var (
	errFixtureUnmarked  = errors.New("fixture database is not marked by a manifest")
	errFixtureIntegrity = errors.New("fixture integrity check failed")
	errFixtureDomain    = errors.New("fixture domain check failed")
)

// fixturePlan is the explicit count schedule one scenario expands to. It keeps
// population, per-parent fan-out, and retained history independent.
type fixturePlan struct {
	scenario                Scenario
	population              int
	historyScale            int
	adapters                int
	devicesPerAdapter       int
	devices                 int
	entities                int
	powerEntities           int
	stateEntities           int
	eventEntities           int
	automations             int
	runsPerAutomation       int
	stateHistory            int
	entityEvents            int
	commands                int
	conversations           int
	messagesPerConversation int
	dashboards              int
	destinations            int
	terminalAttempts        int
	invocationsPerFunction  int
	adapterTransitions      int
	entityTransitions       int
}

// planFor validates the scenario and derives every effective count.
func planFor(scenario Scenario) (fixturePlan, error) {
	if err := ValidateScenario(scenario); err != nil {
		return fixturePlan{}, err
	}
	plan := fixturePlan{
		scenario:                scenario,
		population:              1,
		historyScale:            1,
		adapters:                baselineAdapters,
		devicesPerAdapter:       baselineDevicesPerAdapter,
		stateHistory:            baselineStateHistory,
		entityEvents:            baselineEntityEvents,
		commands:                baselineCommands,
		runsPerAutomation:       baselineRunsPerAutomation,
		conversations:           baselineConversations,
		messagesPerConversation: baselineMessagesPerConversation,
		dashboards:              baselineDashboards,
		terminalAttempts:        baselineAttemptsPerDestination,
		invocationsPerFunction:  baselineInvocationsPerFunction,
	}
	switch scenario.Dimension {
	case dimensionPopulation:
		plan.population = scenario.Scale
	case dimensionHistory:
		plan.historyScale = scenario.Scale
	case dimensionCombined:
		plan.population = combinedPopulationScale
		plan.historyScale = combinedHistoryScale
		plan.devicesPerAdapter = baselineDevicesPerAdapter * combinedFanoutScale
	case dimensionFanout:
		switch scenario.Relation {
		case relationDevices:
			plan.devicesPerAdapter = baselineDevicesPerAdapter * scenario.Scale
		case relationMessages:
			plan.messagesPerConversation *= scenario.Scale
		case relationAutomations:
			// Filled after the power-Entity count is known.
		}
	}
	plan.adapters = baselineAdapters * plan.population
	plan.devices = plan.adapters * plan.devicesPerAdapter
	plan.entities = plan.devices * entitiesPerDevice
	plan.powerEntities = plan.devices
	plan.stateEntities = plan.devices * stateRolesPerDevice
	plan.eventEntities = plan.devices
	plan.stateHistory = baselineStateHistory * plan.historyScale
	plan.entityEvents = baselineEntityEvents * plan.historyScale
	plan.commands = baselineCommands * plan.historyScale
	plan.runsPerAutomation = baselineRunsPerAutomation * plan.historyScale
	plan.conversations = baselineConversations * plan.population
	plan.dashboards = baselineDashboards * plan.population
	plan.destinations = plan.adapters * destinationsPerAdapter
	plan.terminalAttempts = baselineAttemptsPerDestination * plan.historyScale
	plan.invocationsPerFunction = baselineInvocationsPerFunction*plan.historyScale + 1
	plan.adapterTransitions = baselineHealthTransitions * plan.historyScale
	plan.entityTransitions = baselineEntityTransitions * plan.historyScale
	plan.automations = baselineAutomations * plan.population
	if scenario.Dimension == dimensionFanout && scenario.Relation == relationAutomations {
		plan.automations = plan.powerEntities * scenario.Scale
	}
	return plan, nil
}

// observationCount includes the retained history plus the deliberately
// unknown-Entity rejections that must survive without a foreign key.
func (plan fixturePlan) observationCount() int {
	return plan.stateEntities*plan.stateHistory + unknownRejectedObservations
}

func (plan fixturePlan) eventCount() int {
	return plan.eventEntities * plan.entityEvents
}

func (plan fixturePlan) commandCount() int {
	return plan.powerEntities * plan.commands
}

func (plan fixturePlan) historyCount() int {
	return plan.automations * (plan.runsPerAutomation + baselineSkipsPerAutomation)
}

func (plan fixturePlan) stepCount() int {
	// Failed, interrupted, and running Runs retain one reached Step; every
	// other Run records the complete two-Step definition.
	nonSucceededRuns := 3
	return plan.automations * (plan.runsPerAutomation*fixtureDefinitionSteps - nonSucceededRuns)
}

func (plan fixturePlan) messageCount() int {
	return plan.conversations * plan.messagesPerConversation
}

func (plan fixturePlan) attemptCount() int {
	return plan.destinations * (plan.terminalAttempts + 1)
}

func (plan fixturePlan) functionCount() int {
	return plan.adapters * functionsPerCatalog
}

func (plan fixturePlan) invocationCount() int {
	return plan.functionCount() * plan.invocationsPerFunction
}

func (plan fixturePlan) healthTransitionCount() int {
	return plan.adapters*plan.adapterTransitions + plan.entities*plan.entityTransitions
}

func (plan fixturePlan) limits() []string {
	matching := 1
	if plan.scenario.Dimension == dimensionFanout && plan.scenario.Relation == relationAutomations {
		matching = plan.scenario.Scale
	}
	return []string{
		fmt.Sprintf("entities_per_device=%d (domain maximum %d)", entitiesPerDevice, maxEntitiesPerRegistration),
		fmt.Sprintf("automation_steps=%d (domain maximum 32)", fixtureDefinitionSteps),
		fmt.Sprintf("matching_automations_per_entity=%d", matching),
		fmt.Sprintf("observations=%d", plan.observationCount()),
		fmt.Sprintf("commands=%d for %d power Entities only", plan.commandCount(), plan.powerEntities),
		fmt.Sprintf("entity_events=%d for %d event-source Entities only", plan.eventCount(), plan.eventEntities),
		fmt.Sprintf(
			"history_rows=%d target Entity observations; total retained evidence rows=%d",
			plan.historyRows(),
			plan.retainedHistoryRows(),
		),
		"outbox_rows_are_pending_backlog_not_history",
		fmt.Sprintf("unknown_entity_rejected_observations=%d are intentionally FK-free", unknownRejectedObservations),
	}
}

// historyRows is the retained observation depth of the representative power
// Entity, which is the keyset offset consumers use for deep history reads.
func (plan fixturePlan) historyRows() int {
	return plan.stateHistory
}

func (plan fixturePlan) retainedHistoryRows() int {
	return plan.observationCount() + plan.eventCount() + plan.commandCount() +
		plan.historyCount() + plan.stepCount() + plan.healthTransitionCount() +
		plan.attemptCount() + plan.invocationCount()
}
