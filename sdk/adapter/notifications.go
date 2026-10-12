package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	maximumNotificationNameSize = 200
	notificationNotSent         = "not_sent"
)

type NotificationMessage struct{ Title, Text string }
type NotificationRequest struct {
	AttemptID, LocalKey string
	Message             NotificationMessage
	Deadline            time.Time
}
type NotificationOutcome struct{ Outcome, ReasonCode, Receipt string }
type NotificationHandler func(context.Context, NotificationRequest) NotificationOutcome

type notificationRegistrationRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type notificationRegistrationResponse struct {
	Status        string `json:"status"`
	DestinationID string `json:"destination_id,omitempty"`
	ReasonCode    string `json:"reason_code,omitempty"`
}
type notificationSendRequest struct {
	AttemptID string `json:"attempt_id"`
	LocalKey  string `json:"local_key"`
	Title     string `json:"title,omitempty"`
	Message   string `json:"message"`
	Deadline  string `json:"deadline"`
}
type notificationSendResponse struct {
	AttemptID  string `json:"attempt_id"`
	Outcome    string `json:"outcome"`
	ReasonCode string `json:"reason_code,omitempty"`
	Receipt    string `json:"receipt,omitempty"`
}

// A key is eligible while registration is in flight. Retain it on acceptance
// or an ambiguous failure: Core may already have committed the route. A definite
// refusal must not revoke another concurrent or previously accepted registration.
type notificationKeyRegistration struct {
	pending  int
	retained bool
}

// NotificationRegistrationRejectedError is a permanent destination registration refusal.
type NotificationRegistrationRejectedError struct{ ReasonCode string }

func (err *NotificationRegistrationRejectedError) Error() string {
	return "notification registration rejected: " + err.ReasonCode
}

// RegisterNotificationDestination retries one stable reg_ envelope. Start the
// notification server first. Local eligibility precedes publication and survives
// ambiguous failures and server restarts within this Session.
//
//nolint:gocognit // One retry loop preserves the registration identity and applies runtime fencing.
func (session *Session) RegisterNotificationDestination(ctx context.Context, key, name string) (string, error) {
	if err := session.sessionError(); err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if !slugPattern.MatchString(key) || !utf8.ValidString(name) || !validTextLength(name, maximumNotificationNameSize) {
		return "", &ValidationError{
			Err: errors.New("notification destination requires a slug key and 1–200 character name"),
		}
	}
	subject, err := natswire.NotificationRegistrationSubject(session.adapterID, session.runtimeID)
	if err != nil {
		return "", &ValidationError{Err: err}
	}
	request, err := prepareRequest(session, "reg", contractsv1.NotificationRegistrationRequestSchemaID,
		contractsv1.NotificationRegistrationResponseSchemaID, "notification registration", subject,
		notificationRegistrationRequest{Key: key, Name: name})
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	registration, err := session.beginNotificationRegistration(key)
	if err != nil {
		return "", err
	}
	permanentlyRejected := false
	defer func() {
		session.finishNotificationRegistration(key, registration, !permanentlyRejected)
	}()
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, requestAttemptTimeout)
		response, requestErr := sendSessionRequest[notificationRegistrationResponse](attemptCtx, session, request)
		cancel()
		if requestErr != nil {
			if retryErr := waitForRequestRetryLogged(
				ctx,
				session.log(),
				"notification_registration",
				requestErr,
			); retryErr != nil {
				return "", retryErr
			}
			continue
		}
		if response.Data.Status == statusRejected {
			permanentlyRejected = true
			if response.Data.ReasonCode == "stale_runtime" {
				session.markFenced(ctx)
				return "", ErrRuntimeFenced
			}
			return "", &NotificationRegistrationRejectedError{ReasonCode: response.Data.ReasonCode}
		}
		session.handlerMutex.Lock()
		if session.closing {
			session.handlerMutex.Unlock()
			return "", ErrClosed
		}
		session.handlerMutex.Unlock()
		if err = session.sessionError(); err != nil {
			return "", err
		}
		return response.Data.DestinationID, nil
	}
}

func (session *Session) beginNotificationRegistration(key string) (*notificationKeyRegistration, error) {
	session.handlerMutex.Lock()
	defer session.handlerMutex.Unlock()
	if session.closing {
		return nil, ErrClosed
	}
	if session.notificationServer == nil || !session.notificationServer.isOpen() {
		return nil, &ValidationError{Err: errors.New("notification server must be active before registration")}
	}
	if session.notificationKeys == nil {
		session.notificationKeys = make(map[string]*notificationKeyRegistration)
	}
	registration := session.notificationKeys[key]
	if registration == nil {
		registration = &notificationKeyRegistration{}
		session.notificationKeys[key] = registration
	}
	registration.pending++
	return registration, nil
}

func (session *Session) finishNotificationRegistration(
	key string,
	registration *notificationKeyRegistration,
	retain bool,
) {
	session.handlerMutex.Lock()
	defer session.handlerMutex.Unlock()
	registration.pending--
	registration.retained = registration.retained || retain
	if registration.pending == 0 && !registration.retained {
		delete(session.notificationKeys, key)
	}
}

// NotificationServer owns a flushed subscription and concurrent handlers without
// a concurrency cap. Close stops admission and joins actual handler returns, not cancellation.
type NotificationServer struct {
	session      *Session
	parent       context.Context
	handler      NotificationHandler
	subscription *natsgo.Subscription
	mutex        sync.Mutex
	closed       bool
	ready        bool
	active       int
	done         chan struct{}
	closeErr     error
}

// StartNotificationServer returns only after NATS has activated the subscription.
// One server may be active per Session; no Device or Command responder is created.
func (session *Session) StartNotificationServer(
	ctx context.Context,
	handler NotificationHandler,
) (*NotificationServer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := session.sessionError(); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, &ValidationError{Err: errors.New("notification handler is required")}
	}
	subject, err := natswire.NotificationSendSubject(session.adapterID, session.runtimeID)
	if err != nil {
		return nil, &ValidationError{Err: err}
	}
	session.handlerMutex.Lock()
	defer session.handlerMutex.Unlock()
	if session.closing {
		return nil, ErrClosed
	}
	if session.notificationServer != nil && !session.notificationServer.isFinished() {
		return nil, &ValidationError{Err: errors.New("notification server is already active")}
	}
	server := &NotificationServer{session: session, parent: ctx, handler: handler, done: make(chan struct{})}
	server.subscription, err = session.connection.Subscribe(subject, server.receive)
	if err != nil {
		return nil, fmt.Errorf("subscribe to notifications: %w", err)
	}
	flushCtx, cancel := context.WithTimeout(ctx, jetStreamFlushTimeout)
	err = session.connection.FlushWithContext(flushCtx)
	cancel()
	if err != nil {
		_ = server.subscription.Unsubscribe()
		return nil, fmt.Errorf("activate notification subscription: %w", err)
	}
	server.mutex.Lock()
	server.ready = true
	server.mutex.Unlock()
	session.notificationServer = server
	go func() {
		select {
		case <-ctx.Done():
		case <-session.closed:
		case <-server.done:
			return
		}
		// Joining must outlive the cancellation that triggered cleanup.
		_ = server.Close(context.WithoutCancel(ctx))
	}()
	return server, nil
}

func (server *NotificationServer) isOpen() bool {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return !server.closed
}
func (server *NotificationServer) isFinished() bool {
	select {
	case <-server.done:
		return true
	default:
		return false
	}
}

// Close is idempotent; a timed-out join may be resumed with another context.
func (server *NotificationServer) Close(ctx context.Context) error {
	server.mutex.Lock()
	if !server.closed {
		server.closed = true
		server.closeErr = server.subscription.Unsubscribe()
		if errors.Is(server.closeErr, natsgo.ErrConnectionClosed) {
			server.closeErr = nil
		}
		if server.active == 0 {
			close(server.done)
		}
	}
	err := server.closeErr
	server.mutex.Unlock()
	if !server.session.connection.IsClosed() {
		flushCtx, cancel := context.WithTimeout(ctx, jetStreamFlushTimeout)
		flushErr := server.session.connection.FlushWithContext(flushCtx)
		cancel()
		err = errors.Join(err, flushErr)
	}
	select {
	case <-server.done:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

func (server *NotificationServer) receive(message *natsgo.Msg) {
	server.mutex.Lock()
	ready := server.ready && !server.closed
	server.mutex.Unlock()
	if !ready {
		return
	}
	if message.Reply == "" {
		return
	}
	request, err := natswire.Decode[notificationSendRequest](
		server.session.validator,
		contractsv1.NotificationSendRequestSchemaID,
		message.Data,
	)
	if err != nil {
		return
	}
	route, err := natswire.ParseNotificationSendSubject(message.Subject)
	if err != nil || route.AdapterID != server.session.adapterID || route.RuntimeID != server.session.runtimeID ||
		request.ID != request.Data.AttemptID {
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, request.Data.Deadline)
	if err != nil {
		return
	}
	traceCtx := natswire.ExtractTrace(server.parent, message.Header)
	if server.session.sessionError() != nil {
		return
	}
	if !time.Now().Before(deadline) {
		server.respond(
			traceCtx,
			message,
			request,
			NotificationOutcome{Outcome: notificationNotSent, ReasonCode: "deadline_expired"},
		)
		return
	}
	server.session.handlerMutex.Lock()
	_, known := server.session.notificationKeys[request.Data.LocalKey]
	closing := server.session.closing
	server.session.handlerMutex.Unlock()
	if closing {
		return
	}
	if !known {
		server.respond(
			traceCtx,
			message,
			request,
			NotificationOutcome{Outcome: "rejected", ReasonCode: "destination_unknown"},
		)
		return
	}
	server.mutex.Lock()
	if server.closed {
		server.mutex.Unlock()
		return
	}
	server.active++
	server.mutex.Unlock()
	go server.handle(traceCtx, message, request, deadline)
}

func (server *NotificationServer) handle(
	parent context.Context,
	message *natsgo.Msg,
	request natswire.Envelope[notificationSendRequest],
	deadline time.Time,
) {
	defer func() {
		server.mutex.Lock()
		defer server.mutex.Unlock()
		server.active--
		if server.closed && server.active == 0 {
			close(server.done)
		}
	}()
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	stop := context.AfterFunc(server.session.lifecycleCtx, cancel)
	defer stop()
	if server.session.sessionError() != nil {
		return
	}
	if ctx.Err() != nil {
		reason := "destination_unavailable"
		if !time.Now().Before(deadline) {
			reason = "deadline_expired"
		}
		server.respond(
			parent,
			message,
			request,
			NotificationOutcome{Outcome: notificationNotSent, ReasonCode: reason},
		)
		return
	}
	outcome := server.handler(
		ctx,
		NotificationRequest{AttemptID: request.Data.AttemptID, LocalKey: request.Data.LocalKey,
			Message: NotificationMessage{Title: request.Data.Title, Text: request.Data.Message}, Deadline: deadline},
	)
	server.respond(parent, message, request, outcome)
}

func (server *NotificationServer) respond(
	ctx context.Context,
	message *natsgo.Msg,
	request natswire.Envelope[notificationSendRequest],
	outcome NotificationOutcome,
) {
	// Invalid handler evidence is never promoted to acceptance or non-delivery.
	if !utf8.ValidString(outcome.Receipt) {
		outcome = NotificationOutcome{Outcome: "unknown", ReasonCode: "invalid_response"}
	}
	id, err := newID("rep")
	if err != nil {
		return
	}
	envelope := natswire.Envelope[notificationSendResponse]{
		ID:            id,
		Schema:        contractsv1.NotificationSendResponseSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: request.CorrelationID,
		CausationID:   &request.ID,
		Data: notificationSendResponse{
			AttemptID:  request.Data.AttemptID,
			Outcome:    outcome.Outcome,
			ReasonCode: outcome.ReasonCode,
			Receipt:    outcome.Receipt,
		},
	}
	payload, err := natswire.Encode(server.session.validator, contractsv1.NotificationSendResponseSchemaID, envelope)
	if err != nil {
		envelope.Data = notificationSendResponse{
			AttemptID:  request.Data.AttemptID,
			Outcome:    "unknown",
			ReasonCode: "invalid_response",
		}
		payload, err = natswire.Encode(server.session.validator, contractsv1.NotificationSendResponseSchemaID, envelope)
		if err != nil {
			return
		}
	}
	reply := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, reply.Header)
	_ = server.session.connection.PublishMsg(reply)
}
