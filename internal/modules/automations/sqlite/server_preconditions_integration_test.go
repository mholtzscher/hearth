//go:build integration

package sqlite_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestCoreMetadataPreconditionsAreNotInvalidInput(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	id, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	fact := observationFactFor(t, newEntityID(t), at)
	ctx := t.Context()
	for name, check := range map[string]func() error{
		"Fact admission time": func() error {
			_, e := repository.AdmitDeviceFact(ctx, fact, devices.EntityStateSnapshot{}, time.Time{}, at)
			return e
		},
		"Fact startup time": func() error {
			_, e := repository.AdmitDeviceFact(ctx, fact, devices.EntityStateSnapshot{}, at, time.Time{})
			return e
		},
		"manual admission time": func() error {
			_, e := repository.AdmitManualRun(ctx, automations.ManualRunInput{AutomationID: id}, devices.EntityStateSnapshot{}, time.Time{})
			return e
		},
		"schedule activation time": func() error { return repository.InitializeScheduleWatermark(ctx, time.Time{}) },
		"schedule preflight time":  func() error { _, e := repository.ScheduleMinuteProcessed(ctx, time.Time{}); return e },
		"schedule admission time": func() error {
			_, e := repository.AdmitDueSchedules(ctx, devices.EntityStateSnapshot{}, automations.ScheduleTick{Location: time.UTC})
			return e
		},
		"schedule location": func() error {
			_, e := repository.AdmitDueSchedules(ctx, devices.EntityStateSnapshot{}, automations.ScheduleTick{At: at})
			return e
		},
		"held selection time": func() error { _, e := repository.ListDueHeldStates(ctx, time.Time{}, 1); return e },
		"held admission cutoff": func() error {
			_, _, e := repository.AdmitDueHeldStates(ctx, devices.EntityStateSnapshot{}, time.Time{}, at, 1)
			return e
		},
		"held evaluation time": func() error {
			_, _, e := repository.AdmitDueHeldStates(ctx, devices.EntityStateSnapshot{}, at, time.Time{}, 1)
			return e
		},
		"interruption time": func() error { return repository.InterruptRun(ctx, runID, time.Time{}) },
		"startup recovery time": func() error {
			return repository.InterruptActiveRuns(ctx, time.Time{}, automations.FailureCoreRestarted)
		},
		"retention cutoff": func() error { _, e := repository.DeleteHistoryBefore(ctx, time.Time{}, 1); return e },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failure := check()
			if failure == nil || errors.Is(failure, automations.ErrInvalidAutomation) ||
				errors.Is(
					failure,
					automations.ErrInvalidDeviceFact,
				) || errors.Is(failure, automations.ErrAutomationNotFound) || errors.Is(failure, automations.ErrAdmissionUnavailable) {
				t.Fatalf("Core-owned precondition error=%v, want internal failure", failure)
			}
		})
	}
}

func TestCoreMetadataChecksPreserveInputValidation(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	ctx := t.Context()
	if _, err := repository.AdmitDeviceFact(
		ctx,
		nil,
		devices.EntityStateSnapshot{},
		time.Time{},
		time.Time{},
	); !errors.Is(
		err,
		automations.ErrInvalidDeviceFact,
	) {
		t.Fatalf("malformed Fact lost wire classification: %v", err)
	}
	for name, check := range map[string]func() error{
		"manual identity": func() error {
			_, e := repository.AdmitManualRun(ctx, automations.ManualRunInput{AutomationID: "bad"}, devices.EntityStateSnapshot{}, time.Time{})
			return e
		},
		"held selection limit": func() error { _, e := repository.ListDueHeldStates(ctx, time.Time{}, 0); return e },
		"held admission limit": func() error {
			_, _, e := repository.AdmitDueHeldStates(ctx, devices.EntityStateSnapshot{}, time.Time{}, time.Time{}, 0)
			return e
		},
		"retention limit":       func() error { _, e := repository.DeleteHistoryBefore(ctx, time.Time{}, 0); return e },
		"interruption identity": func() error { return repository.InterruptRun(ctx, "bad", time.Time{}) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if failure := check(); !errors.Is(failure, automations.ErrInvalidAutomation) {
				t.Fatalf("malformed input lost classification: %v", failure)
			}
		})
	}
}
