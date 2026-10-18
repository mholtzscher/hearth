package nats

import (
	"context"
	"errors"
	"log/slog"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/outbox"
)

const (
	deviceFactEventRetry  = "device_fact.retry"
	deviceFactEventPoison = "device_fact.poison"
)

// DeviceFactRelay binds the shared publisher to Device Fact storage and wire
// mapping. Durability remains in the devices transaction and outbox table.
type DeviceFactRelay struct {
	*outbox.Relay[devices.PendingDeviceFact]
}

// StartDeviceFactRelay starts publication after stream provisioning. Transient
// faults remain retryable but fail readiness until delivery recovers.
func StartDeviceFactRelay(
	js jetstream.JetStream,
	store devices.DeviceFactOutbox,
	validator *contractsv1.Validator,
	logger *slog.Logger,
) (*DeviceFactRelay, error) {
	if js == nil {
		return nil, errors.New("device fact relay requires a JetStream context")
	}
	return startDeviceFactRelay(store, validator, logger,
		func(ctx context.Context, message *natsgo.Msg) (*jetstream.PubAck, error) {
			return js.PublishMsg(ctx, message)
		},
		outbox.DefaultTiming())
}

func startDeviceFactRelay(
	store devices.DeviceFactOutbox,
	validator *contractsv1.Validator,
	logger *slog.Logger,
	publish func(context.Context, *natsgo.Msg) (*jetstream.PubAck, error),
	timing outbox.Timing,
) (*DeviceFactRelay, error) {
	if store == nil || validator == nil {
		return nil, errors.New("device fact relay requires an outbox and schema validator")
	}
	source := &deviceFactSource{store: store, validator: validator, logger: defaultLogger(logger)}
	relay, err := outbox.Start(outbox.Config[devices.PendingDeviceFact]{
		Source: source, Stream: DeviceFactStreamName, Publish: publish, Timing: timing,
		IsPoison: func(err error) bool { return asDeviceFactPoisonError(err) != nil }, Report: source.report,
	})
	if err != nil {
		return nil, err
	}
	return &DeviceFactRelay{Relay: relay}, nil
}

// NotifyPendingDeviceFacts is the nonblocking post-commit hint, never an enqueue.
func (relay *DeviceFactRelay) NotifyPendingDeviceFacts() {
	if relay != nil {
		relay.Notify()
	}
}

type deviceFactSource struct {
	store     devices.DeviceFactOutbox
	validator *contractsv1.Validator
	logger    *slog.Logger
}

func (source *deviceFactSource) Read(ctx context.Context, limit int) ([]devices.PendingDeviceFact, error) {
	pending, err := source.store.ListPendingDeviceFacts(ctx, limit)
	if poison := asInvalidOutboxRowPoison(err); poison != nil {
		return pending, poison
	}
	return pending, err
}

func (source *deviceFactSource) Prepare(item devices.PendingDeviceFact) (*natsgo.Msg, error) {
	message, err := mapPendingDeviceFact(source.validator, item)
	if err != nil {
		return nil, err
	}
	return message.natsMessage(), nil
}

func (source *deviceFactSource) Delete(ctx context.Context, item devices.PendingDeviceFact) error {
	switch fact := item.Fact.(type) {
	case devices.ObservationFact:
		return source.store.DeleteDeviceFact(ctx, fact.ID)
	case devices.EntityEventFact:
		return source.store.DeleteDeviceFact(ctx, fact.ID)
	default:
		return errors.New("unknown device fact family")
	}
}

func (source *deviceFactSource) report(ctx context.Context, failure outbox.DeliveryError[devices.PendingDeviceFact]) {
	if failure.Permanent {
		source.logPoison(ctx, asDeviceFactPoisonError(failure.Cause))
		return
	}
	attributes := []slog.Attr{
		slog.String(transportEventKey, deviceFactEventRetry),
		slog.String("stage", failure.Stage),
		slog.String(transportErrorCodeKey, failure.Code),
	}
	if failure.Item != nil {
		switch fact := failure.Item.Fact.(type) {
		case devices.ObservationFact:
			attributes = append(attributes, slog.String("family", string(fact.DeviceFactFamily())),
				slog.String("observation_id", string(fact.ObservationID)))
		case devices.EntityEventFact:
			attributes = append(attributes, slog.String("family", string(fact.DeviceFactFamily())),
				slog.String("event_id", string(fact.EventID)))
		}
	}
	source.logger.LogAttrs(ctx, slog.LevelWarn, "device fact relay retry", attributes...)
}

func (source *deviceFactSource) logPoison(ctx context.Context, poison *deviceFactPoisonError) {
	attributes := []slog.Attr{
		slog.String(transportEventKey, deviceFactEventPoison),
		slog.String("stage", poison.stage),
		slog.String(transportErrorCodeKey, poison.code),
	}
	if poison.factID != "" {
		attributes = append(attributes, slog.String("fact_id", poison.factID))
	}
	if poison.family != "" {
		attributes = append(attributes, slog.String("family", string(poison.family)),
			deviceFactSourceIDAttr(poison.family, poison.sourceID))
	}
	source.logger.LogAttrs(ctx, slog.LevelError, "device fact relay faulted", attributes...)
}

func deviceFactSourceIDAttr(family devices.DeviceFactFamily, sourceID string) slog.Attr {
	switch family {
	case devices.DeviceFactFamilyObservation:
		return slog.String("observation_id", sourceID)
	case devices.DeviceFactFamilyEntityEvent:
		return slog.String("event_id", sourceID)
	}
	return slog.String("source_id", sourceID)
}
