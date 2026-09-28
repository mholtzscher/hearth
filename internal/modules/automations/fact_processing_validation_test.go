package automations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Reject malformed direct Service input before even the definition pre-read.
func TestReceiveDeviceFactValidationPrecedesRepositoryReads(t *testing.T) {
	t.Parallel()
	repo := &observingAdmissionRepository{}
	service := automations.NewService(repo, newScriptedDevices(), runtimeTestDependencies())
	_, err := service.ReceiveDeviceFact(context.Background(), automations.DeviceFact{})
	if !errors.Is(err, automations.ErrInvalidDeviceFact) {
		t.Fatalf("error = %v, want ErrInvalidDeviceFact", err)
	}
	if calls := repo.listEnabledCalls.Load(); calls != 0 {
		t.Fatalf("definition reads = %d, want zero", calls)
	}
}
