package esphome

import (
	"context"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// Entity availability reason codes.
const (
	reasonDeviceUnreachable      = "adapter.hearth-adapter-esphome.device_unreachable"
	reasonEncryptionKeyRejected  = "adapter.hearth-adapter-esphome.encryption_key_rejected"
	reasonPlaintextRejected      = "adapter.hearth-adapter-esphome.plaintext_rejected"
	reasonDeviceIdentityMismatch = "adapter.hearth-adapter-esphome.device_identity_mismatch"
	reasonIncompatibleAPI        = "adapter.hearth-adapter-esphome.incompatible_api"
	reasonDeepSleepUnsupported   = "adapter.hearth-adapter-esphome.deep_sleep_unsupported"
	reasonPlanningFailed         = "adapter.hearth-adapter-esphome.planning_failed"
	reasonEntityMissing          = "adapter.hearth-adapter-esphome.entity_missing"
	reasonDeviceNotConfigured    = "adapter.hearth-adapter-esphome.device_not_configured"
	reasonDeviceBlocked          = "adapter.hearth-adapter-esphome.device_blocked"
	reasonDiscoveryPending       = "adapter.hearth-adapter-esphome.discovery_pending"
	reasonEncryptionKeyRequired  = "adapter.hearth-adapter-esphome.encryption_key_required"
)

// availabilityPage is the largest availability batch the SDK accepts.
const availabilityPage = 256

// connectionReason classifies why a connection attempt or generation ended.
func connectionReason(err error) string {
	var unsupported *unsupportedDeviceError
	switch {
	case errors.As(err, &unsupported):
		return unsupported.Reason
	case errors.Is(err, nativeapi.ErrEncryptionKeyRejected):
		return reasonEncryptionKeyRejected
	case errors.Is(err, nativeapi.ErrPlaintextDevice):
		return reasonPlaintextRejected
	case errors.Is(err, nativeapi.ErrIdentityMismatch):
		return reasonDeviceIdentityMismatch
	case errors.Is(err, nativeapi.ErrIncompatibleAPI):
		return reasonIncompatibleAPI
	default:
		return reasonDeviceUnreachable
	}
}

// configurationReason reports reasons the operator must fix; reconnect waits at the backoff ceiling for them.
func configurationReason(reason string) bool {
	return reason != reasonDeviceUnreachable
}

// availabilityStatus is one Entity's last reported availability.
type availabilityStatus struct {
	available bool
	reason    string
}

// availabilityTracker reports only status or reason changes. It is owned by one goroutine.
type availabilityTracker struct {
	reported map[string]availabilityStatus
}

func newAvailabilityTracker() *availabilityTracker {
	return &availabilityTracker{reported: map[string]availabilityStatus{}}
}

// changes prepares reports without marking them accepted. commit records them after the SDK acknowledges them.
func (tracker *availabilityTracker) changes(
	wanted map[string]availabilityStatus,
	observedAt time.Time,
) []adapter.EntityAvailabilityReport {
	var reports []adapter.EntityAvailabilityReport
	for entityID, status := range wanted {
		if previous, known := tracker.reported[entityID]; known && previous == status {
			continue
		}
		report := adapter.EntityAvailabilityReport{EntityID: entityID, SourceObservedAt: observedAt}
		if status.available {
			report.Status = adapter.AvailabilityAvailable
		} else {
			report.Status = adapter.AvailabilityUnavailable
			report.ReasonCode = status.reason
		}
		reports = append(reports, report)
	}
	return reports
}

// commit records reports that the SDK accepted.
func (tracker *availabilityTracker) commit(reports []adapter.EntityAvailabilityReport) {
	for _, report := range reports {
		tracker.reported[report.EntityID] = availabilityStatus{
			available: report.Status == adapter.AvailabilityAvailable,
			reason:    report.ReasonCode,
		}
	}
}

// reportAvailability sends reports in SDK-sized batches.
func reportAvailability(ctx context.Context, session Session, reports []adapter.EntityAvailabilityReport) error {
	for start := 0; start < len(reports); start += availabilityPage {
		end := min(start+availabilityPage, len(reports))
		if err := session.ReportEntityAvailability(ctx, reports[start:end]); err != nil {
			return err
		}
	}
	return nil
}
