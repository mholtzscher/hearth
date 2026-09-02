package zigbee2mqtt

import (
	"context"
	"errors"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

func (z2m *Adapter) processDeviceMessage(
	ctx context.Context,
	generation uint64,
	state *connectionSync,
	message mqttMessage,
) error {
	z2m.mutex.Lock()
	devices := z2m.devices
	z2m.mutex.Unlock()
	device, kind := classifyDeviceTopic(z2m.config.BaseTopic, message.Topic, devices)
	switch kind {
	case deviceTopicAvailability:
		if err := z2m.cacheAvailability(state, message, devices); err != nil {
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
		return z2m.publishDeviceState(ctx, generation, device, message)
	case deviceTopicUnknown:
		return nil
	}
	return nil
}

func (z2m *Adapter) publishDeviceState(
	ctx context.Context,
	generation uint64,
	device runtimeDevice,
	message mqttMessage,
) error {
	entities := make([]discoveredEntity, 0, len(device.entities))
	byKey := make(map[string]string, len(device.entities))
	for _, entity := range device.entities {
		entities = append(entities, entity.discovered)
		byKey[entity.discovered.Descriptor.Key] = entity.entityID
	}
	states, issues, err := decodeDeviceState(message.Payload, entities)
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
			"property",
			issue.Property,
			"error",
			issue.Err,
		)
	}
	for _, state := range states {
		entityID := byKey[state.Entity.Descriptor.Key]
		if z2m.claimMatcher(generation, entityID, state, message.Retained, message.ReceivedAt) {
			continue
		}
		observation, observationErr := newObservation(entityID, state, message.ReceivedAt, nil)
		if observationErr != nil {
			return observationErr
		}
		if _, observationErr = z2m.session.PublishObservation(ctx, observation); observationErr != nil {
			return &sessionOperationError{operation: "publish Zigbee2MQTT Observation", err: observationErr}
		}
	}
	return nil
}

func newObservation(
	entityID string,
	state decodedEntityState,
	receivedAt time.Time,
	refreshForCommand *string,
) (adapter.Observation, error) {
	switch state.Entity.Kind {
	case entityKindPower:
		return sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
			EntityID: entityID, Support: powerSupport(), State: contractpowerv1.State(state.Power),
			AdapterReceivedAt: receivedAt, RefreshForCommand: refreshForCommand,
		})
	case entityKindBrightness:
		return sdkbrightnessv1.NewObservation(sdkbrightnessv1.ObservationInput{
			EntityID: entityID, Support: brightnessSupport(), State: contractbrightnessv1.State(state.Brightness),
			AdapterReceivedAt: receivedAt, RefreshForCommand: refreshForCommand,
		})
	default:
		return adapter.Observation{}, errors.New("unknown Zigbee2MQTT Entity kind")
	}
}

func powerSupport() contractpowerv1.Support {
	return contractpowerv1.Support{
		State:      contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{Set: contractpowerv1.SetSupport{}},
	}
}

func brightnessSupport() contractbrightnessv1.Support {
	return contractbrightnessv1.Support{
		State:      contractbrightnessv1.StateSupport{Maximum: hearthBrightnessMaximum},
		Operations: contractbrightnessv1.OperationSupport{Set: contractbrightnessv1.SetSupport{Step: 1}},
	}
}
