package devices

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
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
	delivery.calls <- commandDeliveryCall{AdapterID: adapterID, Dispatch: dispatch}
	return delivery.deliver(ctx, adapterID, dispatch)
}

func openMigratedDeviceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := platformdb.Open(context.Background(), "file:devices-test-"+uuid.NewString()+"?mode=memory&cache=shared")
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
		context.Background(), database,
		slog.New(slog.NewTextHandler(io.Discard, nil)), controls,
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, database
}

func runDeviceTestService(t *testing.T, service *Service, delivery CommandDelivery) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, delivery) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run service: %v", err)
		}
	})
}

func testDelivery(deliver func(context.Context, string, CommandDispatch) (CommandAcceptance, error)) *inMemoryCommandDelivery {
	return &inMemoryCommandDelivery{calls: make(chan commandDeliveryCall, 16), deliver: deliver}
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

func controlsWithCatalog(catalog *typeCatalog) serviceControls {
	controls := productionServiceControls()
	controls.newCatalog = func() (*typeCatalog, error) { return catalog, nil }
	return controls
}
