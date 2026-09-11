package simulator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	contractenumeventv1 "github.com/mholtzscher/hearth/entitytypes/enumeventv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkadapterenumeventv1 "github.com/mholtzscher/hearth/sdk/adapter/enumeventv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	futureObservationOffset = 2 * time.Minute

	ScenarioHappy               = "happy"
	ScenarioAdapterUnhealthy    = "adapter-unhealthy"
	ScenarioEntityUnavailable   = "entity-unavailable"
	ScenarioUpstreamRejection   = "upstream-rejection"
	ScenarioOutcomeTimeout      = "outcome-timeout"
	ScenarioNoOpRefresh         = "no-op-refresh"
	ScenarioOverlappingCommands = "overlapping-opposite-command"
	ScenarioInterruptedCommand  = "interrupted-command"
	ScenarioDelayedSourceTime   = "delayed-source-time"
	ScenarioFutureClockSkew     = "future-clock-skew"
	ScenarioRestartBeforeAck    = "restart-before-ack"
	ScenarioEntityEvents        = "entity-events"

	// EntityEventSinglePress and EntityEventDoublePress are the synthetic
	// report names the entity-events scenario support advertises and the
	// operator input loop accepts.
	EntityEventSinglePress = "single_press"
	EntityEventDoublePress = "double_press"
)

func ValidScenario(value string) bool {
	switch value {
	case ScenarioHappy, ScenarioAdapterUnhealthy, ScenarioEntityUnavailable, ScenarioUpstreamRejection,
		ScenarioOutcomeTimeout, ScenarioNoOpRefresh, ScenarioOverlappingCommands,
		ScenarioInterruptedCommand, ScenarioDelayedSourceTime, ScenarioFutureClockSkew,
		ScenarioRestartBeforeAck, ScenarioEntityEvents:
		return true
	default:
		return false
	}
}

type Session interface {
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
	PublishEntityEvent(context.Context, adapter.EntityEvent) (adapter.EntityEventID, error)
	SetHealth(context.Context, adapter.HealthReport) error
	ReportEntityAvailability(context.Context, []adapter.EntityAvailabilityReport) error
}

type Adapter struct {
	session       Session
	scenario      string
	support       contractpowerv1.Support
	mutex         sync.Mutex
	state         contractpowerv1.State
	eventEntityID string
}

func New(session Session, scenario string) (*Adapter, error) {
	if session == nil {
		return nil, errors.New("simulator adapter Session is required")
	}
	if !ValidScenario(scenario) {
		return nil, fmt.Errorf("unknown simulator scenario %q", scenario)
	}
	return &Adapter{
		session:  session,
		scenario: scenario,
		support: contractpowerv1.Support{
			State: contractpowerv1.StateSupport{},
			Operations: contractpowerv1.OperationSupport{
				Set: contractpowerv1.SetSupport{},
			},
		},
	}, nil
}

func (simulator *Adapter) Support() contractpowerv1.Support { return simulator.support }

// EntityEventSupport returns the generated support for the scenario's event
// source Entity: no State, no Operations, and exactly the synthetic report
// names the operator input loop publishes.
func (simulator *Adapter) EntityEventSupport() contractenumeventv1.Support {
	return contractenumeventv1.Support{
		State:      contractenumeventv1.StateSupport{},
		Operations: contractenumeventv1.OperationSupport{},
		Events: contractenumeventv1.SupportEvents{
			Names: contractenumeventv1.SupportEventsNames{
				EntityEventSinglePress, EntityEventDoublePress,
			},
		},
	}
}

// InitializeEntityEventSource binds the canonical ID of a registered event
// source Entity and reports it available. EmitEntityEvent refuses to publish
// before this call, so the simulator never guesses an Entity identity.
func (simulator *Adapter) InitializeEntityEventSource(ctx context.Context, entityID string) error {
	if entityID == "" {
		return errors.New("event source Entity ID is required")
	}
	if err := simulator.session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{{
		EntityID: entityID, Status: adapter.AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}}); err != nil {
		return err
	}
	simulator.mutex.Lock()
	simulator.eventEntityID = entityID
	simulator.mutex.Unlock()
	return nil
}

// EmitEntityEvent validates one reported name against the generated event
// source support and publishes it as a synthetic report. It is deterministic:
// tests call it directly instead of driving standard input, and the generated
// facade owns support and name validation.
func (simulator *Adapter) EmitEntityEvent(ctx context.Context, name string) (adapter.EntityEventID, error) {
	simulator.mutex.Lock()
	entityID := simulator.eventEntityID
	simulator.mutex.Unlock()
	if entityID == "" {
		return "", errors.New("event source Entity is not initialized")
	}
	event, err := sdkadapterenumeventv1.NewEntityEvent(sdkadapterenumeventv1.EntityEventInput{
		EntityID: entityID, Support: simulator.EntityEventSupport(), Name: name,
	})
	if err != nil {
		return "", err
	}
	return simulator.session.PublishEntityEvent(ctx, event)
}

func (simulator *Adapter) Initialize(ctx context.Context, entityID string) error {
	now := time.Now().UTC()
	if simulator.scenario == ScenarioAdapterUnhealthy {
		return simulator.session.SetHealth(ctx, adapter.HealthReport{
			Status: adapter.HealthUnhealthy, SourceObservedAt: now,
			ReasonCode: "hearth.external_system_unavailable",
		})
	}
	if err := simulator.session.SetHealth(ctx, adapter.HealthReport{
		Status: adapter.HealthHealthy, SourceObservedAt: now,
	}); err != nil {
		return err
	}
	report := adapter.EntityAvailabilityReport{
		EntityID: entityID, Status: adapter.AvailabilityAvailable, SourceObservedAt: now,
	}
	if simulator.scenario == ScenarioEntityUnavailable {
		report.Status = adapter.AvailabilityUnavailable
		report.ReasonCode = "adapter.hearth-simulator.entity_unavailable"
	}
	if err := simulator.session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{report}); err != nil {
		return err
	}
	if simulator.scenario == ScenarioEntityUnavailable {
		return nil
	}
	return simulator.PublishInitial(ctx, entityID)
}

func (simulator *Adapter) PublishInitial(ctx context.Context, entityID string) error {
	now := time.Now().UTC()
	adapterReceivedAt := now
	var sourceUpdatedAt *time.Time
	switch simulator.scenario {
	case ScenarioDelayedSourceTime:
		value := now.Add(-24 * time.Hour)
		sourceUpdatedAt = &value
	case ScenarioFutureClockSkew:
		adapterReceivedAt = now.Add(futureObservationOffset)
	}
	simulator.mutex.Lock()
	state := simulator.state
	simulator.mutex.Unlock()
	observation, err := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
		EntityID: entityID, Support: simulator.support, State: state,
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: sourceUpdatedAt,
	})
	if err != nil {
		return err
	}
	_, err = simulator.session.PublishObservation(ctx, observation)
	return err
}

func (simulator *Adapter) CommandHandler(entityID string) (adapter.CommandHandler, error) {
	return sdkpowerv1.NewCommandHandler(entityID, simulator.support, sdkpowerv1.Handlers{
		Set: simulator.set,
	})
}

func (simulator *Adapter) set(
	ctx context.Context,
	command typed.Command[contractpowerv1.SetParameters],
	responder adapter.Responder,
) error {
	if simulator.scenario == ScenarioUpstreamRejection {
		return responder.Reject("simulated upstream rejection")
	}
	if simulator.scenario == ScenarioEntityUnavailable {
		if err := simulator.session.ReportEntityAvailability(ctx, []adapter.EntityAvailabilityReport{{
			EntityID: command.EntityID, Status: adapter.AvailabilityAvailable,
			SourceObservedAt: time.Now().UTC(),
		}}); err != nil {
			return err
		}
	}
	simulator.mutex.Lock()
	simulator.state = contractpowerv1.State(command.Parameters.Value)
	state := simulator.state
	simulator.mutex.Unlock()
	evidence, err := responder.Accept()
	if err != nil {
		return err
	}
	if simulator.scenario == ScenarioOutcomeTimeout || simulator.scenario == ScenarioInterruptedCommand {
		return nil
	}
	observation, err := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
		EntityID: command.EntityID, Support: simulator.support, State: state,
		AdapterReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	_, err = evidence.PublishObservation(ctx, observation)
	return err
}
