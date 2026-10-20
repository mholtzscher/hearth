//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// blockingAutomationDevices holds a Command in flight while tests inspect shutdown.
type blockingAutomationDevices struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAutomationDevices() *blockingAutomationDevices {
	return &blockingAutomationDevices{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (seam *blockingAutomationDevices) ValidateObservationTrigger(context.Context, devices.EntityID, []string) error {
	return nil
}

func (seam *blockingAutomationDevices) ValidateConditionEntity(context.Context, devices.EntityID) error {
	return nil
}

func (seam *blockingAutomationDevices) GetEntityStateSnapshot(
	context.Context, []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	return devices.EntityStateSnapshot{}, nil
}

func (seam *blockingAutomationDevices) ValidateEntityEventTrigger(
	context.Context, devices.EntityID, devices.EntityEventName,
) error {
	return nil
}

func (seam *blockingAutomationDevices) ValidateCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandParameters, error) {
	return input.Parameters, nil
}

func (seam *blockingAutomationDevices) CommandAdmissionOpen() bool { return true }

func (seam *blockingAutomationDevices) ExecuteCommand(
	context.Context, devices.CommandInput,
) (devices.CommandResult, error) {
	seam.once.Do(func() { close(seam.entered) })
	<-seam.release
	return devices.CommandResult{}, errors.New("released by test")
}

func (seam *blockingAutomationDevices) GetCommand(
	context.Context, devices.CommandID,
) (devices.CommandRecord, error) {
	return devices.CommandRecord{}, devices.ErrCommandNotFound
}
