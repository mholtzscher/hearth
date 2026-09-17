package automations

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AdmissionTimeout bounds one automatic or manual admission, including its
// definition pre-read, Condition State snapshot read, and repository transaction.
const AdmissionTimeout = 2 * time.Second

// ManualRunInput carries explicit operator intent for one manual admission; no
// Command identities are accepted.
type ManualRunInput struct {
	AutomationID     AutomationID
	BypassConditions bool
}

// ManualAdmissionResult is exactly one committed Run or Skip, returned only
// after the transaction commits.
type ManualAdmissionResult struct {
	Run  *Run
	Skip *Skip
}

// emptyEntityStateSnapshot represents an admission that has no configured Conditions to evaluate.
func emptyEntityStateSnapshot() devices.EntityStateSnapshot {
	return devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}
}

// admitAutomaticFact reads the State needed by enabled matching definitions
// before opening the admission transaction.
func (service *Service) admitAutomaticFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionResult, error) {
	admissionContext, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	definitions, err := service.repository.ListEnabledAutomations(admissionContext)
	if err != nil {
		return AdmissionResult{}, err
	}
	required, err := requiredMatchingConditionEntityIDs(fact, definitions)
	if err != nil {
		return AdmissionResult{}, err
	}
	snapshot := emptyEntityStateSnapshot()
	if len(required) > 0 {
		snapshot, err = service.readConditionStateSnapshot(admissionContext, required)
		if err != nil {
			return AdmissionResult{}, err
		}
	}
	return service.repository.AdmitDeviceFact(admissionContext, fact, snapshot, service.dependencies.Now())
}

// admitManualRun reads the current definition before opening the admission transaction.
func (service *Service) admitManualRun(
	ctx context.Context,
	input ManualRunInput,
) (ManualAdmissionResult, error) {
	admissionContext, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	record, err := service.repository.GetAutomation(admissionContext, input.AutomationID)
	if err != nil {
		return ManualAdmissionResult{}, err
	}
	snapshot := emptyEntityStateSnapshot()
	if !input.BypassConditions && record.Definition.Conditions != nil {
		required, requiredErr := RequiredConditionEntityIDs(*record.Definition.Conditions)
		if requiredErr != nil {
			return ManualAdmissionResult{}, requiredErr
		}
		snapshot, err = service.readConditionStateSnapshot(admissionContext, required)
		if err != nil {
			return ManualAdmissionResult{}, err
		}
	}
	return service.repository.AdmitManualRun(admissionContext, input, snapshot, service.dependencies.Now())
}

// requiredMatchingConditionEntityIDs returns the sorted, deduplicated Entity
// union every enabled definition matching fact requires for its Conditions.
func requiredMatchingConditionEntityIDs(
	fact DeviceFact,
	definitions []Record,
) ([]devices.EntityID, error) {
	required := make(map[devices.EntityID]struct{})
	for _, record := range definitions {
		conditions := record.Definition.Conditions
		if conditions == nil {
			continue
		}
		matched, err := MatchTriggers(fact, record.Definition)
		if err != nil {
			return nil, err
		}
		if len(matched) == 0 {
			continue
		}
		entityIDs, err := RequiredConditionEntityIDs(*conditions)
		if err != nil {
			return nil, err
		}
		for _, entityID := range entityIDs {
			required[entityID] = struct{}{}
		}
	}
	ids := make([]devices.EntityID, 0, len(required))
	for entityID := range required {
		ids = append(ids, entityID)
	}
	slices.Sort(ids)
	return ids, nil
}

// readConditionStateSnapshot reads one complete snapshot through the devices
// seam, preserving [devices.ErrEntityStateSnapshotCorrupt].
func (service *Service) readConditionStateSnapshot(
	ctx context.Context,
	ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	if service.devices == nil {
		return devices.EntityStateSnapshot{}, errors.New("automation condition state source is unavailable")
	}
	return service.devices.GetEntityStateSnapshot(ctx, ids)
}

// logConditionStateCorrupt records the fixed, value-free diagnostic for unusable stored State.
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

// DecideConditions evaluates one configured Condition tree against a covering
// snapshot and reports the evaluated decision plus the Skip reason it implies
// ("" admits).
func DecideConditions(
	conditions *Condition,
	snapshot devices.EntityStateSnapshot,
	at time.Time,
) (ConditionDecision, SkipReason, error) {
	required, err := RequiredConditionEntityIDs(*conditions)
	if err != nil {
		return nil, "", err
	}
	if missing := missingSnapshotCoverage(required, snapshot); len(missing) > 0 {
		return nil, "", &ConditionSnapshotRequiredError{RequiredEntityIDs: missing}
	}
	evaluation, err := EvaluateConditions(*conditions, snapshot, at)
	if err != nil {
		return nil, "", err
	}
	decision := EvaluatedDecision(*conditions, evaluation)
	switch evaluation.Result {
	case ConditionTrue:
		return decision, "", nil
	case ConditionFalse:
		return decision, SkipConditionsFalse, nil
	case ConditionUnknown:
		return decision, SkipConditionsUnknown, nil
	default:
		return nil, "", invalid("condition evaluation has an unknown result")
	}
}

// missingSnapshotCoverage returns the required set when the snapshot does not cover all of it.
func missingSnapshotCoverage(
	required []devices.EntityID,
	snapshot devices.EntityStateSnapshot,
) []devices.EntityID {
	for _, entityID := range required {
		if _, covered := snapshot.Entries[entityID]; !covered {
			return required
		}
	}
	return nil
}
