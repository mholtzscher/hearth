package devices

import (
	"context"
	"errors"
	"testing"
	"time"
)

type readRepository struct {
	*stubRegistrationRepository
	entity             EntityWithState
	entityErr          error
	entitiesPage       Page[EntityWithState]
	listEntitiesParams ListEntitiesParams
	command            CommandRecord
	commandPage        Page[CommandRecord]
	listCommandsParams ListEntityCommandsParams
	getEntityCalls     int
	listEntityCalls    int
	listCommandsCalls  int
}

func newReadRepository() *readRepository {
	return &readRepository{stubRegistrationRepository: &stubRegistrationRepository{}}
}

func (repository *readRepository) GetEntity(context.Context, EntityID) (EntityWithState, error) {
	repository.getEntityCalls++
	return repository.entity, repository.entityErr
}

func (repository *readRepository) ListEntities(_ context.Context, params ListEntitiesParams) (Page[EntityWithState], error) {
	repository.listEntityCalls++
	repository.listEntitiesParams = params
	return repository.entitiesPage, nil
}

func (repository *readRepository) GetCommand(context.Context, CommandID) (CommandRecord, error) {
	return repository.command, nil
}

func (repository *readRepository) ListEntityCommands(_ context.Context, params ListEntityCommandsParams) (Page[CommandRecord], error) {
	repository.listCommandsCalls++
	repository.listCommandsParams = params
	return repository.commandPage, nil
}

func TestReadServiceValidatesPagesBeforeRepositoryCalls(t *testing.T) {
	repository := newReadRepository()
	service := NewService(repository, nil, nil, Dependencies{})
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
		{"invalid command position", func() error {
			now := time.Now()
			_, err := service.ListEntityCommands(context.Background(), ListEntityCommandsParams{EntityID: commandTestEntityID, BeforeRequestedAt: &now, BeforeID: &invalidCommandID, Limit: 1})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrInvalidPage) {
				t.Fatalf("error = %v, want ErrInvalidPage", err)
			}
		})
	}
	if repository.listEntityCalls != 0 || repository.listCommandsCalls != 0 {
		t.Fatalf("repository list calls = entities %d, commands %d", repository.listEntityCalls, repository.listCommandsCalls)
	}
}

func TestReadServiceReturnsOwnedDataAndNormalizesCommandPosition(t *testing.T) {
	sourceUpdatedAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	completedAt := sourceUpdatedAt.Add(time.Second)
	failure := CommandFailureOutcomeTimeout
	repository := newReadRepository()
	repository.entity = EntityWithState{
		Entity: Entity{ID: commandTestEntityID, DeviceID: commandTestDeviceID, Support: EntitySupport(`{"state":{}}`)},
		State:  &State{EntityID: commandTestEntityID, Value: Value(`true`), SourceUpdatedAt: &sourceUpdatedAt},
	}
	repository.entitiesPage = Page[EntityWithState]{Items: []EntityWithState{repository.entity}, HasMore: true}
	repository.command = CommandRecord{
		ID: commandTestID, EntityID: commandTestEntityID, Parameters: CommandParameters(`{"value":true}`),
		CompletedAt: &completedAt, FailureCode: &failure,
	}
	repository.commandPage = Page[CommandRecord]{Items: []CommandRecord{repository.command}}
	service := NewService(repository, nil, nil, Dependencies{})

	entities, err := service.ListEntities(context.Background(), ListEntitiesParams{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	entities.Items[0].Entity.Support[0] = 'x'
	entities.Items[0].State.Value[0] = 'f'
	*entities.Items[0].State.SourceUpdatedAt = time.Time{}
	if string(repository.entity.Entity.Support) != `{"state":{}}` || string(repository.entity.State.Value) != "true" || repository.entity.State.SourceUpdatedAt.IsZero() {
		t.Fatal("ListEntities result aliases repository data")
	}

	position := time.Date(2026, 8, 25, 5, 0, 0, 0, time.FixedZone("offset", -5*60*60))
	commands, err := service.ListEntityCommands(context.Background(), ListEntityCommandsParams{
		EntityID: commandTestEntityID, BeforeRequestedAt: &position, BeforeID: pointerTo(commandTestID), Limit: 1,
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
	if string(repository.command.Parameters) != `{"value":true}` || repository.command.CompletedAt.IsZero() || *repository.command.FailureCode != failure {
		t.Fatal("ListEntityCommands result aliases repository data")
	}
}

func TestListEntityCommandsDistinguishesUnknownParent(t *testing.T) {
	repository := newReadRepository()
	repository.entityErr = ErrEntityNotFound
	service := NewService(repository, nil, nil, Dependencies{})
	_, err := service.ListEntityCommands(context.Background(), ListEntityCommandsParams{EntityID: commandTestEntityID, Limit: 50})
	if !errors.Is(err, ErrEntityNotFound) || repository.listCommandsCalls != 0 {
		t.Fatalf("error = %v, list calls = %d", err, repository.listCommandsCalls)
	}
}

func pointerTo[T any](value T) *T { return &value }
