//go:build integration

package adapter //nolint:testpackage // Reuses the real-NATS Session harness and inspects wire DTOs.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const testDestinationID = "ndst_01890f47-7a6b-7c4d-8e9f-0123456789ab"

func notificationHarness(t *testing.T) (*Session, *natsgo.Conn) {
	t.Helper()
	broker := startServer(t, -1, t.TempDir())
	session := connectSession(t, broker.ClientURL())
	core := connectNATS(t, broker.ClientURL())
	_, err := core.Subscribe(natswire.NotificationRegistrationWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[notificationRegistrationRequest](
			session.validator,
			contractsv1.NotificationRegistrationRequestSchemaID,
			message.Data,
		)
		if decodeErr != nil {
			t.Error(decodeErr)
			return
		}
		if request.Data.Name != "Household" {
			t.Errorf("name was not normalized: %q", request.Data.Name)
		}
		respondTestLifecycle(
			t,
			session.validator,
			message,
			request.ID,
			request.CorrelationID,
			contractsv1.NotificationRegistrationResponseSchemaID,
			notificationRegistrationResponse{Status: "accepted", DestinationID: testDestinationID},
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Flush(); err != nil {
		t.Fatal(err)
	}
	return session, core
}

func notificationPayload(t *testing.T, session *Session, key string, deadline time.Time) []byte {
	t.Helper()
	id, err := newID("ntf")
	if err != nil {
		t.Fatal(err)
	}
	correlation, err := newID("cor")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(session.validator, contractsv1.NotificationSendRequestSchemaID,
		natswire.Envelope[notificationSendRequest]{
			ID:            id,
			Schema:        contractsv1.NotificationSendRequestSchemaID,
			EmittedAt:     nowString(),
			CorrelationID: correlation,
			Data: notificationSendRequest{AttemptID: id, LocalKey: key,
				Title: "Freezer", Message: "Too warm", Deadline: deadline.UTC().Format(time.RFC3339Nano)},
		})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func requestNotification(t *testing.T, session *Session, core *natsgo.Conn, payload []byte) notificationSendResponse {
	t.Helper()
	subject, err := natswire.NotificationSendSubject(session.adapterID, session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := core.RequestWithContext(testContext(t), subject, payload)
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[notificationSendResponse](
		session.validator,
		contractsv1.NotificationSendResponseSchemaID,
		message.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := natswire.Decode[notificationSendRequest](
		session.validator,
		contractsv1.NotificationSendRequestSchemaID,
		payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.CausationID == nil || *response.CausationID != request.ID ||
		response.CorrelationID != request.CorrelationID ||
		response.Data.AttemptID != request.Data.AttemptID {
		t.Fatalf("response identity mismatch: %#v", response)
	}
	return response.Data
}

//nolint:gocognit // Exercises admission guards through one actual NATS subscription.
func TestNotificationReadinessRouteGuardExpiryAndMalformedRequests(t *testing.T) {
	t.Parallel()
	session, core := notificationHarness(t)
	if _, err := session.RegisterNotificationDestination(testContext(t), "household", "Household"); err == nil {
		t.Fatal("registration allowed without serving subscription")
	}
	var calls atomic.Int32
	server, err := session.StartNotificationServer(
		testContext(t),
		func(ctx context.Context, request NotificationRequest) NotificationOutcome {
			calls.Add(1)
			if request.LocalKey != "household" || request.Message.Text != "Too warm" ||
				request.Message.Title != "Freezer" {
				t.Errorf("unexpected request: %#v", request)
			}
			if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(request.Deadline) {
				t.Error("handler deadline differs from wire deadline")
			}
			return NotificationOutcome{Outcome: "accepted", Receipt: "123"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(testContext(t)) })
	if _, startErr := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome { return NotificationOutcome{} },
	); startErr == nil {
		t.Fatal("two servers active")
	}
	// No polling: immediately after Start returns a request must be routable.
	response := requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if response.Outcome != "rejected" || response.ReasonCode != "destination_unknown" {
		t.Fatalf("unregistered route = %#v", response)
	}
	id, err := session.RegisterNotificationDestination(testContext(t), "household", " Household ")
	if err != nil || id != testDestinationID {
		t.Fatalf("registration = %q, %v", id, err)
	}
	response = requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(-time.Second)),
	)
	if response.Outcome != "not_sent" || response.ReasonCode != "deadline_expired" {
		t.Fatalf("expired = %#v", response)
	}
	response = requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "other", time.Now().Add(time.Second)),
	)
	if response.ReasonCode != "destination_unknown" {
		t.Fatalf("unknown route = %#v", response)
	}
	subject, err := natswire.NotificationSendSubject(session.adapterID, session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	valid := notificationPayload(t, session, "household", time.Now().Add(time.Second))
	for _, malformed := range []string{
		strings.Replace(string(valid), `"message":"Too warm"`, `"message":"\u000b"`, 1),
		strings.Replace(string(valid), `"message":"Too warm"`, `"message":"\u0085\u3000"`, 1),
		strings.Replace(string(valid), `"title":"Freezer"`, `"title":null`, 1),
		strings.Replace(string(valid), `"message":"Too warm"`, `"message":null`, 1),
		strings.Replace(string(valid), `"message":"Too warm"`, `"message":"Too warm","secret":"forbidden"`, 1),
		strings.Replace(string(valid), `"attempt_id":"ntf_`, `"attempt_id":"cmd_`, 1),
	} {
		ctx, cancel := context.WithTimeout(testContext(t), 50*time.Millisecond)
		_, requestErr := core.RequestWithContext(ctx, subject, []byte(malformed))
		cancel()
		if !errors.Is(requestErr, context.DeadlineExceeded) {
			t.Fatalf("malformed reply error = %v", requestErr)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("guarded requests invoked provider %d times", calls.Load())
	}
	response = requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if response.Outcome != "accepted" || response.Receipt != "123" || calls.Load() != 1 {
		t.Fatalf("accepted = %#v; calls=%d", response, calls.Load())
	}
}

type notificationRegistrationResult struct {
	id  string
	err error
}

func pendingNotificationHarness(t *testing.T) (*Session, *natsgo.Conn, <-chan *natsgo.Msg, *NotificationServer) {
	t.Helper()
	broker := startServer(t, -1, t.TempDir())
	session := connectSession(t, broker.ClientURL())
	core := connectNATS(t, broker.ClientURL())
	requests := make(chan *natsgo.Msg, 4)
	_, err := core.Subscribe(
		natswire.NotificationRegistrationWildcard(),
		func(message *natsgo.Msg) { requests <- message },
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Flush(); err != nil {
		t.Fatal(err)
	}
	server, err := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			return NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(testContext(t)) })
	return session, core, requests, server
}

func registerNotificationAsync(
	ctx context.Context,
	session *Session,
	key string,
) <-chan notificationRegistrationResult {
	result := make(chan notificationRegistrationResult, 1)
	go func() {
		id, err := session.RegisterNotificationDestination(ctx, key, "Household")
		result <- notificationRegistrationResult{id, err}
	}()
	return result
}

func replyNotificationRegistration(t *testing.T, session *Session, message *natsgo.Msg, status string) {
	t.Helper()
	request, err := natswire.Decode[notificationRegistrationRequest](
		session.validator,
		contractsv1.NotificationRegistrationRequestSchemaID,
		message.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	response := notificationRegistrationResponse{Status: status, DestinationID: testDestinationID}
	if status == statusRejected {
		response = notificationRegistrationResponse{Status: statusRejected, ReasonCode: "invalid_registration"}
	}
	respondTestLifecycle(
		t,
		session.validator,
		message,
		request.ID,
		request.CorrelationID,
		contractsv1.NotificationRegistrationResponseSchemaID,
		response,
	)
}

func TestNotificationRouteServesBeforeRegistrationReplyAndAfterLostReplyRestart(t *testing.T) {
	t.Parallel()
	session, core, requests, server := pendingNotificationHarness(t)
	ctx, cancel := context.WithCancel(testContext(t))
	defer cancel()
	result := registerNotificationAsync(ctx, session, "household")
	select {
	case <-requests: // Core has committed registration but its reply is lost.
	case <-testContext(t).Done():
		t.Fatal("registration was not published")
	}
	response := requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if response.Outcome != "accepted" {
		t.Fatalf("route before reply = %#v", response)
	}
	cancel()
	if got := <-result; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("lost reply cancellation = %+v", got)
	}
	if err := server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	restarted, err := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			return NotificationOutcome{Outcome: "accepted", Receipt: "restarted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(testContext(t)) })
	response = requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if response.Outcome != "accepted" || response.Receipt != "restarted" {
		t.Fatalf("route after lost reply and restart = %#v", response)
	}
}

func TestNotificationRegistrationRejectionDoesNotRevokeConcurrentOrAcceptedRoute(t *testing.T) {
	t.Parallel()
	session, core, requests, _ := pendingNotificationHarness(t)
	first := registerNotificationAsync(testContext(t), session, "household")
	firstRequest := <-requests
	second := registerNotificationAsync(testContext(t), session, "household")
	secondRequest := <-requests
	replyNotificationRegistration(t, session, firstRequest, statusRejected)
	var rejected *NotificationRegistrationRejectedError
	if got := <-first; !errors.As(got.err, &rejected) {
		t.Fatalf("permanent rejection = %+v", got)
	}
	response := requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if response.Outcome != "accepted" {
		t.Fatalf("concurrent pending route was revoked = %#v", response)
	}
	replyNotificationRegistration(t, session, secondRequest, statusAccepted)
	if got := <-second; got.err != nil || got.id != testDestinationID {
		t.Fatalf("accepted registration = %+v", got)
	}
	for _, key := range []string{"household", "refused"} {
		result := registerNotificationAsync(testContext(t), session, key)
		replyNotificationRegistration(t, session, <-requests, statusRejected)
		if got := <-result; !errors.As(got.err, &rejected) {
			t.Fatalf("rejected %s = %+v", key, got)
		}
		response = requestNotification(
			t,
			session,
			core,
			notificationPayload(t, session, key, time.Now().Add(time.Second)),
		)
		want := "accepted"
		if key == "refused" {
			want = "rejected"
		}
		if response.Outcome != want {
			t.Fatalf("route %s after rejection = %#v", key, response)
		}
	}
}

func TestSessionCloseStopsPendingNotificationRegistration(t *testing.T) {
	t.Parallel()
	session, _, requests, _ := pendingNotificationHarness(t)
	result := registerNotificationAsync(testContext(t), session, "household")
	select {
	case <-requests:
	case <-testContext(t).Done():
		t.Fatal("registration was not published")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if !errors.Is(got.err, ErrClosed) || got.id != "" {
			t.Fatalf("registration after Session.Close = %+v", got)
		}
	case <-testContext(t).Done():
		t.Fatal("Session.Close left registration running")
	}
	if _, err := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			return NotificationOutcome{Outcome: "accepted"}
		},
	); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("closed Session restarted server: %v", err)
	}
}

func TestNotificationConcurrentHandlersCloseStopsAdmissionAndJoinsActualReturns(t *testing.T) {
	t.Parallel()
	session, core := notificationHarness(t)
	started := make(chan struct{}, 5)
	expired := make(chan struct{})
	finish := make(chan struct{})
	finishHandlers := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(finishHandlers)
	var calls atomic.Int32
	server, err := session.StartNotificationServer(
		testContext(t),
		func(ctx context.Context, _ NotificationRequest) NotificationOutcome {
			call := calls.Add(1)
			started <- struct{}{}
			if call == 1 {
				<-ctx.Done()
				close(expired)
			}
			<-finish
			return NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(testContext(t), "household", "Household"); err != nil {
		t.Fatal(err)
	}
	replies := make(chan notificationSendResponse, 5)
	for index := range 5 {
		deadline := time.Now().Add(5 * time.Second)
		if index == 0 {
			deadline = time.Now().Add(100 * time.Millisecond)
		}
		payload := notificationPayload(t, session, "household", deadline)
		go func() { replies <- requestNotification(t, session, core, payload) }()
		select {
		case <-started:
		case <-testContext(t).Done():
			t.Fatal("handler did not start")
		}
	}
	select {
	case <-expired:
	case <-testContext(t).Done():
		t.Fatal("handler deadline did not expire")
	}
	// Cancellation cannot finish the join while external handler work is still running.
	ctx, cancel := context.WithTimeout(testContext(t), 50*time.Millisecond)
	closeErr := server.Close(ctx)
	cancel()
	if !errors.Is(closeErr, context.DeadlineExceeded) {
		t.Fatalf("close did not wait for provider returns: %v", closeErr)
	}
	subject, err := natswire.NotificationSendSubject(session.adapterID, session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.RequestWithContext(
		testContext(t),
		subject,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if !errors.Is(err, natsgo.ErrNoResponders) {
		t.Fatalf("closed subscription error = %v", err)
	}
	finishHandlers()
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if reply := <-replies; reply.Outcome != "accepted" {
			t.Fatalf("admitted reply = %#v", reply)
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("close admitted new provider work: calls=%d", calls.Load())
	}
}

// Wait's context belongs to the observer, not the server or admitted handlers.
func TestNotificationWaitCancellationLeavesHandlersRunningAndAllowsLaterJoin(t *testing.T) {
	t.Parallel()
	session, core := notificationHarness(t)
	started := make(chan struct{}, 2)
	finish := make(chan struct{})
	finishHandlers := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(finishHandlers)
	server, err := session.StartNotificationServer(testContext(t),
		func(ctx context.Context, _ NotificationRequest) NotificationOutcome {
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return NotificationOutcome{Outcome: "not_sent", ReasonCode: "destination_unavailable"}
			case <-finish:
				return NotificationOutcome{Outcome: "accepted"}
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(testContext(t), "household", "Household"); err != nil {
		t.Fatal(err)
	}
	replies := make(chan notificationSendResponse, 2)
	send := func() {
		payload := notificationPayload(t, session, "household", time.Now().Add(5*time.Second))
		go func() { replies <- requestNotification(t, session, core, payload) }()
		select {
		case <-started:
		case <-testContext(t).Done():
			t.Fatal("handler did not start")
		}
	}
	send()
	waitCtx, cancel := context.WithTimeout(testContext(t), 50*time.Millisecond)
	err = server.Wait(waitCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled observer = %v", err)
	}
	select {
	case reply := <-replies:
		t.Fatalf("Wait stopped admitted handler: %+v", reply)
	default:
	}
	// A new real request proves cancellation also left the subscription open.
	send()
	joined := make(chan error, 1)
	go func() { joined <- server.Wait(testContext(t)) }()
	select {
	case err = <-joined:
		t.Fatalf("later Wait finished before server close: %v", err)
	default:
	}
	finishHandlers()
	for range 2 {
		if reply := <-replies; reply.Outcome != "accepted" {
			t.Fatalf("observer cancellation changed handler outcome: %+v", reply)
		}
	}
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-joined:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("later Wait join = %v", err)
		}
	case <-testContext(t).Done():
		t.Fatal("later Wait did not observe joined server")
	}
}

func TestNotificationRegistrationRetriesIdenticalBytesAndFencesSession(t *testing.T) {
	t.Parallel()
	broker := startServer(t, -1, t.TempDir())
	session := connectSession(t, broker.ClientURL())
	core := connectNATS(t, broker.ClientURL())
	var calls atomic.Int32
	server, err := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			calls.Add(1)
			return NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan []byte, 3)
	var registrations atomic.Int32
	_, err = core.Subscribe(natswire.NotificationRegistrationWildcard(), func(message *natsgo.Msg) {
		requests <- append([]byte(nil), message.Data...)
		request, decodeErr := natswire.Decode[notificationRegistrationRequest](
			session.validator,
			contractsv1.NotificationRegistrationRequestSchemaID,
			message.Data,
		)
		if decodeErr != nil {
			t.Error(decodeErr)
			return
		}
		attempt := registrations.Add(1)
		if attempt == 1 {
			return
		}
		response := notificationRegistrationResponse{Status: "accepted", DestinationID: testDestinationID}
		if attempt == 3 {
			response = notificationRegistrationResponse{Status: "rejected", ReasonCode: "stale_runtime"}
		}
		respondTestLifecycle(
			t,
			session.validator,
			message,
			request.ID,
			request.CorrelationID,
			contractsv1.NotificationRegistrationResponseSchemaID,
			response,
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(testContext(t), "household", "Household"); err != nil {
		t.Fatal(err)
	}
	first, second := <-requests, <-requests
	if string(first) != string(second) {
		t.Fatal("registration retry changed identity or bytes")
	}
	// A different runtime subject has no subscriber and cannot call the provider.
	otherRuntime, err := newID("run")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.NotificationSendSubject(session.adapterID, otherRuntime)
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.RequestWithContext(
		testContext(t),
		subject,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if !errors.Is(err, natsgo.ErrNoResponders) {
		t.Fatalf("wrong runtime error = %v", err)
	}
	if _, err = session.RegisterNotificationDestination(
		testContext(t),
		"household",
		"Household",
	); !errors.Is(
		err,
		ErrRuntimeFenced,
	) {
		t.Fatalf("stale registration = %v", err)
	}
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome { return NotificationOutcome{} },
	); !errors.Is(
		err,
		ErrRuntimeFenced,
	) {
		t.Fatalf("fenced startup = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("fenced or wrong-runtime work invoked provider %d times", calls.Load())
	}
}

func TestNotificationInvalidHandlerEvidenceRemainsUnknown(t *testing.T) {
	t.Parallel()
	session, core := notificationHarness(t)
	server, err := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			return NotificationOutcome{Outcome: "accepted", ReasonCode: "provider_error", Receipt: "123"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(testContext(t), "household", "Household"); err != nil {
		t.Fatal(err)
	}
	response := requestNotification(
		t,
		session,
		core,
		notificationPayload(t, session, "household", time.Now().Add(time.Second)),
	)
	if response.Outcome != "unknown" || response.ReasonCode != "invalid_response" || response.Receipt != "" {
		t.Fatalf("invalid handler evidence = %#v", response)
	}
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationSendRejectsMismatchedAttemptIdentity(t *testing.T) {
	t.Parallel()
	session, core := notificationHarness(t)
	var calls atomic.Int32
	server, err := session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			calls.Add(1)
			return NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(testContext(t), "household", "Household"); err != nil {
		t.Fatal(err)
	}
	payload := notificationPayload(t, session, "household", time.Now().Add(time.Second))
	var envelope map[string]any
	if err = json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	id, err := newID("ntf")
	if err != nil {
		t.Fatal(err)
	}
	envelope["id"] = id
	payload, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.NotificationSendSubject(session.adapterID, session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(testContext(t), 50*time.Millisecond)
	_, err = core.RequestWithContext(ctx, subject, payload)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
		t.Fatalf("mismatched identity error=%v calls=%d", err, calls.Load())
	}
	if err = server.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCloseJoinsNotificationsBeforeRuntimeRelease(t *testing.T) {
	t.Parallel()
	session, core := notificationHarness(t)
	started := make(chan struct{})
	finish := make(chan struct{})
	finishHandler := sync.OnceFunc(func() { close(finish) })
	t.Cleanup(finishHandler)
	var returned atomic.Bool
	released := make(chan bool, 1)
	_, err := core.Subscribe(natswire.AdapterReleaseWildcard(), func(*natsgo.Msg) { released <- returned.Load() })
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Flush(); err != nil {
		t.Fatal(err)
	}
	_, err = session.StartNotificationServer(
		testContext(t),
		func(context.Context, NotificationRequest) NotificationOutcome {
			close(started)
			<-finish
			returned.Store(true)
			return NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = session.RegisterNotificationDestination(testContext(t), "household", "Household"); err != nil {
		t.Fatal(err)
	}
	payload := notificationPayload(t, session, "household", time.Now().Add(5*time.Second))
	reply := make(chan notificationSendResponse, 1)
	go func() { reply <- requestNotification(t, session, core, payload) }()
	select {
	case <-started:
	case <-testContext(t).Done():
		t.Fatal("handler did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case <-released:
		t.Fatal("runtime released while notification handler was running")
	case <-closed:
		t.Fatal("Session.Close returned while notification handler was running")
	case <-time.After(100 * time.Millisecond):
	}
	finishHandler()
	select {
	case joined := <-released:
		if !joined {
			t.Fatal("release preceded provider return")
		}
	case <-testContext(t).Done():
		t.Fatal("runtime was not released")
	}
	if err = <-closed; err != nil {
		t.Fatal(err)
	}
	if response := <-reply; response.Outcome != "accepted" {
		t.Fatalf("admitted response = %#v", response)
	}
}
