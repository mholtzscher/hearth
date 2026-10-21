//go:build integration

//nolint:testpackage // Verifies the app's private readiness and SDK lifecycle boundary.
package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationnats "github.com/mholtzscher/hearth/internal/modules/notifications/nats"
	notificationsqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type notificationRegistrarFunc func(context.Context, notifications.Registration) (notifications.Destination, error)

func (f notificationRegistrarFunc) Register(
	ctx context.Context,
	r notifications.Registration,
) (notifications.Destination, error) {
	return f(ctx, r)
}

type notificationAppFixture struct {
	url        string
	connection *natsgo.Conn
	validator  *contractsv1.Validator
	service    *notifications.Service
	runtimes   *adapters.Service
	sender     *notificationnats.Sender
}

func newNotificationAppFixture(t *testing.T) notificationAppFixture {
	t.Helper()
	broker := natstest.StartServer(t)
	connection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "core.db"))
	runtimeRepo := adapterssqlite.NewAdapterRepository(database)
	runtimes := adapters.NewService(adapterssqlite.AdapterStores(runtimeRepo), adapters.Dependencies{})
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Close(context.Background()) })
	sender := notificationnats.NewSender(connection, validator)
	service, err := notifications.NewService(t.Context(), notificationsqlite.NewRepository(database), sender)
	if err != nil {
		t.Fatal(err)
	}
	return notificationAppFixture{
		url:        broker.ClientURL(),
		connection: connection,
		validator:  validator,
		service:    service,
		runtimes:   runtimes,
		sender:     sender,
	}
}

func (f notificationAppFixture) connect(t *testing.T) *adapter.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), sessionFailureWait)
	defer cancel()
	session, err := adapter.Connect(
		ctx,
		adapter.Config{
			AdapterID:       "alerts",
			SoftwareName:    "simulator",
			SoftwareVersion: "1",
			NATSURL:         f.url,
			Logger:          slog.New(slog.DiscardHandler),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func (f notificationAppFixture) registerServer(t *testing.T, registrar notificationnats.Registrar) {
	t.Helper()
	server, err := notificationnats.StartRegistrationServer(f.connection, f.validator, registrar, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
}

// The real SDK allows dispatch while registration is in flight. This protects
// canonical capture identity, subscription-before-registration, and readiness.
//
//nolint:gocognit,gocyclo,cyclop // Keeps the real SDK registration race, capture and shutdown evidence in one scenario.
func TestNotificationRegistrationReadinessAndCanonicalIdentity(t *testing.T) {
	t.Parallel()
	f := newNotificationAppFixture(t)
	probed := make(chan notifications.Evidence, 1)
	allowReply := make(chan struct{})
	f.registerServer(
		t,
		notificationRegistrarFunc(
			func(ctx context.Context, r notifications.Registration) (notifications.Destination, error) {
				destination, err := f.service.Register(ctx, r)
				if err != nil {
					return destination, err
				}
				id, err := notifications.NewAttemptID()
				if err != nil {
					return destination, err
				}
				probed <- f.sender.Send(ctx, notifications.Attempt{ID: id, DestinationID: destination.ID, Route: notifications.RouteSnapshot{AdapterID: r.AdapterID, RuntimeID: r.RuntimeID, LocalKey: r.Key}, Message: notifications.Message{Text: "before reply"}, Deadline: time.Now().Add(time.Second)})
				select {
				case <-allowReply:
				case <-ctx.Done():
					return notifications.Destination{}, ctx.Err()
				}
				return destination, nil
			},
		),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	ready := make(chan *scripted.NotificationRuntime, 1)
	done := make(chan error, 1)
	entry := ScriptedAdapterConfig{
		AdapterID:     "alerts",
		Notifications: []scripted.NotificationSpec{{Key: "home", Name: "Home", Hold: true}},
	}
	session := f.connect(t)
	go func() {
		done <- runScriptedSessionReady(runCtx, entry, session, slog.New(slog.DiscardHandler), "", func(_ *scripted.Runtime, runtime *scripted.NotificationRuntime, _ *scripted.FunctionRuntime) {
			ready <- runtime
		})
	}()
	select {
	case evidence := <-probed:
		if evidence.Outcome != notifications.NotSent || evidence.ReasonCode != "adapter_busy" {
			t.Fatalf("in-flight registration: %+v", evidence)
		}
	case <-ctx.Done():
		t.Fatal("registration never dispatched through active SDK server")
	}
	select {
	case <-ready:
		t.Fatal("ready before registration reply")
	default:
	}
	close(allowReply)
	var runtime *scripted.NotificationRuntime
	select {
	case runtime = <-ready:
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-ctx.Done():
		t.Fatal("never ready")
	}
	page, err := runtime.Requests(0, 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("unmapped work captured: %+v %v", page, err)
	}
	if page.Metadata != (scripted.NotificationCaptureMetadata{
		AdapterID: "alerts", LatestSequence: 1, HighestDroppedSequence: 1, DroppedCount: 1, Truncated: true,
	}) {
		t.Fatalf("pre-Attach SDK receipt hidden from loss metadata: %+v", page.Metadata)
	}
	destination := runtime.Destinations()[0]
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan notifications.Attempt, 1)
	go func() {
		attempt, sendErr := f.service.Send(
			ctx,
			notifications.SendInput{
				AttemptID:     id,
				DestinationID: notifications.DestinationID(destination.DestinationID),
				Message:       notifications.Message{Title: "Title", Text: "exact text"},
			},
		)
		if sendErr != nil {
			t.Error(sendErr)
		}
		sent <- attempt
	}()
	for {
		page, err = runtime.Requests(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("send not captured")
		case <-time.After(time.Millisecond):
		}
	}
	if page.Items[0].AttemptID != string(id) || page.Items[0].DestinationID != destination.DestinationID ||
		page.Items[0].Message.Text != "exact text" || page.Items[0].Sequence != 2 ||
		page.Metadata.LatestSequence != 2 || page.Metadata.HighestDroppedSequence != 1 ||
		page.Metadata.DroppedCount != 1 || !page.Metadata.Truncated {
		t.Fatalf("capture: %+v", page.Items)
	}
	stop()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cancellation failed to join held SDK handler")
	}
	page, err = runtime.Requests(0, 100)
	if err != nil || page.Items[0].FinishedAt == nil {
		t.Fatalf("handler not joined before return: %+v %v", page, err)
	}
	select {
	case <-sent:
	case <-ctx.Done():
		t.Fatal("Core send remained blocked")
	}
	// Core persists key identity across new Sessions, not just runtime snapshots.
	next := f.connect(t)
	server, err := next.StartNotificationServer(
		ctx,
		func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	stable, err := next.RegisterNotificationDestination(ctx, "home", "Home")
	if err != nil || stable != destination.DestinationID {
		t.Fatalf("restart ID: %s %v", stable, err)
	}
}

func TestNotificationRegistrationFailureNeverReady(t *testing.T) {
	t.Parallel()
	f := newNotificationAppFixture(t)
	f.registerServer(
		t,
		notificationRegistrarFunc(func(context.Context, notifications.Registration) (notifications.Destination, error) {
			return notifications.Destination{}, notifications.ErrInvalidInput
		}),
	)
	ctx, cancel := context.WithTimeout(t.Context(), sessionFailureWait)
	defer cancel()
	called := false
	err := runScriptedSessionReady(
		ctx,
		ScriptedAdapterConfig{
			AdapterID:     "alerts",
			Notifications: []scripted.NotificationSpec{{Key: "home", Name: "Home"}},
		},
		f.connect(t),
		slog.New(slog.DiscardHandler),
		"",
		func(*scripted.Runtime, *scripted.NotificationRuntime, *scripted.FunctionRuntime) { called = true },
	)
	var rejected *adapter.NotificationRegistrationRejectedError
	if !errors.As(err, &rejected) || called {
		t.Fatalf("failure readiness: %v ready=%v", err, called)
	}
}

func TestNotificationServerStartFailureNeverReady(t *testing.T) {
	t.Parallel()
	f := newNotificationAppFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), sessionFailureWait)
	defer cancel()
	session := f.connect(t)
	server, err := session.StartNotificationServer(
		ctx,
		func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = runScriptedSessionReady(
		ctx,
		ScriptedAdapterConfig{
			AdapterID:     "alerts",
			Notifications: []scripted.NotificationSpec{{Key: "home", Name: "Home"}},
		},
		session,
		slog.New(slog.DiscardHandler),
		"",
		func(*scripted.Runtime, *scripted.NotificationRuntime, *scripted.FunctionRuntime) { called = true },
	)
	var invalid *adapter.ValidationError
	if !errors.As(err, &invalid) || called {
		t.Fatalf("startup failure: %v ready=%v", err, called)
	}
	if err = server.Wait(ctx); !errors.Is(err, adapter.ErrClosed) {
		t.Fatalf("existing server not joined: %v", err)
	}
}

type supervisedNotificationSession struct {
	*adapter.Session

	server            *adapter.NotificationServer
	commandsEntered   chan struct{}
	commandFailure    chan struct{}
	commandsJoined    chan struct{}
	runtime           *scripted.NotificationRuntime
	joinedBeforeClose bool
}

func (s *supervisedNotificationSession) Close() error {
	s.joinedBeforeClose = true
	select {
	case <-s.commandsJoined:
	default:
		s.joinedBeforeClose = false
	}
	if s.runtime != nil {
		page, err := s.runtime.Requests(0, 1)
		if err != nil || (len(page.Items) > 0 && page.Items[0].FinishedAt == nil) {
			s.joinedBeforeClose = false
		}
	}
	return s.Session.Close()
}

func (s *supervisedNotificationSession) StartNotificationServer(
	ctx context.Context,
	handler adapter.NotificationHandler,
) (*adapter.NotificationServer, error) {
	server, err := s.Session.StartNotificationServer(ctx, handler)
	s.server = server
	return server, err
}

func (s *supervisedNotificationSession) ServeCommands(ctx context.Context, _ adapter.CommandHandler) error {
	close(s.commandsEntered)
	defer close(s.commandsJoined)
	select {
	case <-s.commandFailure:
		return adapter.ErrRuntimeFenced
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Both serving owners must stop their siblings without cancelling the caller.
//
//nolint:gocognit,nestif // Each failure direction synchronizes and verifies actual SDK handler joins.
func TestNotificationAndCommandServingFailuresJoinSiblings(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"notification", "command"} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			f := newNotificationAppFixture(t)
			f.registerServer(t, f.service)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			session := &supervisedNotificationSession{
				Session:         f.connect(t),
				commandsEntered: make(chan struct{}),
				commandFailure:  make(chan struct{}),
				commandsJoined:  make(chan struct{}),
			}
			ready := make(chan *scripted.NotificationRuntime, 1)
			done := make(chan error, 1)
			go func() {
				done <- runScriptedSessionReady(ctx, ScriptedAdapterConfig{AdapterID: "alerts", Notifications: []scripted.NotificationSpec{{Key: "home", Name: "Home", Hold: true}}}, session, slog.New(slog.DiscardHandler), "", func(_ *scripted.Runtime, runtime *scripted.NotificationRuntime, _ *scripted.FunctionRuntime) {
					session.runtime = runtime
					ready <- runtime
				})
			}()
			var runtime *scripted.NotificationRuntime
			select {
			case runtime = <-ready:
			case <-ctx.Done():
				t.Fatal("never ready")
			}
			select {
			case <-session.commandsEntered:
			case <-ctx.Done():
				t.Fatal("commands never started")
			}
			if source == "notification" {
				if err := session.server.Close(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				id, err := notifications.NewAttemptID()
				if err != nil {
					t.Fatal(err)
				}
				sent := make(chan struct{})
				go func() {
					_, _ = f.service.Send(
						ctx,
						notifications.SendInput{
							AttemptID:     id,
							DestinationID: notifications.DestinationID(runtime.Destinations()[0].DestinationID),
							Message:       notifications.Message{Text: "held"},
						},
					)
					close(sent)
				}()
				for {
					page, pageErr := runtime.Requests(0, 1)
					if pageErr != nil {
						t.Fatal(pageErr)
					}
					if len(page.Items) > 0 {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("held handler not admitted")
					case <-time.After(time.Millisecond):
					}
				}
				close(session.commandFailure)
				defer func() {
					select {
					case <-sent:
					case <-ctx.Done():
						t.Error("Core send not joined")
					}
				}()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("serving failure hidden")
				}
			case <-ctx.Done():
				t.Fatal("siblings not joined")
			}
			if ctx.Err() != nil {
				t.Fatal("caller cancelled")
			}
			if !session.joinedBeforeClose {
				t.Fatal("siblings were not joined before Session.Close")
			}
			select {
			case <-session.commandsJoined:
			default:
				t.Fatal("command loop not joined")
			}
			page, err := runtime.Requests(0, 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) > 0 && page.Items[0].FinishedAt == nil {
				t.Fatal("notification handler not joined")
			}
		})
	}
}

//nolint:gocognit // One real multi-Session scenario checks the shared readiness barrier and terminal-failure cleanup.
func TestMultipleNotificationAdaptersWaitForAllRegistrationsAndStopTogether(t *testing.T) {
	t.Parallel()
	f := newNotificationAppFixture(t)
	registered := make(chan string, 2)
	alertsRuntime := make(chan string, 1)
	allowBeta := make(chan struct{})
	f.registerServer(
		t,
		notificationRegistrarFunc(
			func(ctx context.Context, r notifications.Registration) (notifications.Destination, error) {
				destination, err := f.service.Register(ctx, r)
				if r.AdapterID == "alerts" {
					select {
					case alertsRuntime <- r.RuntimeID:
					default:
					}
				}
				select {
				case registered <- r.AdapterID:
				default:
				}
				if r.AdapterID == "beta" {
					select {
					case <-allowBeta:
					case <-ctx.Done():
						return notifications.Destination{}, ctx.Err()
					}
				}
				return destination, err
			},
		),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	addr := freeLoopbackAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{NATSURL: f.url, ControlAddr: addr, Adapters: []ScriptedAdapterConfig{
			{AdapterID: "alerts", Notifications: []scripted.NotificationSpec{{Key: "home", Name: "Home"}}},
			{AdapterID: "beta", Notifications: []scripted.NotificationSpec{{Key: "home", Name: "Home"}}},
		}}, slog.New(slog.DiscardHandler))
	}()
	for waiting := true; waiting; {
		select {
		case id := <-registered:
			waiting = id != "beta"
		case <-ctx.Done():
			t.Fatal("adapters did not register")
		}
	}
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("control exposed before all destination replies")
	}
	close(allowBeta)
	waitForControlListening(t, addr)
	client := &http.Client{Timeout: time.Second}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://"+addr+"/v1/sim/notifications/destinations",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var destinations []scripted.NotificationDestinationInfo
	err = json.NewDecoder(response.Body).Decode(&destinations)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || len(destinations) != 2 ||
		destinations[0].AdapterID != "alerts" ||
		destinations[1].AdapterID != "beta" {
		t.Fatalf("discovery: %+v %v", destinations, err)
	}
	// Revoking the current runtime fences alerts' real SDK Session; beta and the shared listener
	// must stop even though their caller's context remains live.
	select {
	case id := <-alertsRuntime:
		if err = f.runtimes.ReleaseAdapterRuntime(ctx, "alerts", adapters.RuntimeID(id)); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("alerts runtime not registered")
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("fenced sibling failure hidden")
		}
	case <-ctx.Done():
		t.Fatal("multi-adapter siblings did not stop")
	}
	if ctx.Err() != nil {
		t.Fatal("caller cancelled")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("control listener not joined: %v", err)
	}
	_ = listener.Close()
}
