package zigbee2mqtt

import (
	"context"
	"errors"
	"time"

	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractcolortempv1 "github.com/mholtzscher/hearth/entitytypes/colortempv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkbrightnessv1 "github.com/mholtzscher/hearth/sdk/adapter/brightnessv1"
	sdkcolortempv1 "github.com/mholtzscher/hearth/sdk/adapter/colortempv1"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
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
	for _, decoded := range states {
		result := make(chan stateDisposition, 1)
		candidate := stateCandidate{
			ctx:           ctx,
			generation:    generation,
			routeRevision: routeRevision,
			entityID:      byKey[decoded.Entity.Descriptor.Key],
			state:         decoded,
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

func newObservation(
	entityID string,
	state decodedEntityState,
	receivedAt time.Time,
) (adapter.Observation, error) {
	switch state.Entity.Kind {
	case entityKindPower:
		return sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
			EntityID: entityID, Support: powerSupport(), State: contractpowerv1.State(state.Power),
			AdapterReceivedAt: receivedAt,
		})
	case entityKindBrightness:
		return sdkbrightnessv1.NewObservation(sdkbrightnessv1.ObservationInput{
			EntityID: entityID, Support: brightnessSupport(), State: contractbrightnessv1.State(state.Brightness),
			AdapterReceivedAt: receivedAt,
		})
	case entityKindColorTemp:
		return sdkcolortempv1.NewObservation(sdkcolortempv1.ObservationInput{
			EntityID:          entityID,
			Support:           colorTempSupport(state.Entity),
			State:             contractcolortempv1.State(state.ColorTemp),
			AdapterReceivedAt: receivedAt,
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

func colorTempSupport(entity discoveredEntity) contractcolortempv1.Support {
	return contractcolortempv1.Support{
		State: contractcolortempv1.StateSupport{
			Minimum: entity.ColorTempMinimum,
			Maximum: entity.ColorTempMaximum,
		},
		Operations: contractcolortempv1.OperationSupport{Set: contractcolortempv1.SetSupport{Step: 1}},
	}
}
