package discord //nolint:testpackage // Injects the HTTP transport at application assembly, not via operator config.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	discordadapter "github.com/mholtzscher/hearth/internal/adapters/discord"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
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

const integrationWebhook = "https://discord.com/api/webhooks/123/not-real-credential"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type coreFixture struct {
	connection    *natsgo.Conn
	validator     *contractsv1.Validator
	runtimes      *adapters.Service
	functions     *adapters.FunctionService
	notifications *notifications.Service
	url           string
}

// Uses the same real Core Session/notification/SQLite boundary as the notifications SDK harness.
func startCore(t *testing.T) coreFixture {
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
	repository := adapterssqlite.NewAdapterRepository(database)
	runtimes := adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Close(context.Background()) })
	functions, err := adapters.NewFunctionService(
		t.Context(),
		repository,
		adaptersnats.NewFunctionInvoker(connection, validator),
	)
	if err != nil {
		t.Fatal(err)
	}
	functionRegistrations, err := adaptersnats.StartFunctionRegistrationServer(connection, validator, functions, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = functionRegistrations.Close(context.Background()) })
	service, err := notifications.NewService(
		t.Context(),
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
	return coreFixture{
		connection:    connection,
		validator:     validator,
		runtimes:      runtimes,
		functions:     functions,
		notifications: service,
		url:           broker.ClientURL(),
	}
}

func processConfig(t *testing.T, natsURL string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webhook")
	if err := os.WriteFile(path, []byte(integrationWebhook+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Config{AdapterID: "discord-main", NATSURL: natsURL, Destinations: map[string]DestinationConfig{
		"household": {Name: " Household alerts ", WebhookURLFile: path},
		"old-route": {Name: "Old route", WebhookURLFile: path},
	}}
}

func waitReady(t *testing.T, core coreFixture, count int) []notifications.Destination {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := core.notifications.ListDestinations(ctx, 20, "")
		if err != nil {
			t.Fatal(err)
		}
		instance, getErr := core.runtimes.GetAdapter(ctx, "discord-main")
		if len(page.Items) == count && getErr == nil && instance.Health.Status == adapters.AdapterHealthHealthy {
			return page.Items
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("process not ready")
		}
	}
}

func sendInput(t *testing.T, id notifications.DestinationID) notifications.SendInput {
	t.Helper()
	attemptID, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return notifications.SendInput{
		AttemptID:     attemptID,
		DestinationID: id,
		Message:       notifications.Message{Title: "Freezer", Text: "Too warm"},
	}
}

func awaitRun(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join")
	}
}

func destinationNamed(t *testing.T, destinations []notifications.Destination, name string) notifications.Destination {
	t.Helper()
	for _, destination := range destinations {
		if destination.Name == name {
			return destination
		}
	}
	t.Fatalf("missing destination %s", name)
	return notifications.Destination{}
}

// A3 protects assembled startup ordering, credential-free registration, durable receipts,
// provider isolation, shutdown cancellation and stable additive registration across restart.
func TestProcessRegistersSendsJoinsAndRestarts(t *testing.T) {
	t.Parallel()
	core := startCore(t)
	config := processConfig(t, core.url)
	registrations := make(chan []byte, 4)
	subscription, err := core.connection.Subscribe(
		natswire.NotificationRegistrationWildcard(),
		func(message *natsgo.Msg) { registrations <- append([]byte(nil), message.Data...) },
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = subscription.Unsubscribe() }()
	if err = core.connection.Flush(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	blocked := make(chan struct{})
	returned := make(chan struct{})
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"12345"}`)),
				Header:     make(http.Header),
			}, nil
		case 2:
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(integrationWebhook)),
				Header:     make(http.Header),
			}, nil
		default:
			close(blocked)
			<-request.Context().Done()
			close(returned)
			return nil, request.Context().Err()
		}
	})
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	result := make(chan error, 1)
	go func() { result <- run(ctx, config, slog.New(slog.DiscardHandler), transport) }()
	destinations := waitReady(t, core, 2)
	home := destinationNamed(t, destinations, "Household alerts")
	assertRegistrations(t, registrations)
	accepted, err := core.notifications.Send(t.Context(), sendInput(t, home.ID))
	if err != nil || accepted.Outcome != notifications.Accepted || accepted.Receipt != "12345" {
		t.Fatalf("send=%+v %v", accepted, err)
	}
	saved, err := core.notifications.GetAttempt(t.Context(), accepted.ID)
	if err != nil || saved.Receipt != "12345" {
		t.Fatalf("durable receipt=%+v %v", saved, err)
	}
	failure, err := core.notifications.Send(t.Context(), sendInput(t, home.ID))
	if err != nil || failure.Outcome != notifications.Unknown || failure.ReasonCode != "provider_error" {
		t.Fatalf("failure=%+v %v", failure, err)
	}
	requestDone := make(chan struct{})
	go func() { _, _ = core.notifications.Send(t.Context(), sendInput(t, home.ID)); close(requestDone) }()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP not admitted")
	}
	stop()
	awaitRun(t, result)
	select {
	case <-returned:
	default:
		t.Fatal("runtime released before HTTP returned")
	}
	<-requestDone
	instance, err := core.runtimes.GetAdapter(t.Context(), "discord-main")
	if err != nil || instance.Health.Runtime.Status != "offline" {
		t.Fatalf("release=%+v %v", instance, err)
	}
	delete(config.Destinations, "old-route")
	restartCtx, stopRestart := context.WithCancel(t.Context())
	defer stopRestart()
	go func() { result <- run(restartCtx, config, slog.New(slog.DiscardHandler), transport) }()
	destinations = waitReady(t, core, 2)
	if got := destinationNamed(t, destinations, "Household alerts"); got.ID != home.ID || !got.Available {
		t.Fatalf("restart identity=%+v", got)
	}
	if got := destinationNamed(t, destinations, "Old route"); got.Available {
		t.Fatal("omitted route reconciled or available")
	}
	stopRestart()
	awaitRun(t, result)
	if calls.Load() != 3 {
		t.Fatalf("startup/restart submitted HTTP: %d", calls.Load())
	}
}

// A3 traverses the real SDK subscription with a real Discord handler: wrong runtime,
// expired and unknown routes cannot submit; fencing cannot erase already admitted work.
//
//nolint:gocognit // The guards and concurrent fence share one actual subscription and provider admission.
func TestNATSAdmissionGuardsAndConcurrentFencing(t *testing.T) {
	t.Parallel()
	core := startCore(t)
	session, err := adapter.Connect(
		t.Context(),
		adapter.Config{
			AdapterID:       "discord-main",
			SoftwareName:    "hearth-adapter-discord",
			SoftwareVersion: "1",
			NATSURL:         core.url,
			Logger:          slog.New(slog.DiscardHandler),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	var calls atomic.Int32
	started := make(chan struct{})
	finish := make(chan struct{})
	finishHandler := sync.OnceFunc(func() { close(finish) })
	client, err := discordadapter.New(
		map[string]string{"household": integrationWebhook},
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			close(started)
			<-finish
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"123"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := session.StartNotificationServer(
		t.Context(),
		func(ctx context.Context, request adapter.NotificationRequest) adapter.NotificationOutcome {
			return client.Send(ctx, request.LocalKey, request.Message)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close(context.Background()) }()
	defer finishHandler()
	if _, err = session.RegisterNotificationDestination(t.Context(), "household", "Household alerts"); err != nil {
		t.Fatal(err)
	}
	instance, err := core.runtimes.GetAdapter(t.Context(), "discord-main")
	if err != nil {
		t.Fatal(err)
	}
	runtimeID := string(instance.Health.Runtime.ID)
	subject, err := natswire.NotificationSendSubject("discord-main", runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	otherSubject, err := natswire.NotificationSendSubject("discord-main", "run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.connection.Request(
		otherSubject,
		notificationPayload(t, "household", time.Now().Add(time.Second)),
		time.Second,
	)
	if !errors.Is(err, natsgo.ErrNoResponders) {
		t.Fatalf("wrong runtime=%v", err)
	}
	for _, test := range []struct {
		key             string
		deadline        time.Time
		outcome, reason string
	}{
		{"household", time.Now().Add(-time.Second), "not_sent", "deadline_expired"},
		{"unknown", time.Now().Add(time.Second), "rejected", "destination_unknown"},
	} {
		message, requestErr := core.connection.Request(
			subject,
			notificationPayload(t, test.key, test.deadline),
			time.Second,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		// Wire reason_code needs its own DTO rather than the SDK's Go field spelling.
		var envelope struct {
			Data struct {
				Outcome string `json:"outcome"`
				Reason  string `json:"reason_code"`
			} `json:"data"`
		}
		if err = json.Unmarshal(message.Data, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.Outcome != test.outcome || envelope.Data.Reason != test.reason {
			t.Fatalf("guard=%s", message.Data)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("guard submitted webhook")
	}
	admitted := make(chan error, 1)
	go func() {
		_, requestErr := core.connection.Request(
			subject,
			notificationPayload(t, "household", time.Now().Add(5*time.Second)),
			5*time.Second,
		)
		admitted <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no HTTP admission")
	}
	if err = core.runtimes.ReleaseAdapterRuntime(
		t.Context(),
		"discord-main",
		adapters.RuntimeID(runtimeID),
	); err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(
		t.Context(),
		"household",
		"Household alerts",
	); !errors.Is(
		err,
		adapter.ErrRuntimeFenced,
	) {
		t.Fatalf("fencing=%v", err)
	}
	if err = session.Wait(t.Context()); !errors.Is(err, adapter.ErrRuntimeFenced) {
		t.Fatalf("terminal session wait=%v", err)
	}
	_, err = core.connection.Request(
		subject,
		notificationPayload(t, "household", time.Now().Add(time.Second)),
		100*time.Millisecond,
	)
	if err == nil {
		t.Fatal("known-fenced request responded")
	}
	if calls.Load() != 1 {
		t.Fatal("known-fenced request submitted HTTP")
	}
	finishHandler()
	<-admitted // Fencing may close NATS before the already-submitted provider result returns.
	if err = server.Close(t.Context()); err != nil && !errors.Is(err, natsgo.ErrConnectionClosed) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("fencing replayed admitted HTTP")
	}
}

func assertRegistrations(t *testing.T, registrations <-chan []byte) {
	t.Helper()
	for range 2 {
		select {
		case payload := <-registrations:
			if strings.Contains(string(payload), integrationWebhook) || strings.Contains(string(payload), "webhook") {
				t.Fatal("registration contains credentials")
			}
			var envelope struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Data) != 2 || envelope.Data["key"] == "" || envelope.Data["name"] == "" {
				t.Fatalf("registration=%s", payload)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing registration")
		}
	}
}

func notificationPayload(t *testing.T, key string, deadline time.Time) []byte {
	t.Helper()
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"id": string(
			id,
		),
		"schema":         contractsv1.NotificationSendRequestSchemaID,
		"emitted_at":     time.Now().UTC().Format(time.RFC3339Nano),
		"correlation_id": "cor_" + strings.TrimPrefix(string(id), "ntf_"),
		"data": map[string]string{
			"attempt_id": string(id),
			"local_key":  key,
			"title":      "Freezer",
			"message":    "Too warm",
			"deadline":   deadline.UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestFunctionSendMessageRoundTrip(t *testing.T) {
	t.Parallel()
	core := startCore(t)
	config := processConfig(t, core.url)
	var calls atomic.Int32
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"777"}`)),
			Header:     make(http.Header),
		}, nil
	})
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	result := make(chan error, 1)
	go func() { result <- run(ctx, config, slog.New(slog.DiscardHandler), transport) }()
	_ = waitReady(t, core, 2)
	function, err := core.functions.GetFunction(t.Context(), "discord-main", "send_message")
	if err != nil {
		t.Fatal(err)
	}
	var functionSchema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(function.Descriptor.InputSchema, &functionSchema); err != nil {
		t.Fatal(err)
	}
	if got := functionSchema.Properties["destination"].Enum; !slices.Equal(got, []string{"household", "old-route"}) {
		t.Fatalf("destination enum=%v", got)
	}
	_, err = core.functions.Invoke(t.Context(), adapters.InvokeInput{
		AdapterID:       "discord-main",
		FunctionKey:     "send_message",
		CatalogRevision: function.CatalogRevision,
		Arguments:       json.RawMessage(`{"destination":"Household alerts","text":"Too warm"}`),
	})
	if !errors.Is(err, adapters.ErrInvalidFunctionInput) {
		t.Fatalf("display-name invocation error=%v", err)
	}
	invocation, err := core.functions.Invoke(t.Context(), adapters.InvokeInput{
		AdapterID:       "discord-main",
		FunctionKey:     "send_message",
		CatalogRevision: function.CatalogRevision,
		Arguments:       json.RawMessage(`{"destination":"household","text":"Too warm","title":"Freezer"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Completion == nil {
		t.Fatal("missing completion")
	}
	succeeded, ok := invocation.Completion.Evidence.(adapters.Succeeded)
	if !ok {
		t.Fatalf("evidence=%#v", invocation.Completion.Evidence)
	}
	var decoded struct {
		Destination string `json:"destination"`
		Receipt     string `json:"receipt"`
		Status      string `json:"status"`
	}
	if unmarshalErr := json.Unmarshal(succeeded.Result, &decoded); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if decoded.Destination != "household" || decoded.Receipt != "777" || decoded.Status != "sent" {
		t.Fatalf("result=%s", succeeded.Result)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	stop()
	awaitRun(t, result)
}

func TestInvalidConfigAndSecretsFailBeforeClaim(t *testing.T) {
	t.Parallel()
	core := startCore(t)
	var claims atomic.Int32
	sub, err := core.connection.Subscribe(natswire.AdapterClaimWildcard(), func(*natsgo.Msg) { claims.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err = core.connection.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "https://evil.test/api/webhooks/123/secret", integrationWebhook + "?thread_id=123"} {
		config := processConfig(t, core.url)
		if err = os.WriteFile(config.Destinations["household"].WebhookURLFile, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		runErr := Run(t.Context(), config, slog.New(slog.DiscardHandler))
		if runErr == nil || value != "" && strings.Contains(runErr.Error(), value) {
			t.Fatalf("invalid secret result=%v", runErr)
		}
	}
	config := processConfig(t, core.url)
	config.Destinations = nil
	if err = Run(t.Context(), config, nil); err == nil {
		t.Fatal("empty routes accepted")
	}
	if claims.Load() != 0 {
		t.Fatalf("claimed before validation: %d", claims.Load())
	}
	if _, err = core.runtimes.GetAdapter(t.Context(), "discord-main"); !errors.Is(err, adapters.ErrAdapterNotFound) {
		t.Fatalf("invalid startup persisted an Adapter: %v", err)
	}
}
