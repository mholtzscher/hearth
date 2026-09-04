package zigbee2mqtt

import (
	"context"
	"errors"

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
				"topic",
				message.Topic,
				"error",
				err,
			)
			return nil
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
			"friendly_name",
			device.friendly,
			"error",
			err,
		)
		return nil
	}
	for _, issue := range issues {
		z2m.logger.WarnContext(
			ctx,
			"ignored invalid Zigbee2MQTT State property",
			"friendly_name",
			device.friendly,
			"properties",
			issue.Properties,
			"error",
			issue.Err,
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
