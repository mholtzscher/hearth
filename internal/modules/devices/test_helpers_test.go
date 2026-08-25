package devices

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type commandDeliveryCall struct {
	AdapterID string
	Dispatch  CommandDispatch
}

type inMemoryCommandDelivery struct {
	calls   chan commandDeliveryCall
	deliver func(context.Context, string, CommandDispatch) (CommandAcceptance, error)
}

func (delivery *inMemoryCommandDelivery) Deliver(
	ctx context.Context,
	adapterID string,
	dispatch CommandDispatch,
) (CommandAcceptance, error) {
	if delivery.calls != nil {
		delivery.calls <- commandDeliveryCall{AdapterID: adapterID, Dispatch: dispatch}
	}
	return delivery.deliver(ctx, adapterID, dispatch)
}

type commandDeliveryFunc func(context.Context, string, CommandDispatch) (CommandAcceptance, error)

func (deliver commandDeliveryFunc) Deliver(ctx context.Context, adapterID string, dispatch CommandDispatch) (CommandAcceptance, error) {
	return deliver(ctx, adapterID, dispatch)
}

func openMigratedDeviceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := "file:devices-test-" + uuid.NewString() + "?mode=memory&cache=shared"
	return openMigratedDeviceFileDB(t, path)
}

func openMigratedDeviceFileDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := platformdb.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

func newDeviceTestService(t *testing.T, controls serviceControls) (*Service, *sql.DB) {
	t.Helper()
	database := openMigratedDeviceTestDB(t)
	service, err := newService(
		context.Background(), database, slog.New(slog.NewTextHandler(io.Discard, nil)), controls,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, database
}

type runningTestService struct {
	cancel context.CancelFunc
	errors chan error
	once   sync.Once
}

func (running *runningTestService) stop(t *testing.T) {
	t.Helper()
	running.once.Do(func() {
		running.cancel()
		if err := <-running.errors; err != nil {
			t.Errorf("run Device / Entity module: %v", err)
		}
	})
}

func runDeviceTestService(t *testing.T, service *Service, delivery CommandDelivery) *runningTestService {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	running := &runningTestService{cancel: cancel, errors: make(chan error, 1)}
	go func() { running.errors <- service.Run(ctx, delivery) }()
	t.Cleanup(func() { running.stop(t) })
	return running
}

func newRunningDeviceTestService(
	t *testing.T,
	controls serviceControls,
	delivery CommandDelivery,
) (*Service, *sql.DB) {
	t.Helper()
	service, database := newDeviceTestService(t, controls)
	runDeviceTestService(t, service, delivery)
	return service, database
}

func acceptingTestDelivery() CommandDelivery {
	return commandDeliveryFunc(func(context.Context, string, CommandDispatch) (CommandAcceptance, error) {
		return CommandAcceptance{Accepted: true}, nil
	})
}

func firstLightCatalog(t *testing.T) *typeCatalog {
	t.Helper()
	catalog, err := newBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func validDomainRegistration() Registration {
	externalID := "ha-device"
	return Registration{
		BindingKey: "office-light",
		Device:     DeviceDescriptor{ExternalID: &externalID, Name: "Office light", Kind: DeviceKindLight},
		Entities: []EntityDescriptor{{
			Key: "power", ExternalID: "light.office", Name: "Power", TypeID: EntityTypePowerV1,
			Support: EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}},
	}
}

func temporaryDeviceDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "hearth.db")
}
