package automations

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationAdmissionTimeout bounds one automatic or manual admission,
// including every Condition State snapshot read and every repository retry. The
// NATS consumer exposes it as DeviceFactAdmissionTimeout rather than repeating
// the duration, and HTTP never imports NATS.
const AutomationAdmissionTimeout = 2 * time.Second

// Bounded Condition coverage recovery. The initial empty-snapshot attempt counts
// as an attempt, so one admission performs at most two coherent batch reads and
// three repository attempts inside AutomationAdmissionTimeout. An admission that
// still lacks coverage returns [ErrConditionSnapshotUnstable] instead of looping,
// sleeping, or resetting its deadline.
const (
	automationAdmissionAttempts      = 3
	automationAdmissionSnapshotReads = 2
)

// ManualRunInput carries explicit operator intent for one manual admission. It
// accepts no Command identities: Steps and Commands come from the transaction's
// current definition snapshot alone.
type ManualRunInput struct {
	AutomationID     AutomationID
	BypassConditions bool
}

// ManualAdmissionResult is exactly one successfully committed Run or Skip. The
// repository returns it only after the transaction commits, so the Service can
// register a Run worker or report a committed Condition Skip without risking a
// rollback of the required history.
type ManualAdmissionResult struct {
	Run  *AutomationRun
	Skip *AutomationSkip
}

// emptyEntityStateSnapshot is the first-attempt evidence: no requested Entity is
// covered, which the repository reports as a coverage error rather than treating
// a missing key as absent State.
func emptyEntityStateSnapshot() devices.EntityStateSnapshot {
	return devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}
}

// admitAutomaticFact runs one Device Fact admission to a committed result within
// the bounded coverage protocol: the reservation already held by the caller
// spans every attempt and snapshot read, each attempt uses a fresh decision time,
// and each coverage error replaces the whole snapshot instead of merging samples.
func (service *Service) admitAutomaticFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionResult, error) {
	return admitWithCoverageRecovery(ctx, service, func(
		attemptContext context.Context,
		snapshot devices.EntityStateSnapshot,
	) (AdmissionResult, error) {
		return service.repository.AdmitDeviceFact(
			attemptContext, fact, snapshot, service.dependencies.Now(),
		)
	})
}

// admitManualRun runs one manual admission under the same bounded coverage
// protocol as an automatic Fact. A bypass or unconditioned definition never
// requests coverage, so it reads no State at all.
func (service *Service) admitManualRun(
	ctx context.Context,
	input ManualRunInput,
) (ManualAdmissionResult, error) {
	return admitWithCoverageRecovery(ctx, service, func(
		attemptContext context.Context,
		snapshot devices.EntityStateSnapshot,
	) (ManualAdmissionResult, error) {
		return service.repository.AdmitManualRun(
			attemptContext, input, snapshot, service.dependencies.Now(),
		)
	})
}

// admitWithCoverageRecovery runs the bounded Condition coverage protocol for one
// admission: at most three repository attempts and two coherent replacement
// reads inside the shared two-second budget, with each coverage error replaced
// rather than merged. It returns the first committed outcome, or
// [ErrConditionSnapshotUnstable] when coverage never stabilizes. The generic
// result keeps automatic Fact and manual admission on one protocol without
// duplicating the budget, retry, and error classification rules.
func admitWithCoverageRecovery[T any](
	ctx context.Context,
	service *Service,
	attempt func(context.Context, devices.EntityStateSnapshot) (T, error),
) (T, error) {
	var zero T
	admissionContext, cancel := context.WithTimeout(ctx, AutomationAdmissionTimeout)
	defer cancel()
	snapshot := emptyEntityStateSnapshot()
	reads := 0
	for attemptIndex := 1; attemptIndex <= automationAdmissionAttempts; attemptIndex++ {
		result, err := attempt(admissionContext, snapshot)
		var coverage *ConditionSnapshotRequiredError
		switch {
		case errors.As(err, &coverage):
			if reads >= automationAdmissionSnapshotReads {
				return zero, ErrConditionSnapshotUnstable
			}
			reads++
			snapshot, err = service.readConditionStateSnapshot(admissionContext, coverage.RequiredEntityIDs)
			if err != nil {
				return zero, classifyAdmissionError(ctx, admissionContext, err)
			}
		case err != nil:
			return zero, classifyAdmissionError(ctx, admissionContext, err)
		default:
			return result, nil
		}
	}
	return zero, ErrConditionSnapshotUnstable
}

// readConditionStateSnapshot reads one complete replacement snapshot through the
// devices seam. It never merges a previous sample, and it preserves
// [devices.ErrEntityStateSnapshotCorrupt] through the returned error so the
// caller retains the Fact and negatively acknowledges it.
func (service *Service) readConditionStateSnapshot(
	ctx context.Context,
	ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	if service.devices == nil {
		return devices.EntityStateSnapshot{}, errors.New("automation condition state source is unavailable")
	}
	return service.devices.GetEntityStateSnapshot(ctx, ids)
}

// classifyAdmissionError maps a snapshot acquisition or repository deadline or
// cancellation to [ErrConditionSnapshotUnstable] so transport callers can answer
// a safe HTTP 503 condition_snapshot_unavailable. Caller cancellation and
// deadline keep their own error because the server may no longer be able to
// respond; corrupt and ordinary storage failures pass through unchanged so the
// existing 500 mapping and diagnostics stay intact.
func classifyAdmissionError(callerContext, admissionContext context.Context, err error) error {
	if callerContext.Err() != nil {
		return err
	}
	if admissionContext.Err() != nil || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %w", ErrConditionSnapshotUnstable, err)
	}
	return err
}

// logConditionStateCorrupt records the fixed, value-free diagnostic for unusable
// stored State. It never logs a selected value, an operand, a definition, or raw
// stored JSON, and it never turns corruption into an unknown Condition result.
func (service *Service) logConditionStateCorrupt(ctx context.Context, err error) {
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		return
	}
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation condition state is corrupt",
		slog.String("event", "automation.condition_state_corrupt"),
	)
}
