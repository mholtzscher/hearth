package devices //nolint:testpackage // Independent service boundaries retain their scoped input/error contracts.

import (
	"context"
	"errors"
	"testing"
)

func TestServiceReadIdentityAndOperationalContracts(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"device", "entity", "command", "enablement"} {
		for _, mode := range []string{"missing", "storage", "canceled"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				t.Parallel()
				cause := inputContractFailure(operation, mode)
				repo := &inputContractRepository{cause: cause}
				service := newTestService(repo, nil, nil, Dependencies{})
				if err := callInputContract(service, operation, false); err == nil {
					t.Fatal("malformed identity accepted")
				}
				if repo.calls != 0 {
					t.Fatal("malformed identity reached storage")
				}
				if err := callInputContract(service, operation, true); !errors.Is(err, cause) {
					t.Fatalf("missing/operational identity lost: %v", err)
				}
				if repo.calls != 1 {
					t.Fatalf("storage calls=%d", repo.calls)
				}
			})
		}
	}
}

func inputContractFailure(operation, mode string) error {
	if mode == "storage" {
		return errors.New("storage fault")
	}
	if mode == "canceled" {
		return context.Canceled
	}
	switch operation {
	case "device":
		return ErrDeviceNotFound
	case "command":
		return ErrCommandNotFound
	default:
		return ErrEntityNotFound
	}
}

func callInputContract(service *Service, operation string, valid bool) error {
	id := "bad"
	if valid {
		id = "01890f47-7a6b-7c4d-8e9f-0123456789ab"
	}
	var err error
	switch operation {
	case "device":
		_, err = service.GetDevice(context.Background(), GetDeviceParams{ID: DeviceID("dev_" + id), EntityLimit: 1})
	case "entity":
		_, err = service.GetEntity(context.Background(), EntityID("ent_"+id))
	case "command":
		_, err = service.GetCommand(context.Background(), CommandID("cmd_"+id))
	case "enablement":
		_, err = service.SetEntityEnabled(context.Background(), EntityID("ent_"+id), true)
	}
	return err
}

type inputContractRepository struct {
	ReadRepository

	cause error
	calls int
}

func (repo *inputContractRepository) GetDevice(context.Context, GetDeviceParams) (DeviceAggregate, error) {
	repo.calls++
	return DeviceAggregate{}, repo.cause
}
func (repo *inputContractRepository) GetEntity(context.Context, EntityID) (EntityWithState, error) {
	repo.calls++
	return EntityWithState{}, repo.cause
}
func (repo *inputContractRepository) GetCommand(context.Context, CommandID) (CommandRecord, error) {
	repo.calls++
	return CommandRecord{}, repo.cause
}

func (repo *inputContractRepository) SetEntityEnabled(
	context.Context,
	SetEntityEnabledParams,
) (EntityWithState, error) {
	repo.calls++
	return EntityWithState{}, repo.cause
}
