package hearthd

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

type blockingRecoveryDelivery struct {
	calls chan devices.CommandDispatch
}

func (delivery *blockingRecoveryDelivery) Deliver(
	ctx context.Context,
	_ string,
	dispatch devices.CommandDispatch,
) (devices.CommandAcceptance, error) {
	delivery.calls <- dispatch
	<-ctx.Done()
	return devices.CommandAcceptance{}, ctx.Err()
}

func TestGracefulStopDefersInterruptionUntilNextStartup(t *testing.T) {
	path, commandID := leaveActiveCommand(t)
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	assertCommandStatus(t, database, commandID, "requested")
	if _, err := devices.New(context.Background(), database, nil); err != nil {
		t.Fatal(err)
	}
	assertCommandStatus(t, database, commandID, "interrupted")
}

func TestRunRecoversCommandsBeforeNATSConnectionFailure(t *testing.T) {
	path, commandID := leaveActiveCommand(t)
	address := unusedLoopbackAddress(t)
	err := Run(context.Background(), Config{
		HTTPAddr: unusedLoopbackAddress(t), NATSURL: "nats://" + address, SQLitePath: path,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("Run unexpectedly connected to absent NATS")
	}
	database, openErr := platformdb.Open(context.Background(), path)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer database.Close()
	assertCommandStatus(t, database, commandID, "interrupted")
}

func leaveActiveCommand(t *testing.T) (string, devices.CommandID) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database, err := platformdb.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := platformdb.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	service, err := devices.New(context.Background(), database, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery := &blockingRecoveryDelivery{calls: make(chan devices.CommandDispatch, 1)}
	runContext, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(runContext, delivery) }()
	binding, err := service.Register(context.Background(), "simulator", devices.Registration{
		BindingKey: "recovery-light",
		Device:     devices.DeviceDescriptor{Name: "Recovery light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{{
			Key: "power", ExternalID: "recovery.light", Name: "Power",
			TypeID:  devices.EntityTypePowerV1,
			Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executeDone := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(
			context.Background(), binding.Entities[0].EntityID,
			devices.OperationNameSet, devices.CommandParameters(`{"value":true}`),
		)
		executeDone <- err
	}()
	dispatch := <-delivery.calls
	stop()
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if err := <-executeDone; !errors.Is(err, devices.ErrServiceStopped) {
		t.Fatalf("command error = %v", err)
	}
	assertCommandStatus(t, database, dispatch.ID, "requested")
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path, dispatch.ID
}

func assertCommandStatus(t *testing.T, database *sql.DB, id devices.CommandID, want string) {
	t.Helper()
	var status string
	if err := database.QueryRow("SELECT status FROM commands WHERE id = ?", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("command %s status = %q, want %q", id, status, want)
	}
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
