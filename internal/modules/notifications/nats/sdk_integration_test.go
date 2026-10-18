package nats_test

import (
	"context"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationnats "github.com/mholtzscher/hearth/internal/modules/notifications/nats"
	notificationsqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type sdkFixture struct {
	service    *notifications.Service
	session    *adapter.Session
	broker     *server.Server
	connection *natsgo.Conn
	validator  *contractsv1.Validator
}

func sdkIntegrationFixture(t *testing.T) sdkFixture {
	t.Helper()
	broker, connection, validator := transportBrokerFixture(t)
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "core.db"))
	runtimeRepo := adapterssqlite.NewAdapterRepository(database)
	runtimes := adapters.NewService(adapterssqlite.AdapterStores(runtimeRepo), adapters.Dependencies{})
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	service, err := notifications.NewService(
		context.Background(),
		notificationsqlite.NewRepository(database),
		notificationnats.NewSender(connection, validator),
	)
	if err != nil {
		t.Fatal(err)
	}
	registrations, err := notificationnats.StartRegistrationServer(connection, validator, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := adapter.Connect(
		ctx,
		adapter.Config{
			AdapterID:       "alerts",
			SoftwareName:    "alerts",
			SoftwareVersion: "1",
			NATSURL:         connection.ConnectedUrl(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return sdkFixture{service: service, session: session, broker: broker, connection: connection, validator: validator}
}

// A3: the client's reconnect buffer can deliver an expired request, but never
// permits provider execution or a second dispatch of that attempt.
//
//nolint:gocognit // The outage scenario keeps all observable reconnect evidence together.
func TestReconnectBufferedExpiredSendCannotInvokeSDKProvider(t *testing.T) {
	t.Parallel()
	f := sdkIntegrationFixture(t)
	var calls atomic.Int32
	notificationServer, err := f.session.StartNotificationServer(
		context.Background(),
		func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
			calls.Add(1)
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = notificationServer.Close(context.Background()) })
	id, err := f.session.RegisterNotificationDestination(context.Background(), "home", "Home")
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.service.Send(context.Background(), sendInput(t, id))
	if err != nil || first.Outcome != notifications.Accepted {
		t.Fatalf("initial send: %+v %v", first, err)
	}
	in := sendInput(t, id)
	expired := first
	expired.ID = in.AttemptID
	subject, err := natswire.NotificationSendSubject(first.Route.AdapterID, first.Route.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan struct{}, 1)
	_, err = f.connection.Subscribe(subject, func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[sendRequest](
			f.validator,
			contractsv1.NotificationSendRequestSchemaID,
			message.Data,
		)
		if decodeErr != nil {
			t.Error(decodeErr)
			return
		}
		if request.Data.AttemptID == string(expired.ID) {
			select {
			case observed <- struct{}{}:
			default:
				t.Error("expired attempt published twice")
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.connection.Flush(); err != nil {
		t.Fatal(err)
	}
	reconnecting := f.connection.StatusChanged(natsgo.RECONNECTING)
	port := f.broker.Addr().(*net.TCPAddr).Port
	f.broker.Shutdown()
	f.broker.WaitForShutdown()
	select {
	case <-reconnecting:
	case <-time.After(5 * time.Second):
		t.Fatal("Core did not disconnect")
	}
	expired.Deadline = time.Now().Add(100 * time.Millisecond)
	e := notificationnats.NewSender(f.connection, f.validator).Send(context.Background(), expired)
	if e.Outcome != notifications.Unknown || e.ReasonCode != "outcome_timeout" {
		t.Fatalf("buffered send evidence: %+v", e)
	}
	replacement, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: port, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go replacement.Start()
	if !replacement.ReadyForConnections(5 * time.Second) {
		t.Fatal("replacement broker not ready")
	}
	t.Cleanup(func() {
		_ = notificationServer.Close(context.Background())
		_ = f.session.Close()
		replacement.Shutdown()
		replacement.WaitForShutdown()
	})
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect buffer did not deliver expired request")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err = f.session.RegisterNotificationDestination(ctx, "home", "Home"); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.service.Send(ctx, sendInput(t, id))
	if err != nil || fresh.Outcome != notifications.Accepted || calls.Load() != 2 {
		t.Fatalf("expired request invoked provider: %+v %v calls=%d", fresh, err, calls.Load())
	}
}
func sendInput(t *testing.T, id string) notifications.SendInput {
	t.Helper()
	attemptID, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return notifications.SendInput{
		AttemptID:     attemptID,
		DestinationID: notifications.DestinationID(id),
		Message:       notifications.Message{Title: "Freezer", Text: "Too warm"},
	}
}

// A6's no-responders window is exercised across actual SDK subscription cleanup,
// active runtime persistence, Core NATS sending, and the durable service outcome.
func TestSDKSendAndSubscriptionCloseBeforeRuntimeRelease(t *testing.T) {
	t.Parallel()
	f := sdkIntegrationFixture(t)
	var calls atomic.Int32
	server, err := f.session.StartNotificationServer(
		context.Background(),
		func(_ context.Context, r adapter.NotificationRequest) adapter.NotificationOutcome {
			calls.Add(1)
			if r.LocalKey != "home" || r.Message.Title != "Freezer" || r.Message.Text != "Too warm" {
				t.Errorf("SDK request: %+v", r)
			}
			return adapter.NotificationOutcome{Outcome: "accepted", Receipt: "external-receipt"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.session.RegisterNotificationDestination(context.Background(), "home", "Home")
	if err != nil {
		t.Fatal(err)
	}
	// Public shutdown does not fence an already reserved internal Step send.
	f.service.ClosePublicAdmission()
	a, err := f.service.Send(context.Background(), sendInput(t, id))
	if err != nil || a.Outcome != notifications.Accepted || a.Receipt != "external-receipt" {
		t.Fatalf("SDK evidence: %+v %v", a, err)
	}
	if err = server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, err := f.service.GetDestination(context.Background(), notifications.DestinationID(id))
	if err != nil || !d.Available {
		t.Fatalf("runtime prematurely released: %+v %v", d, err)
	}
	unavailable, err := f.service.Send(context.Background(), sendInput(t, id))
	if err != nil || unavailable.Outcome != notifications.NotSent ||
		unavailable.ReasonCode != "destination_unavailable" ||
		calls.Load() != 1 {
		t.Fatalf("no responders: %+v %v calls=%d", unavailable, err, calls.Load())
	}
	saved, err := f.service.GetAttempt(context.Background(), unavailable.ID)
	if err != nil || saved.Outcome != notifications.NotSent {
		t.Fatalf("durable no responders: %+v %v", saved, err)
	}
	if err = f.session.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = f.service.GetDestination(context.Background(), notifications.DestinationID(id))
	if err != nil || d.Available {
		t.Fatalf("released runtime still available: %+v %v", d, err)
	}
	if err = f.service.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}
