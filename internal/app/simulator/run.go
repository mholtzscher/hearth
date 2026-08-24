package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	registrationRetryMinimum = 100 * time.Millisecond
	registrationRetryMaximum = 2 * time.Second
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: config.AdapterID, NATSURL: config.NATSURL})
	if err != nil {
		return err
	}
	defer session.Close()
	simulated, err := simulatoradapter.New(session, config.Scenario)
	if err != nil {
		return err
	}
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: config.BindingKey + ".power", Name: "Power",
	}, simulated.Support())
	if err != nil {
		return err
	}
	deviceExternalID := config.BindingKey
	binding, err := register(ctx, session, adapter.Registration{
		BindingKey: config.BindingKey,
		Device: adapter.DeviceDescriptor{
			ExternalID: &deviceExternalID, Name: "Simulated light", Kind: "light",
		},
		Entities: []adapter.EntityDescriptor{descriptor},
	}, logger)
	if err != nil {
		return err
	}
	entityID, err := entityIDForKey(binding, "power")
	if err != nil {
		return err
	}
	switch config.Scenario {
	case simulatoradapter.ScenarioDuplicate:
		if err := publishFaultObservation(ctx, config, entityID); err != nil {
			return err
		}
	case simulatoradapter.ScenarioMalformed:
		if err := publishMalformedObservation(ctx, config, entityID); err != nil {
			return err
		}
	default:
		if err := simulated.PublishInitial(ctx, entityID); err != nil {
			return fmt.Errorf("publish initial simulator Observation: %w", err)
		}
	}
	if config.Scenario == simulatoradapter.ScenarioUnavailableAdapter {
		<-ctx.Done()
		return nil
	}
	handler, err := simulated.CommandHandler(entityID)
	if err != nil {
		return err
	}
	if err := session.ServeCommands(ctx, handler); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, adapter.ErrClosed) {
		return err
	}
	return nil
}

func register(ctx context.Context, session *adapter.Session, registration adapter.Registration, logger *slog.Logger) (adapter.Binding, error) {
	delay := registrationRetryMinimum
	for {
		binding, err := session.Register(ctx, registration)
		if err == nil {
			return binding, nil
		}
		var validation *adapter.ValidationError
		var rejected *adapter.RegistrationRejectedError
		if errors.As(err, &validation) || errors.As(err, &rejected) {
			return adapter.Binding{}, err
		}
		logger.Warn("retry simulator registration", "error", err, "retry_in", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return adapter.Binding{}, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > registrationRetryMaximum {
			delay = registrationRetryMaximum
		}
	}
}

func entityIDForKey(binding adapter.Binding, key string) (string, error) {
	for _, entity := range binding.Entities {
		if entity.Key == key {
			return entity.EntityID, nil
		}
	}
	return "", fmt.Errorf("registration response omitted Entity key %q", key)
}

func publishFaultObservation(ctx context.Context, config Config, entityID string) error {
	observationID, err := faultID("obs")
	if err != nil {
		return err
	}
	correlationID, err := faultID("cor")
	if err != nil {
		return err
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload, err := platformnats.Encode(validator, contractsv1.ObservationSchemaID, platformnats.Envelope[platformnats.Observation]{
		ID: observationID, Schema: contractsv1.ObservationSchemaID, EmittedAt: now, CorrelationID: correlationID,
		Data: platformnats.Observation{
			EntityID: entityID, Value: json.RawMessage(`false`), AdapterReceivedAt: now,
		},
	})
	if err != nil {
		return err
	}
	return publishRawObservation(ctx, config, entityID, observationID, payload, true)
}

func publishMalformedObservation(ctx context.Context, config Config, entityID string) error {
	observationID, err := faultID("obs")
	if err != nil {
		return err
	}
	return publishRawObservation(ctx, config, entityID, observationID, []byte("{"), false)
}

func publishRawObservation(
	ctx context.Context,
	config Config,
	entityID string,
	observationID string,
	payload []byte,
	duplicate bool,
) error {
	connection, err := natsgo.Connect(config.NATSURL, natsgo.Name("hearth-simulator-faults"))
	if err != nil {
		return fmt.Errorf("connect simulator fault publisher: %w", err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return fmt.Errorf("create simulator fault publisher: %w", err)
	}
	subject, err := platformnats.ObservationSubject(config.AdapterID, entityID)
	if err != nil {
		return err
	}
	message := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	message.Header.Set(natsgo.MsgIdHdr, observationID)
	if _, err := js.PublishMsg(ctx, message); err != nil {
		return fmt.Errorf("publish simulator fault Observation: %w", err)
	}
	if duplicate {
		acknowledgement, err := js.PublishMsg(ctx, message)
		if err != nil {
			return fmt.Errorf("publish duplicate simulator Observation: %w", err)
		}
		if !acknowledgement.Duplicate {
			return fmt.Errorf("duplicate simulator Observation was not deduplicated by JetStream")
		}
	}
	return nil
}

func faultID(prefix string) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate simulator %s ID: %w", prefix, err)
	}
	return prefix + "_" + id.String(), nil
}
