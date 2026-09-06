package zigbee2mqtt

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func (z2m *Adapter) processDeviceMessage(
	ctx context.Context,
	generation uint64,
	state *connectionSync,
	message mqttMessage,
) error {
	device, kind := classifyDeviceTopic(z2m.config.BaseTopic, message.Topic, state.snapshot.devices)
	switch kind {
	case deviceTopicAvailability:
		if err := z2m.cacheAvailability(state, message, state.snapshot.devices); err != nil {
			z2m.logger.WarnContext(
				ctx,
				"ignored invalid Zigbee2MQTT availability",
				slog.String(eventKey, "adapter.availability_ignored"),
				slog.String("error_code", "invalid_availability"),
			)
			return nil //nolint:nilerr // Invalid upstream input is skipped by design; the connection continues.
		}
		evidence := state.availability[device.ieeeAddress]
		reports := make([]adapter.EntityAvailabilityReport, 0, len(device.entities))
		for _, entity := range device.entities {
			report := adapter.EntityAvailabilityReport{EntityID: entity.entityID, SourceObservedAt: evidence.receivedAt}
			if evidence.available {
				report.Status = adapter.AvailabilityAvailable
			} else {
				report.Status = adapter.AvailabilityUnavailable
				report.ReasonCode = deviceOfflineReason
			}
			reports = append(reports, report)
		}
		return z2m.reportAvailability(ctx, reports)
	case deviceTopicState:
		return z2m.publishDeviceState(ctx, generation, state.routeRevision, device, message)
	case deviceTopicUnknown:
		return nil
	}
	return nil
}

func (z2m *Adapter) publishDeviceState(
	ctx context.Context,
	generation uint64,
	routeRevision uint64,
	device runtimeDevice,
	message mqttMessage,
) error {
	states, issues, err := decodeDeviceState(message.Payload, device.entities, message.ReceivedAt)
	if err != nil {
		z2m.logger.WarnContext(
			ctx,
			"ignored malformed Zigbee2MQTT Device State",
			slog.String(eventKey, "adapter.device_state_ignored"),
			slog.String("error_code", "invalid_device_state"),
		)
		return nil //nolint:nilerr // Malformed upstream input is skipped by design; the connection continues.
	}
	for range issues {
		z2m.logger.WarnContext(
			ctx,
			"ignored invalid Zigbee2MQTT State property",
			slog.String(eventKey, "adapter.state_property_ignored"),
			slog.String("error_code", "invalid_state_property"),
		)
	}
	for _, decoded := range states {
		result := make(chan stateDisposition, 1)
		candidate := stateCandidate{
			ctx:           ctx,
			generation:    generation,
			routeRevision: routeRevision,
			entityID:      decoded.entityID,
			report:        decoded.report,
			retained:      message.Retained,
			receivedAt:    message.ReceivedAt,
			result:        result,
		}
		select {
		case z2m.runtimeEvents <- candidate:
		case <-ctx.Done():
			return ctx.Err()
		case <-z2m.runtimeDone:
			return errors.New("Zigbee2MQTT runtime stopped")
		}
		select {
		case <-result:
		case <-ctx.Done():
			return ctx.Err()
		case <-z2m.runtimeDone:
			return errors.New("Zigbee2MQTT runtime stopped")
		}
	}
	return nil
}
