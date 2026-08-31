package simulator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

const (
	futureObservationOffset = 2 * time.Minute

	ScenarioHappy               = "happy"
	ScenarioUnavailableAdapter  = "unavailable-adapter"
	ScenarioUpstreamRejection   = "upstream-rejection"
	ScenarioOutcomeTimeout      = "outcome-timeout"
	ScenarioNoOpRefresh         = "no-op-refresh"
	ScenarioOverlappingCommands = "overlapping-opposite-command"
	ScenarioInterruptedCommand  = "interrupted-command"
	ScenarioDelayedSourceTime   = "delayed-source-time"
	ScenarioFutureClockSkew     = "future-clock-skew"
	ScenarioRestartBeforeAck    = "restart-before-ack"
)

func ValidScenario(value string) bool {
	switch value {
	case ScenarioHappy, ScenarioUnavailableAdapter, ScenarioUpstreamRejection,
		ScenarioOutcomeTimeout, ScenarioNoOpRefresh, ScenarioOverlappingCommands,
		ScenarioInterruptedCommand, ScenarioDelayedSourceTime, ScenarioFutureClockSkew,
		ScenarioRestartBeforeAck:
		return true
	default:
		return false
	}
}

type ObservationPublisher interface {
	PublishObservation(context.Context, adapter.Observation) (adapter.ObservationID, error)
}

type Adapter struct {
	publisher ObservationPublisher
	scenario  string
	support   contractpowerv1.Support
	mutex     sync.Mutex
	state     contractpowerv1.State
}

func New(publisher ObservationPublisher, scenario string) (*Adapter, error) {
	if publisher == nil {
		return nil, errors.New("simulator observation publisher is required")
	}
	if !ValidScenario(scenario) {
		return nil, fmt.Errorf("unknown simulator scenario %q", scenario)
	}
	return &Adapter{
		publisher: publisher,
		scenario:  scenario,
		support: contractpowerv1.Support{
			State: contractpowerv1.StateSupport{},
			Operations: contractpowerv1.OperationSupport{
				Set: contractpowerv1.SetSupport{},
			},
		},
	}, nil
}

func (simulator *Adapter) Support() contractpowerv1.Support { return simulator.support }

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
	_, err = simulator.publisher.PublishObservation(ctx, observation)
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
	simulator.mutex.Lock()
	simulator.state = contractpowerv1.State(command.Parameters.Value)
	state := simulator.state
	simulator.mutex.Unlock()
	if err := responder.Accept(); err != nil {
		return err
	}
	if simulator.scenario == ScenarioOutcomeTimeout || simulator.scenario == ScenarioInterruptedCommand {
		return nil
	}
	commandID := command.ID
	observation, err := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
		EntityID: command.EntityID, Support: simulator.support, State: state,
		AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &commandID,
	})
	if err != nil {
		return err
	}
	_, err = simulator.publisher.PublishObservation(ctx, observation)
	return err
}
