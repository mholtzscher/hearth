package hearthd //nolint:testpackage // Exercises real assembly and shutdown with the existing Run harness.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationsapi "github.com/mholtzscher/hearth/internal/modules/notifications/api"
	notificationsnats "github.com/mholtzscher/hearth/internal/modules/notifications/nats"
	notificationssqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type notificationFixture struct {
	service       *notifications.Service
	database      *sql.DB
	connection    *natsgo.Conn
	session       *adapter.Session
	runtimes      *devices.Service
	handler       http.Handler
	destinationID string
}

func newNotificationFixture(t *testing.T, provider adapter.NotificationHandler) notificationFixture {
	t.Helper()
	broker := startLifecycleNATSServer(t)
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
	runtimeRepo := devicessqlite.NewDeviceRepository(database, nil)
	runtimes := devices.NewService(devices.Stores{Runtimes: runtimeRepo}, nil, nil, devices.Dependencies{})
	sessions, err := devicesnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	service, err := notifications.NewService(
		t.Context(),
		notificationssqlite.NewRepository(database),
		notificationsnats.NewSender(connection, validator),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Drain(context.Background()) })
	registrations, err := notificationsnats.StartRegistrationServer(connection, validator, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Close(context.Background()) })
	session, err := adapter.Connect(
		t.Context(),
		adapter.Config{
			AdapterID:       "private-alerts",
			SoftwareName:    "controlled-provider",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	_, err = session.StartNotificationServer(t.Context(), provider)
	if err != nil {
		t.Fatal(err)
	}
	id, err := session.RegisterNotificationDestination(t.Context(), "private-route", "Household alerts")
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubDevices{}
	automations := &stubAutomations{}
	mcpServer := newMCPServer(stub, automations, service, nil)
	handler, _ := newHTTPHandler(
		stub,
		automations,
		service,
		&stubAgent{},
		&testReadiness{},
		stub,
		automations,
		mcpServer,
	)
	return notificationFixture{
		service:       service,
		database:      database,
		connection:    connection,
		session:       session,
		runtimes:      runtimes,
		handler:       handler,
		destinationID: id,
	}
}

func notificationRequest(
	t *testing.T,
	handler http.Handler,
	method, path, body string,
	status int,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: %d, want %d: %s", method, path, response.Code, status, response.Body.String())
	}
	return response
}
func decodeNotification[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func assertNotificationPrivacy(t *testing.T, data []byte) {
	t.Helper()
	for _, private := range []string{"adapter_id", "runtime_id", "local_key", "private-alerts", "private-route", "webhook", "credential"} {
		if bytes.Contains(data, []byte(private)) {
			t.Fatalf("private routing leaked %q: %s", private, data)
		}
	}
}

// A4: this boundary catches transport retries/deduplication, DTO route leakage,
// failure-as-HTTP-error mistakes and cursor scope drift over actual SDK/NATS/SQL.
//
//nolint:gocognit,gocyclo,cyclop // Keeps the transport/history evidence of one scenario together.
func TestNotificationHTTPMCPHistoryAndDistinctInvocations(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	f := newNotificationFixture(
		t,
		func(_ context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
			calls.Add(1)
			switch request.Message.Text {
			case "refused":
				return adapter.NotificationOutcome{Outcome: "rejected", ReasonCode: "provider_forbidden"}
			case "ambiguous":
				return adapter.NotificationOutcome{Outcome: "unknown", ReasonCode: "provider_error"}
			default:
				return adapter.NotificationOutcome{Outcome: "accepted", Receipt: "opaque-receipt"}
			}
		},
	)
	client := connectAssembledMCP(t, f.handler)
	path := "/v1/notification-destinations/" + f.destinationID
	destination := notificationRequest(t, f.handler, http.MethodGet, path, "", 200)
	assertNotificationPrivacy(t, destination.Body.Bytes())
	if !decodeNotification[notificationsapi.DestinationBody](t, destination.Body.Bytes()).Available {
		t.Fatal("registered route is unavailable")
	}
	list := notificationRequest(t, f.handler, http.MethodGet, "/v1/notification-destinations", "", 200)
	if page := decodeNotification[notificationsapi.DestinationCollectionBody](
		t,
		list.Body.Bytes(),
	); len(page.Items) != 1 ||
		page.NextCursor != nil {
		t.Fatalf("destinations: %+v", page)
	}
	for _, name := range []string{"list_notification_destinations", "get_notification_destination"} {
		args := map[string]any{}
		if name == "get_notification_destination" {
			args["destination_id"] = f.destinationID
		}
		if result := mcpCallTool(t, client, name, args); result.IsError {
			t.Fatalf("%s: %+v", name, result)
		}
	}
	ids := map[string]bool{}
	for range 2 {
		response := notificationRequest(
			t,
			f.handler,
			http.MethodPost,
			path+"/notifications",
			`{"title":"Freezer","message":"Too warm"}`,
			201,
		)
		assertNotificationPrivacy(t, response.Body.Bytes())
		a := decodeNotification[notificationsapi.AttemptBody](t, response.Body.Bytes())
		if a.Status != "accepted" || a.Receipt != "opaque-receipt" || a.CompletedAt == nil || ids[a.ID] {
			t.Fatalf("manual attempt: %+v", a)
		}
		ids[a.ID] = true
	}
	for range 2 {
		result := mcpCallTool(
			t,
			client,
			"send_notification",
			map[string]any{"destination_id": f.destinationID, "title": "Freezer", "message": "Too warm"},
		)
		if result.IsError {
			t.Fatalf("send: %+v", result)
		}
		fields := mcpStructuredFields(t, result)
		id := fields["id"].(string)
		if ids[id] || fields["status"] != "accepted" {
			t.Fatalf("MCP attempt: %+v", fields)
		}
		ids[id] = true
	}
	for _, text := range []string{"refused", "ambiguous"} {
		response := notificationRequest(
			t,
			f.handler,
			http.MethodPost,
			path+"/notifications",
			`{"message":"`+text+`"}`,
			201,
		)
		a := decodeNotification[notificationsapi.AttemptBody](t, response.Body.Bytes())
		if a.ReasonCode == "" || a.Status == "accepted" {
			t.Fatalf("failure evidence: %+v", a)
		}
		ids[a.ID] = true
	}
	if calls.Load() != 6 {
		t.Fatalf("provider calls = %d, want 6", calls.Load())
	}
	page := decodeNotification[notificationsapi.AttemptCollectionBody](
		t,
		notificationRequest(t, f.handler, http.MethodGet,
			"/v1/notifications?limit=2&destination_id="+f.destinationID, "", 200).Body.Bytes(),
	)
	if len(page.Items) != 2 || page.NextCursor == nil || page.Items[0].Status != "unknown" ||
		page.Items[1].Status != "rejected" {
		t.Fatalf("newest history: %+v", page)
	}
	unknown, _ := notifications.NewDestinationID()
	notificationRequest(
		t,
		f.handler,
		http.MethodGet,
		"/v1/notifications?destination_id="+string(unknown)+"&cursor="+*page.NextCursor,
		"",
		422,
	)
	empty := decodeNotification[notificationsapi.AttemptCollectionBody](
		t,
		notificationRequest(t, f.handler, http.MethodGet,
			"/v1/notifications?destination_id="+string(unknown), "", 200).Body.Bytes(),
	)
	if len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("unknown filter: %+v", empty)
	}
	seen := map[string]bool{}
	for {
		for _, a := range page.Items {
			if seen[a.ID] || !ids[a.ID] {
				t.Fatalf("duplicate or unrecognized history ID: %s", a.ID)
			}
			seen[a.ID] = true
			response := notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications/"+a.ID, "", 200)
			assertNotificationPrivacy(t, response.Body.Bytes())
			result := mcpCallTool(t, client, "get_notification", map[string]any{"id": a.ID})
			if result.IsError || mcpStructuredFields(t, result)["status"] != a.Status {
				t.Fatalf("MCP detail drift: %+v", result)
			}
		}
		if page.NextCursor == nil {
			break
		}
		page = decodeNotification[notificationsapi.AttemptCollectionBody](
			t,
			notificationRequest(
				t,
				f.handler,
				http.MethodGet,
				"/v1/notifications?limit=2&destination_id="+f.destinationID+"&cursor="+*page.NextCursor,
				"",
				200,
			).Body.Bytes(),
		)
	}
	if len(seen) != 6 {
		t.Fatalf("paged history = %d, want 6", len(seen))
	}
	result := mcpCallTool(t, client, "list_notifications", map[string]any{})
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	assertNotificationPrivacy(t, raw)
	if result.IsError || len(decodeNotification[notificationsapi.AttemptCollectionBody](t, raw).Items) != 6 {
		t.Fatalf("MCP history: %s", raw)
	}
	if err = f.session.Close(); err != nil {
		t.Fatal(err)
	}
	a := decodeNotification[notificationsapi.AttemptBody](t, notificationRequest(t, f.handler, http.MethodPost,
		path+"/notifications", `{"message":"offline"}`, 201).Body.Bytes())
	if a.Status != "not_sent" || a.ReasonCode != "destination_unavailable" || calls.Load() != 6 {
		t.Fatalf("offline evidence: %+v", a)
	}
}

// Invalid transport documents must never create an attempt or invoke a provider.
func TestNotificationStrictInputsAndStatusMapping(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	f := newNotificationFixture(t, func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
		calls.Add(1)
		return adapter.NotificationOutcome{Outcome: "accepted"}
	})
	path := "/v1/notification-destinations/" + f.destinationID + "/notifications"
	for _, body := range []string{`{"message":"ok","title":null}`, `{"message":"ok","attempt_id":"ntf_supplied"}`, `{"message":"ok","extra":true}`, `{"message":null}`, `{}`, `null`, `{"message":"  "}`, `{"message":"ok","title":17}`,
		`{"message":"` + string([]byte{0xff}) + `"}`, `{"message":"` + strings.Repeat("x", 2001) + `"}`, `{"message":"ok","title":"` + strings.Repeat("x", 257) + `"}`} {
		notificationRequest(t, f.handler, http.MethodPost, path, body, 422)
	}
	for _, escaped := range []string{`\ud800`, `\udfff`, `\ud800x`, `\ud800\ud800`, `\udfff\ud800`} {
		for _, body := range []string{
			`{"message":"` + escaped + `"}`,
			`{"message":"ok","title":"` + escaped + `"}`,
		} {
			notificationRequest(t, f.handler, http.MethodPost, path, body, 422)
		}
	}
	unknown, _ := notifications.NewDestinationID()
	notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/notification-destinations/"+string(unknown)+"/notifications",
		`{"message":"ok"}`,
		404,
	)
	missing, _ := notifications.NewAttemptID()
	notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications/"+string(missing), "", 404)
	for _, query := range []string{"limit=0", "limit=201", "cursor=invalid"} {
		notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications?"+query, "", 422)
	}
	client := connectAssembledMCP(t, f.handler)
	for _, args := range []map[string]any{
		{"destination_id": f.destinationID, "message": "ok", "title": nil},
		{"destination_id": f.destinationID, "message": "ok", "attempt_id": "ntf_supplied"},
		{"destination_id": f.destinationID, "message": "  "},
	} {
		result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "send_notification", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid MCP args accepted: %+v", args)
		}
	}
	page, err := f.service.ListAttempts(t.Context(), notifications.AttemptFilter{})
	if err != nil || len(page.Items) != 0 || calls.Load() != 0 {
		t.Fatalf("invalid input created evidence: %+v %v calls=%d", page, err, calls.Load())
	}
	f.service.ClosePublicAdmission()
	notificationRequest(t, f.handler, http.MethodPost, path, `{"message":"ok"}`, 503)
	result := mcpCallTool(
		t,
		client,
		"send_notification",
		map[string]any{"destination_id": f.destinationID, "message": "ok"},
	)
	if !result.IsError || mcpStructuredFields(t, result)["failure_code"] != "admission_closed" {
		t.Fatalf("closed MCP admission: %+v", result)
	}
}

// Valid surrogate pairs, replacement characters and literal escape-like text
// must reach the provider and retained evidence unchanged.
func TestNotificationHTTPPreservesValidUnicode(t *testing.T) {
	t.Parallel()
	received := make(chan adapter.NotificationMessage, 1)
	f := newNotificationFixture(
		t,
		func(_ context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
			received <- request.Message
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	const want = "🔔 � \\ud800"
	response := notificationRequest(t, f.handler, http.MethodPost,
		"/v1/notification-destinations/"+f.destinationID+"/notifications",
		`{"title":"\ud83d\udd14 \ufffd \\ud800","message":"\ud83d\udd14 � \\ud800"}`, 201)
	attempt := decodeNotification[notificationsapi.AttemptBody](t, response.Body.Bytes())
	if attempt.Status != "accepted" || attempt.Title != want || attempt.Message != want {
		t.Fatalf("changed Unicode in response: %+v", attempt)
	}
	select {
	case message := <-received:
		if message.Title != want || message.Text != want {
			t.Fatalf("changed Unicode at provider: %+v", message)
		}
	default:
		t.Fatal("accepted attempt did not invoke provider")
	}
	stored, err := f.service.GetAttempt(t.Context(), notifications.AttemptID(attempt.ID))
	if err != nil || stored.Message.Title != want || stored.Message.Text != want {
		t.Fatalf("changed Unicode in history: %+v, %v", stored, err)
	}
}

// Cancellation after provider entry cannot cancel admitted work on either public
// transport. Drain supplies a deterministic terminal-evidence barrier.
//
//nolint:gocognit // The two transport cancellation paths share one provider/evidence oracle.
func TestNotificationPublicCancellationDoesNotAbortEvidence(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"HTTP", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			entered := make(chan string, 1)
			release := make(chan struct{})
			f := newNotificationFixture(
				t,
				func(ctx context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
					entered <- request.AttemptID
					select {
					case <-release:
						return adapter.NotificationOutcome{Outcome: "accepted", Receipt: "survived-disconnect"}
					case <-ctx.Done():
						return adapter.NotificationOutcome{Outcome: "unknown", ReasonCode: "provider_error"}
					}
				},
			)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			if transport == "HTTP" {
				server := httptest.NewServer(f.handler)
				t.Cleanup(server.Close)
				request, err := http.NewRequestWithContext(
					ctx,
					http.MethodPost,
					server.URL+"/v1/notification-destinations/"+f.destinationID+"/notifications",
					strings.NewReader(`{"message":"cancel client"}`),
				)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				go func() {
					response, requestErr := server.Client().Do(request)
					if response != nil {
						_ = response.Body.Close()
					}
					done <- requestErr
				}()
			} else {
				client := connectAssembledMCP(t, f.handler)
				go func() {
					_, err := client.CallTool(
						ctx,
						&mcp.CallToolParams{
							Name:      "send_notification",
							Arguments: map[string]any{"destination_id": f.destinationID, "message": "cancel client"},
						},
					)
					done <- err
				}()
			}
			var id string
			select {
			case id = <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("provider never entered")
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("client did not observe cancellation")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("client cancellation blocked")
			}
			close(release)
			if err := f.service.Drain(t.Context()); err != nil {
				t.Fatal(err)
			}
			a, err := f.service.GetAttempt(t.Context(), notifications.AttemptID(id))
			if err != nil || a.Outcome != notifications.Accepted || a.Receipt != "survived-disconnect" {
				t.Fatalf("canceled invocation evidence: %+v %v", a, err)
			}
		})
	}
}

// Pause an actual notification Step after durable link reservation, before Send.
// Shutdown must retain internal admission until that worker and its evidence drain.
//
//nolint:gocognit // Admission, evidence and withdrawal assertions follow one shutdown timeline.
func TestNotificationReservedInternalSendDuringAutomationDrain(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f := newNotificationFixture(
		t,
		func(_ context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
			if request.Message.Text == "manual before drain" {
				return adapter.NotificationOutcome{Outcome: "accepted"}
			}
			entered <- struct{}{}
			<-release
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/notification-destinations/"+f.destinationID+"/notifications",
		`{"message":"manual before drain"}`,
		201,
	)
	reservedID, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	repo, automationService, deviceService, record, run := startReservedNotificationRun(t, f, reservedID)
	dependencyContext, cancelDependencies := context.WithCancel(context.Background())
	defer cancelDependencies()
	var withdrawals atomic.Int32
	shutdown := &coreShutdown{
		runContext:          t.Context(),
		logger:              slog.New(slog.DiscardHandler),
		automationService:   automationService,
		deviceService:       deviceService,
		notificationService: f.service,
		cancelDependencies:  cancelDependencies,
		transports: []registeredDrain{
			{stage: "verify_notification_evidence_before_withdrawal", drain: drainFunc(func() error {
				withdrawals.Add(1)
				a, readErr := f.service.GetAttempt(context.Background(), reservedID)
				if readErr != nil || a.Outcome != notifications.Accepted {
					return errors.New("resources withdrawn before reserved notification evidence")
				}
				return nil
			})},
		},
	}
	stopped := make(chan error, 1)
	go func() { stopped <- shutdown.run() }()
	waitForClosedAdmissionGates(t, automationService, deviceService)
	notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/notification-destinations/"+f.destinationID+"/notifications",
		`{"message":"closed public"}`,
		503,
	)
	close(repo.release)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("internal admission closed before Automation drained")
	}
	if dependencyContext.Err() != nil {
		t.Fatal("dependencies canceled before notification evidence")
	}
	if withdrawals.Load() != 0 || !f.connection.IsConnected() || f.database.PingContext(t.Context()) != nil {
		t.Fatal("NATS/SQLite resources withdrawn while reserved notification is active")
	}
	select {
	case shutdownErr := <-stopped:
		t.Fatalf("shutdown skipped admitted notification: %v", shutdownErr)
	default:
	}
	close(release)
	select {
	case shutdownErr := <-stopped:
		if shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join notification")
	}
	if dependencyContext.Err() == nil {
		t.Fatal("dependencies not closed after notification joined")
	}
	if withdrawals.Load() != 1 {
		t.Fatal("resource withdrawal did not follow notification drain")
	}
	a, err := f.service.GetAttempt(t.Context(), reservedID)
	if err != nil || a.Outcome != notifications.Accepted {
		t.Fatalf("reserved history: %+v %v", a, err)
	}
	entry, err := automationService.GetHistoryEntry(t.Context(), record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	if automations.RunStateStatus(retained.State) != automations.RunInterrupted ||
		retained.Notifications[0].Status != automations.NotificationAccepted ||
		retained.Notifications[0].Outcome == nil ||
		*retained.Notifications[0].Outcome != notifications.Accepted {
		t.Fatalf("drain overwrote accepted Step or advanced Run: %#v", retained)
	}
	page := decodeNotification[notificationsapi.AttemptCollectionBody](
		t,
		notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications", "", 200).Body.Bytes(),
	)
	if len(page.Items) != 2 || page.Items[0].ID != string(reservedID) {
		t.Fatalf("standalone history omitted manual or reserved internal attempt: %+v", page)
	}
	id, _ := notifications.NewAttemptID()
	_, err = f.service.Send(
		t.Context(),
		notifications.SendInput{
			AttemptID:     id,
			DestinationID: a.DestinationID,
			Message:       notifications.Message{Text: "after drain"},
		},
	)
	if !errors.Is(err, notifications.ErrAdmissionClosed) {
		t.Fatalf("internal admission after drain: %v", err)
	}
}

func TestNotificationPersistenceFailureReturns500WithoutLatchingReadiness(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	f := newNotificationFixture(t, func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
		calls.Add(1)
		return adapter.NotificationOutcome{Outcome: "accepted"}
	})
	notificationRequest(t, f.handler, http.MethodGet, "/readyz", "", 200)
	if _, err := f.database.Exec(
		`CREATE TRIGGER fail_notification_insert BEFORE INSERT ON notification_attempts BEGIN SELECT RAISE(FAIL, 'private-storage-error'); END`,
	); err != nil {
		t.Fatal(err)
	}
	response := notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/notification-destinations/"+f.destinationID+"/notifications",
		`{"message":"fail storage"}`,
		500,
	)
	if strings.Contains(response.Body.String(), "private-storage-error") || calls.Load() != 0 {
		t.Fatalf("storage failure leaked or dispatched: %s calls=%d", response.Body.String(), calls.Load())
	}
	notificationRequest(t, f.handler, http.MethodGet, "/readyz", "", 200)
	// Other admission checkers remain open; notifications does not own them.
	if !f.runtimes.CommandAdmissionOpen() {
		t.Fatal("Command admission changed")
	}
	// A continuing write outage is still a per-request 500, not closed admission.
	notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/notification-destinations/"+f.destinationID+"/notifications",
		`{"message":"storage still unavailable"}`,
		500,
	)
	if _, err := f.database.Exec(`DROP TRIGGER fail_notification_insert`); err != nil {
		t.Fatal(err)
	}
	notificationRequest(
		t,
		f.handler,
		http.MethodPost,
		"/v1/notification-destinations/"+f.destinationID+"/notifications",
		`{"message":"independent send after storage recovers"}`,
		201,
	)
	if calls.Load() != 1 {
		t.Fatalf("independent send dispatched %d times", calls.Load())
	}
}

// Shared scheduling must respect notification-owned completion eligibility and
// never cascade into Automation history. A real linked Step is future scope.
func TestNotificationSharedPruningAndRecoveryHistory(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	f := newNotificationFixture(t, func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
		calls.Add(1)
		return adapter.NotificationOutcome{Outcome: "accepted"}
	})
	path := "/v1/notification-destinations/" + f.destinationID + "/notifications"
	old := decodeNotification[notificationsapi.AttemptBody](
		t,
		notificationRequest(t, f.handler, http.MethodPost, path, `{"message":"old"}`, 201).Body.Bytes(),
	)
	boundary := decodeNotification[notificationsapi.AttemptBody](
		t,
		notificationRequest(t, f.handler, http.MethodPost, path, `{"message":"boundary"}`, 201).Body.Bytes(),
	)
	sweep := time.Now().UTC()
	cutoff := sweep.Add(-notifications.Retention)
	for id, completed := range map[string]time.Time{old.ID: cutoff.Add(-time.Nanosecond), boundary.ID: cutoff} {
		const timestampLayout = "2006-01-02T15:04:05.000000000Z"
		if _, err := f.database.Exec(
			`UPDATE notification_attempts SET started_at = ?, deadline = ?, completed_at = ? WHERE id = ?`,
			completed.Add(-time.Second).Format(timestampLayout),
			completed.Add(notifications.SendTimeout-time.Second).Format(timestampLayout),
			completed.Format(timestampLayout),
			id,
		); err != nil {
			t.Fatal(err)
		}
	}
	activeID, _ := notifications.NewAttemptID()
	repo := notificationssqlite.NewRepository(f.database)
	_, _, err := repo.Admit(
		t.Context(),
		notifications.SendInput{
			AttemptID:     activeID,
			DestinationID: notifications.DestinationID(f.destinationID),
			Message: notifications.Message{
				Text: "interrupted",
			},
		},
		cutoff.Add(-time.Hour),
		cutoff.Add(-time.Hour).Add(notifications.SendTimeout),
	)
	if err != nil {
		t.Fatal(err)
	}
	seam := newBlockingAutomationDevices()
	close(seam.release)
	automationService := automations.NewService(
		automationssqlite.NewAutomationRepository(f.database, automations.Dependencies{}),
		seam,
		automations.Dependencies{HistoryRetention: 90 * 24 * time.Hour, Logger: slog.New(slog.DiscardHandler)},
	)
	record := createOrderingAutomation(t.Context(), t, automationService)
	run, err := automationService.StartManualRun(t.Context(), automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = automationService.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	scheduler := newHistoryPruneScheduler(
		slog.New(slog.DiscardHandler),
		newHistoryPruneTasks(&stubHistoryPruner{}, automationService, &stubHistoryPruner{}, f.service),
	)
	scheduler.runPass(t.Context(), sweep)
	notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications/"+old.ID, "", 404)
	notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications/"+boundary.ID, "", 200)
	running := decodeNotification[notificationsapi.AttemptBody](
		t,
		notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications/"+string(activeID), "", 200).Body.Bytes(),
	)
	if running.Status != "running" || running.CompletedAt != nil {
		t.Fatalf("pruned active record: %+v", running)
	}
	if _, err = automationService.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err != nil {
		t.Fatalf("notification prune changed retained Run: %v", err)
	}
	// Conversely, Automation pruning alone must not touch independently eligible
	// notification evidence.
	if err = automationService.PruneHistory(t.Context(), sweep.Add(91*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = automationService.GetHistoryEntry(
		t.Context(),
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrHistoryNotFound,
	) {
		t.Fatalf("Automation retention did not remove its Run: %v", err)
	}
	notificationRequest(t, f.handler, http.MethodGet, "/v1/notifications/"+boundary.ID, "", 200)
	// Startup recovery terminalizes but never publishes the interrupted identity.
	recovered, err := notifications.NewService(
		t.Context(),
		repo,
		notificationsnats.NewSender(f.connection, mustNotificationValidator(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recovered.Drain(context.Background()) }()
	a, err := recovered.GetAttempt(t.Context(), activeID)
	if err != nil || a.Outcome != notifications.Unknown || a.ReasonCode != "core_restarted" || calls.Load() != 2 {
		t.Fatalf("recovery: %+v %v calls=%d", a, err, calls.Load())
	}
}

func mustNotificationValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

// Production startup must install registration, HTTP and the agent's MCP catalog
// without requiring a synthetic Device. This catches omissions in run.go that a
// manually assembled transport fixture cannot detect.
//
//nolint:gocognit // Linear process startup and persisted teardown checks stay in lifecycle order.
func TestNotificationProductionAssembly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	broker := startLifecycleNATSServer(t)
	address, options := reserveLoopbackListener(t)
	config := Config{HouseholdTimezone: "UTC", HTTPAddr: address, NATSURL: broker.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "core.db"), Agent: requiredAgentConfig(t)}
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() { runErrors <- runWithOptions(runContext, config, slog.New(slog.DiscardHandler), options) }()
	client := newNonPoolingHTTPClient(t)
	waitForCoreHTTPStatus(ctx, t, client, address, "/readyz", runErrors)
	session, err := adapter.Connect(
		ctx,
		adapter.Config{
			AdapterID:       "only-notifications",
			SoftwareName:    "controlled-provider",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	_, err = session.StartNotificationServer(
		ctx,
		func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	id, err := session.RegisterNotificationDestination(ctx, "home", "Home")
	if err != nil {
		t.Fatal(err)
	}
	mcpClient, err := mcp.NewClient(&mcp.Implementation{Name: "production-catalog-test", Version: "1"}, nil).
		Connect(ctx,
			&mcp.StreamableClientTransport{Endpoint: "http://" + address + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := mcpClient.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "notification") {
			count++
		}
	}
	if count != 5 {
		t.Fatalf("production notification tools = %d, want 5", count)
	}
	if err = mcpClient.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+address+"/v1/notification-destinations/"+id+"/notifications",
		strings.NewReader(`{"message":"production assembly"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("production send = %d: %s", response.StatusCode, raw)
	}
	a := decodeNotification[notificationsapi.AttemptBody](t, raw)
	if a.Status != "accepted" {
		t.Fatalf("production attempt: %+v", a)
	}
	// Release while Core still serves its Session lifecycle.
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("production shutdown did not finish")
	}
	database, err := platformdb.Open(ctx, config.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	saved, err := notificationssqlite.NewRepository(database).GetAttempt(ctx, notifications.AttemptID(a.ID))
	if err != nil || saved.Outcome != notifications.Accepted {
		t.Fatalf("production durable attempt: %+v %v", saved, err)
	}
	var devicesCount int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM devices`).
		Scan(&devicesCount); err != nil ||
		devicesCount != 0 {
		t.Fatalf("synthetic Devices = %d: %v", devicesCount, err)
	}
}
