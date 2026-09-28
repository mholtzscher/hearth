package automations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type heldAdmissionProbe struct {
	automations.Repository

	cutoff, evaluatedAt time.Time
}

func (probe *heldAdmissionProbe) ListDueHeldStates(
	_ context.Context,
	at time.Time,
	_ int,
) ([]automations.HeldStateCandidate, error) {
	probe.cutoff = at
	return []automations.HeldStateCandidate{{AutomationID: "held-automation", Revision: 1}}, nil
}

func (*heldAdmissionProbe) GetAutomation(context.Context, automations.AutomationID) (automations.Record, error) {
	return automations.Record{Revision: 1, Definition: automations.Definition{Conditions: &automations.Condition{
		ID: "present", Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
		},
	}}}, nil
}

func (probe *heldAdmissionProbe) AdmitDueHeldStates(
	_ context.Context, _ devices.EntityStateSnapshot, cutoff, evaluatedAt time.Time, _ int,
) (automations.AdmissionResult, int, error) {
	probe.cutoff, probe.evaluatedAt = cutoff, evaluatedAt
	return automations.AdmissionResult{}, 0, nil
}

type heldSnapshotProbe struct {
	automations.AutomationDevices

	onRead func()
}

func (heldSnapshotProbe) CommandAdmissionOpen() bool { return true }

func (probe heldSnapshotProbe) GetEntityStateSnapshot(
	_ context.Context,
	_ []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	probe.onRead()
	return devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}, nil
}

func TestHeldStateAdmissionSamplesClockAfterSnapshotAndPreservesCutoff(t *testing.T) {
	t.Parallel()
	cutoff := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	observedAt := cutoff.Add(time.Second)
	current := cutoff
	repository := &heldAdmissionProbe{}
	service := automations.NewService(repository, heldSnapshotProbe{onRead: func() { current = observedAt }},
		automations.Dependencies{Now: func() time.Time { return current }, HeldStateStartupAt: cutoff})
	_, err := service.ProcessDueHeldStates(context.Background(), cutoff, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !repository.cutoff.Equal(cutoff) || !repository.evaluatedAt.Equal(observedAt) {
		t.Fatalf("admission cutoff=%s evaluatedAt=%s, want %s and %s",
			repository.cutoff, repository.evaluatedAt, cutoff, observedAt)
	}
}
