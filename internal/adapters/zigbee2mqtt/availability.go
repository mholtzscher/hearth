package zigbee2mqtt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// availabilityPayload is a per-Device availability message.
type availabilityPayload struct {
	State string `json:"state"`
}

// decodeAvailability accepts only Zigbee2MQTT's explicit online and offline evidence.
func decodeAvailability(payload []byte) (bool, error) {
	var availability availabilityPayload
	if err := decodeJSON(payload, &availability); err != nil {
		return false, fmt.Errorf("decode Zigbee2MQTT availability: %w", err)
	}
	switch availability.State {
	case upstreamOnline:
		return true, nil
	case upstreamOffline:
		return false, nil
	default:
		return false, fmt.Errorf("unsupported availability state %q", availability.State)
	}
}

//nolint:gocognit // The reason precedence directly mirrors the owned-mapping reconciliation contract.
func (z2m *Adapter) reportReconciledAvailability(
	ctx context.Context,
	inventory inventoryDiscovery,
	snapshot routeSnapshot,
	evidence map[string]availabilityEvidence,
) error {
	currentIEEE := make(map[mappingKey]string)
	for _, device := range snapshot.devices {
		for _, entity := range device.entities {
			currentIEEE[mappingKey{binding: device.bindingKey, entity: entity.plan.Descriptor.Key}] =
				device.ieeeAddress
		}
	}
	present := make(map[string]struct{})
	disabled := make(map[string]struct{})
	for _, device := range inventory.Devices {
		present[device.Registration.BindingKey] = struct{}{}
	}
	for _, rejection := range inventory.Rejections {
		if rejection.IEEEAddress == "" {
			continue
		}
		binding := "z2m-" + strings.TrimPrefix(rejection.IEEEAddress, "0x")
		present[binding] = struct{}{}
		if rejection.Code == rejectionDisabled {
			disabled[binding] = struct{}{}
		}
	}

	reports := make([]adapter.EntityAvailabilityReport, 0, len(z2m.knownOrder))
	for _, key := range z2m.knownOrder {
		mapping := z2m.knownMappings[key]
		report := adapter.EntityAvailabilityReport{EntityID: mapping.EntityID, SourceObservedAt: time.Now().UTC()}
		ieeeAddress, current := currentIEEE[key]
		if !current {
			report.Status = adapter.AvailabilityUnavailable
			if _, isDisabled := disabled[key.binding]; isDisabled {
				report.ReasonCode = deviceDisabledReason
			} else if _, isPresent := present[key.binding]; isPresent {
				report.ReasonCode = capabilityMissingReason
			} else {
				report.ReasonCode = deviceMissingReason
			}
			reports = append(reports, report)
			continue
		}
		if available, exists := evidence[ieeeAddress]; exists {
			report.SourceObservedAt = available.receivedAt
			if available.available {
				report.Status = adapter.AvailabilityAvailable
			} else {
				report.Status = adapter.AvailabilityUnavailable
				report.ReasonCode = deviceOfflineReason
			}
			reports = append(reports, report)
		}
	}
	return z2m.reportAvailability(ctx, reports)
}

// clearAvailabilityEvidence drops cached availability and every queued
// availability message while preserving queued State and Event occurrences, so
// an unhealthy bridge cannot replay stale availability evidence but a pending
// occurrence still waits for the next reconciled route snapshot.
func (z2m *Adapter) clearAvailabilityEvidence(state *connectionSync) {
	clear(state.availability)
	kept := state.pending[:0]
	for _, entry := range state.pending {
		if _, kind := parseDeviceTopic(z2m.config.BaseTopic, entry.message.Topic); kind == deviceTopicAvailability {
			continue
		}
		kept = append(kept, entry)
	}
	state.pending = kept
}

func (z2m *Adapter) cacheAvailability(
	state *connectionSync,
	message mqttMessage,
	devices map[string]runtimeDevice,
) error {
	device, kind := classifyDeviceTopic(z2m.config.BaseTopic, message.Topic, devices)
	if kind != deviceTopicAvailability {
		return errors.New("not an availability topic")
	}
	available, err := decodeAvailability(message.Payload)
	if err != nil {
		return err
	}
	state.availability[device.ieeeAddress] = availabilityEvidence{available: available, receivedAt: message.ReceivedAt}
	return nil
}

func (z2m *Adapter) reportAvailability(ctx context.Context, reports []adapter.EntityAvailabilityReport) error {
	for len(reports) > 0 {
		count := min(len(reports), availabilityPage)
		if err := z2m.session.ReportEntityAvailability(ctx, reports[:count]); err != nil {
			return &sessionOperationError{operation: "report Zigbee2MQTT Entity availability", err: err}
		}
		reports = reports[count:]
	}
	return nil
}
