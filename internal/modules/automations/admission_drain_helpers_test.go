package automations_test

import (
	"context"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// blockingAdmissionRepository holds admission before commit and worker registration.
type blockingAdmissionRepository struct {
	automations.Repository

	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func newBlockingAdmissionRepository(
	repository automations.Repository,
) *blockingAdmissionRepository {
	return &blockingAdmissionRepository{
		Repository: repository,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (repository *blockingAdmissionRepository) AdmitManualRun(
	ctx context.Context,
	input automations.ManualRunInput,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
) (automations.ManualAdmissionResult, error) {
	repository.enteredOnce.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.Repository.AdmitManualRun(ctx, input, snapshot, now)
}

func (repository *blockingAdmissionRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
	startupAt time.Time,
) (automations.AdmissionResult, error) {
	repository.enteredOnce.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.Repository.AdmitDeviceFact(ctx, fact, snapshot, now, startupAt)
}
