package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type readRepository struct {
	device             DeviceAggregate
	deviceErr          error
	getDeviceParams    GetDeviceParams
	entity             EntityWithState
	entityErr          error
	entitiesPage       Page[EntityWithState]
	listEntitiesParams ListEntitiesParams
	command            CommandRecord
	commandPage        Page[CommandRecord]
	listCommandsParams ListEntityCommandsParams
	listAllParams      ListCommandsParams
	historyPage        Page[EntityStateHistoryEntry]
	historyParams      ListEntityStateHistoryParams
	historyErr         error
	snapshot           EntityStateSnapshot
	snapshotErr        error
	snapshotIDs        []EntityID
	getDeviceCalls     int
	getEntityCalls     int
	listEntityCalls    int
	listCommandsCalls  int
	historyCalls       int
	snapshotCalls      int
}

func newReadRepository() *readRepository {
	return &readRepository{}
}

func (*readRepository) ListDevices(context.Context, ListDevicesParams) (Page[Device], error) {
	panic("unexpected ListDevices call")
}

func (repository *readRepository) GetDevice(_ context.Context, params GetDeviceParams) (DeviceAggregate, error) {
	repository.getDeviceCalls++
	repository.getDeviceParams = params
	return repository.device, repository.deviceErr
}

func (repository *readRepository) GetEntity(
	context.Context,
	EntityID,
) (EntityWithState, error) {
	repository.getEntityCalls++
	return repository.entity, repository.entityErr
}

func (repository *readRepository) GetEntityStateSnapshot(
	_ context.Context,
	ids []EntityID,
) (EntityStateSnapshot, error) {
	repository.snapshotCalls++
	repository.snapshotIDs = append([]EntityID(nil), ids...)
	return repository.snapshot, repository.snapshotErr
}

func (repository *readRepository) ListEntities(
	_ context.Context,
	params ListEntitiesParams,
) (Page[EntityWithState], error) {
	repository.listEntityCalls++
	repository.listEntitiesParams = params
	return repository.entitiesPage, nil
}

func (repository *readRepository) GetCommand(context.Context, CommandID) (CommandRecord, error) {
	return repository.command, nil
}

func (repository *readRepository) ListEntityCommands(
	_ context.Context,
	params ListEntityCommandsParams,
) (Page[CommandRecord], error) {
	repository.listCommandsCalls++
	repository.listCommandsParams = params
	return repository.commandPage, nil
}

func (repository *readRepository) ListCommands(
	_ context.Context,
	params ListCommandsParams,
) (Page[CommandRecord], error) {
	repository.listCommandsCalls++
	repository.listAllParams = params
	return repository.commandPage, nil
}

func (repository *readRepository) ListEntityStateHistory(
	_ context.Context,
	params ListEntityStateHistoryParams,
) (Page[EntityStateHistoryEntry], error) {
	repository.historyCalls++
	repository.historyParams = params
	return repository.historyPage, repository.historyErr
}

func TestReadServiceValidatesPagesBeforeRepositoryCalls(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	service := newTestService(repository, nil, nil, Dependencies{})
	invalidDeviceID := DeviceID("bad")
	invalidEntityID := EntityID("bad")
	invalidCommandID := CommandID("bad")

	for _, test := range []struct {
		name string
		call func() error
	}{
		{"device limit", func() error {
			_, err := service.ListDevices(context.Background(), ListDevicesParams{Limit: 0})
			return err
		}},
		{"device position", func() error {
			_, err := service.ListDevices(context.Background(), ListDevicesParams{AfterID: &invalidDeviceID, Limit: 1})
			return err
		}},
		{"device entity limit", func() error {
			_, err := service.GetDevice(context.Background(), GetDeviceParams{ID: commandTestDeviceID})
			return err
		}},
		{"device entity position", func() error {
			_, err := service.GetDevice(context.Background(), GetDeviceParams{
				ID: commandTestDeviceID, AfterEntityID: &invalidEntityID, EntityLimit: 1,
			})
			return err
		}},
		{"entity filter", func() error {
			_, err := service.ListEntities(context.Background(), ListEntitiesParams{DeviceID: &invalidDeviceID, Limit: 1})
			return err
		}},
		{"entity position", func() error {
			_, err := service.ListEntities(context.Background(), ListEntitiesParams{AfterID: &invalidEntityID, Limit: 1})
			return err
		}},
		{"partial command position", func() error {
			now := time.Now()
			_, err := service.ListEntityCommands(context.Background(), ListEntityCommandsParams{EntityID: commandTestEntityID, BeforeRequestedAt: &now, Limit: 1})
			return err
		}},
		{"partial household command position", func() error {
			now := time.Now()
			_, err := service.ListCommands(context.Background(), ListCommandsParams{BeforeRequestedAt: &now, Limit: 1})
			return err
		}},
		{"household command entity filter", func() error {
			_, err := service.ListCommands(context.Background(), ListCommandsParams{EntityID: &invalidEntityID, Limit: 1})
			return err
		}},
		{"household command status filter", func() error {
			status := CommandStatus("exploded")
			_, err := service.ListCommands(context.Background(), ListCommandsParams{Status: &status, Limit: 1})
			return err
		}},
		{"household command limit", func() error {
			_, err := service.ListCommands(context.Background(), ListCommandsParams{Limit: 0})
			return err
		}},
		{"invalid command position", func() error {
			now := time.Now()
			_, err := service.ListEntityCommands(context.Background(), ListEntityCommandsParams{EntityID: commandTestEntityID, BeforeRequestedAt: &now, BeforeID: &invalidCommandID, Limit: 1})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.call(); !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("error = %v, want ErrInvalidPage", err)
			}
		})
	}
	if repository.getDeviceCalls != 0 || repository.listEntityCalls != 0 || repository.listCommandsCalls != 0 {
		t.Fatalf(
			"repository calls = device %d, entities %d, commands %d",
			repository.getDeviceCalls, repository.listEntityCalls, repository.listCommandsCalls,
		)
	}
}

func TestReadServiceReturnsOwnedDataAndNormalizesCommandPosition(t *testing.T) {
	t.Parallel()
	sourceUpdatedAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	completedAt := sourceUpdatedAt.Add(time.Second)
	failure := CommandFailureOutcomeTimeout
	repository := newReadRepository()
	repository.entity = EntityWithState{
		Entity: Entity{ID: commandTestEntityID, DeviceID: commandTestDeviceID, Support: EntitySupport(`{"state":{}}`)},
		State:  &State{EntityID: commandTestEntityID, Value: Value(`true`), SourceUpdatedAt: &sourceUpdatedAt},
	}
	repository.device = DeviceAggregate{
		Device:   Device{ID: commandTestDeviceID, Kind: DeviceKindLight, Name: "Office"},
		Entities: Page[EntityWithState]{Items: []EntityWithState{repository.entity}, HasMore: true},
	}
	repository.entitiesPage = Page[EntityWithState]{Items: []EntityWithState{repository.entity}, HasMore: true}
	repository.command = CommandRecord{
		ID: commandTestID, EntityID: commandTestEntityID, Parameters: CommandParameters(`{"value":true}`),
		CompletedAt: &completedAt, FailureCode: &failure,
	}
	repository.commandPage = Page[CommandRecord]{Items: []CommandRecord{repository.command}}
	service := newTestService(repository, nil, nil, Dependencies{})

	device, err := service.GetDevice(context.Background(), GetDeviceParams{
		ID: commandTestDeviceID, AfterEntityID: new(commandTestEntityID), EntityLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.getDeviceParams.ID != commandTestDeviceID || repository.getDeviceParams.EntityLimit != 1 ||
		repository.getDeviceParams.AfterEntityID == nil || *repository.getDeviceParams.AfterEntityID != commandTestEntityID ||
		!device.Entities.HasMore {
		t.Fatalf("device params/result = %#v, %#v", repository.getDeviceParams, device)
	}
	device.Entities.Items[0].Entity.Support[0] = 'x'
	if string(repository.entity.Entity.Support) != `{"state":{}}` {
		t.Fatal("GetDevice result aliases repository data")
	}

	entities, err := service.ListEntities(context.Background(), ListEntitiesParams{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	entities.Items[0].Entity.Support[0] = 'x'
	entities.Items[0].State.Value[0] = 'f'
	*entities.Items[0].State.SourceUpdatedAt = time.Time{}
	if string(repository.entity.Entity.Support) != `{"state":{}}` || string(repository.entity.State.Value) != "true" ||
		repository.entity.State.SourceUpdatedAt.IsZero() {
		t.Fatal("ListEntities result aliases repository data")
	}

	position := time.Date(2026, 8, 25, 5, 0, 0, 0, time.FixedZone("offset", -5*60*60))
	commands, err := service.ListEntityCommands(context.Background(), ListEntityCommandsParams{
		EntityID: commandTestEntityID, BeforeRequestedAt: &position, BeforeID: new(commandTestID), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.getEntityCalls != 1 || repository.listCommandsParams.BeforeRequestedAt.Location() != time.UTC ||
		!repository.listCommandsParams.BeforeRequestedAt.Equal(position) {
		t.Fatalf("history params = %#v", repository.listCommandsParams)
	}
	commands.Items[0].Parameters[0] = 'x'
	*commands.Items[0].CompletedAt = time.Time{}
	*commands.Items[0].FailureCode = CommandFailureInternalError
	if string(repository.command.Parameters) != `{"value":true}` || repository.command.CompletedAt.IsZero() ||
		*repository.command.FailureCode != failure {
		t.Fatal("ListEntityCommands result aliases repository data")
	}
}

func TestListCommandsSkipsParentCheckAndReturnsOwnedData(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, 8, 25, 10, 0, 1, 0, time.UTC)
	failure := CommandFailureOutcomeTimeout
	repository := newReadRepository()
	repository.command = CommandRecord{
		ID: commandTestID, EntityID: commandTestEntityID, Parameters: CommandParameters(`{"value":true}`),
		CompletedAt: &completedAt, FailureCode: &failure,
	}
	repository.commandPage = Page[CommandRecord]{Items: []CommandRecord{repository.command}}
	service := newTestService(repository, nil, nil, Dependencies{})

	position := time.Date(2026, 8, 25, 5, 0, 0, 0, time.FixedZone("offset", -5*60*60))
	status := CommandStatusSatisfied
	commands, err := service.ListCommands(context.Background(), ListCommandsParams{
		EntityID: new(commandTestEntityID), Status: &status,
		BeforeRequestedAt: &position, BeforeID: new(commandTestID), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Household history never resolves the Entity filter: unknown Entities
	// match nothing instead of failing, so no parent lookup happens.
	if repository.getEntityCalls != 0 || repository.listAllParams.EntityID == nil ||
		*repository.listAllParams.EntityID != commandTestEntityID ||
		repository.listAllParams.Status == nil || *repository.listAllParams.Status != status ||
		repository.listAllParams.BeforeRequestedAt.Location() != time.UTC ||
		!repository.listAllParams.BeforeRequestedAt.Equal(position) {
		t.Fatalf(
			"household history params = %#v, entity calls = %d",
			repository.listAllParams,
			repository.getEntityCalls,
		)
	}
	commands.Items[0].Parameters[0] = 'x'
	*commands.Items[0].CompletedAt = time.Time{}
	*commands.Items[0].FailureCode = CommandFailureInternalError
	if string(repository.command.Parameters) != `{"value":true}` || repository.command.CompletedAt.IsZero() ||
		*repository.command.FailureCode != failure {
		t.Fatal("ListCommands result aliases repository data")
	}
}

func TestEntityReadsPreservePersistedAvailabilityDuringReadinessPause(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	repository := newReadRepository()
	repository.entity = EntityWithState{
		Entity: Entity{ID: commandTestEntityID, DeviceID: commandTestDeviceID},
		State:  &State{EntityID: commandTestEntityID, Value: Value(`true`)},
		Availability: EntityAvailability{
			Status: EntityAvailabilityAvailable, Source: "entity_report",
			Since: now.Add(-time.Minute), EvidenceAt: now.Add(-time.Minute),
		},
	}
	service := newTestService(repository, nil, nil, Dependencies{Now: func() time.Time { return now }})

	view, err := service.GetEntity(context.Background(), commandTestEntityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || string(view.State.Value) != "true" ||
		view.Availability.Status != EntityAvailabilityAvailable || view.Availability.Source != "entity_report" {
		t.Fatalf("paused Entity view = %#v", view)
	}
}

func TestListEntityCommandsDistinguishesUnknownParent(t *testing.T) {
	t.Parallel()
	repository := newReadRepository()
	repository.entityErr = ErrEntityNotFound
	service := newTestService(repository, nil, nil, Dependencies{})
	_, err := service.ListEntityCommands(
		context.Background(),
		ListEntityCommandsParams{EntityID: commandTestEntityID, Limit: 50},
	)
	if !errors.Is(err, ErrEntityNotFound) || repository.listCommandsCalls != 0 {
		t.Fatalf("error = %v, list calls = %d", err, repository.listCommandsCalls)
	}
}
