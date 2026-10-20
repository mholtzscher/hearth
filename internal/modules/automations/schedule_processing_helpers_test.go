package automations_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func scheduledDefinition(t *testing.T, expression string, condition *automations.Condition) automations.Definition {
	t.Helper()
	definition := runtimeDefinition(t, 1)
	definition.Triggers = []automations.Trigger{
		{ID: "scheduled", Body: automations.CronTrigger{Expression: expression}},
	}
	definition.Conditions = condition
	return definition
}

type scheduleAdmissionRepository struct {
	automations.Repository

	beforeListReturn func()
	admit            func(context.Context, devices.EntityStateSnapshot, automations.ScheduleTick) (automations.AdmissionResult, error)
	executorWrites   atomic.Int64
}

func (repository *scheduleAdmissionRepository) MarkStepRunning(ctx context.Context, start automations.StepStart) error {
	repository.executorWrites.Add(1)
	return repository.Repository.MarkStepRunning(ctx, start)
}

func (repository *scheduleAdmissionRepository) CompleteRun(
	ctx context.Context,
	completion automations.RunCompletion,
) error {
	repository.executorWrites.Add(1)
	return repository.Repository.CompleteRun(ctx, completion)
}

func (repository *scheduleAdmissionRepository) ListEnabledAutomations(
	ctx context.Context,
) ([]automations.Record, error) {
	records, err := repository.Repository.ListEnabledAutomations(ctx)
	if repository.beforeListReturn != nil {
		repository.beforeListReturn()
	}
	return records, err
}

func (repository *scheduleAdmissionRepository) AdmitDueSchedules(
	ctx context.Context, snapshot devices.EntityStateSnapshot, tick automations.ScheduleTick,
) (automations.AdmissionResult, error) {
	if repository.admit != nil {
		return repository.admit(ctx, snapshot, tick)
	}
	return repository.Repository.AdmitDueSchedules(ctx, snapshot, tick)
}
