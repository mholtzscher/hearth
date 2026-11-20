package homekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	deviceFactStream      = "HEARTH_DEVICE_FACTS_V1"
	observationFactFilter = "hearth.v1.core.fact.entity.*.observation.*"
	factInactiveThreshold = 2 * time.Minute
)

// ObservationFact is a validated Observation delivery. The HTTP read remains
// authoritative for inventory and availability.
type ObservationFact struct {
	EntityID      string
	ObservationID string
	Value         json.RawMessage
	ObservedAt    time.Time
}

// FactSource opens an ephemeral stream reader for each reconciliation generation.
type FactSource struct {
	js        jetstream.JetStream
	validator *contractsv1.Validator
	logger    *slog.Logger
}

// NewFactSource prepares schema validation without creating broker resources.
func NewFactSource(js jetstream.JetStream, logger *slog.Logger) (*FactSource, error) {
	if js == nil || logger == nil {
		return nil, errors.New("Device Fact source requires JetStream and a logger")
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile Device Fact schema: %w", err)
	}
	return &FactSource{js: js, validator: validator, logger: logger}, nil
}

// Open creates the consumer before the runtime reads HTTP inventory. Pending
// deliveries stay in JetStream until the runtime starts taking messages.
func (source *FactSource) Open(ctx context.Context) (*FactFeed, error) {
	stream, err := source.js.Stream(ctx, deviceFactStream)
	if err != nil {
		return nil, fmt.Errorf("get Device Fact stream: %w", err)
	}
	consumer, err := stream.CreateConsumer(ctx, jetstream.ConsumerConfig{
		DeliverPolicy:     jetstream.DeliverNewPolicy,
		AckPolicy:         jetstream.AckExplicitPolicy,
		MaxAckPending:     1,
		FilterSubject:     observationFactFilter,
		InactiveThreshold: factInactiveThreshold,
	})
	if err != nil {
		return nil, fmt.Errorf("create Observation Fact consumer: %w", err)
	}
	return &FactFeed{source: source, stream: stream, consumer: consumer}, nil
}

// FactFeed owns one ephemeral consumer. Use Run once and Close after Run returns.
type FactFeed struct {
	source   *FactSource
	stream   jetstream.Stream
	consumer jetstream.Consumer
}

// Run delivers matching Facts in broker order. A broken consumer ends the
// generation so the runtime can open a new feed and reload HTTP inventory.
func (feed *FactFeed) Run(ctx context.Context, deliver func(context.Context, ObservationFact) error) error {
	reader, err := feed.consumer.Messages(
		jetstream.PullMaxMessages(1), jetstream.WithMessagesErrOnMissingHeartbeat(true),
	)
	if err != nil {
		return fmt.Errorf("open Observation Fact iterator: %w", err)
	}
	defer reader.Stop()
	for {
		message, nextErr := reader.Next(jetstream.NextContext(ctx))
		if nextErr != nil {
			return fmt.Errorf("receive Observation Fact: %w", nextErr)
		}
		if applyErr := feed.apply(ctx, message, deliver); applyErr != nil {
			return applyErr
		}
	}
}

// Close removes the consumer. An already deleted consumer is not an error.
func (feed *FactFeed) Close(ctx context.Context) error {
	err := feed.stream.DeleteConsumer(ctx, feed.consumer.CachedInfo().Name)
	if err != nil && !errors.Is(err, jetstream.ErrConsumerNotFound) {
		return fmt.Errorf("delete Observation Fact consumer: %w", err)
	}
	return nil
}

func (feed *FactFeed) apply(
	ctx context.Context, message jetstream.Msg, deliver func(context.Context, ObservationFact) error,
) error {
	fact, err := decodeObservationFact(
		feed.source.validator, message.Subject(), message.Headers().Get(jetstream.MsgIDHeader), message.Data(),
	)
	if err != nil {
		feed.source.logger.WarnContext(ctx, "Observation Fact discarded",
			slog.String("event", "homekit.fact_discarded"), slog.String("error_code", "invalid_fact"))
	} else if err = deliver(ctx, fact); err != nil {
		return err
	}
	if err = message.Ack(); err != nil {
		return fmt.Errorf("acknowledge Observation Fact: %w", err)
	}
	return nil
}

type observationFactData struct {
	ObservationID string          `json:"observation_id"`
	EntityID      string          `json:"entity_id"`
	Disposition   string          `json:"disposition"`
	Value         json.RawMessage `json:"value"`
	ObservedAt    time.Time       `json:"observed_at"`
}

func decodeObservationFact(
	validator *contractsv1.Validator, subject, messageID string, payload []byte,
) (ObservationFact, error) {
	route, err := natswire.ParseDeviceFactSubject(subject)
	if err != nil || route.Family != natswire.DeviceFactFamilyObservation {
		return ObservationFact{}, errors.New("expected an Observation Fact subject")
	}
	envelope, err := natswire.Decode[observationFactData](validator, contractsv1.ObservationFactSchemaID, payload)
	if err != nil {
		return ObservationFact{}, errors.New("invalid Observation Fact envelope")
	}
	data := envelope.Data
	if route.EntityID != data.EntityID || route.Variant != data.Disposition || messageID != envelope.ID ||
		envelope.CausationID == nil || *envelope.CausationID != data.ObservationID {
		return ObservationFact{}, errors.New("mismatched Observation Fact routing or evidence")
	}
	return ObservationFact{
		EntityID: data.EntityID, ObservationID: data.ObservationID, Value: data.Value, ObservedAt: data.ObservedAt,
	}, nil
}
