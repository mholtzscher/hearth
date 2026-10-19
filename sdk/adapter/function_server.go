package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

var functionReasonPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

const (
	functionNotExecuted     = "not_executed"
	functionOutcomeUnknown  = "unknown"
	functionDeadlineExpired = "deadline_expired"
	functionInvalidResponse = "invalid_response"
)

type functionInvokeRequest struct {
	InvocationID    string          `json:"invocation_id"`
	CatalogRevision string          `json:"catalog_revision"`
	FunctionKey     string          `json:"function_key"`
	Arguments       json.RawMessage `json:"arguments"`
	Deadline        string          `json:"deadline"`
}

type functionInvokeResponse struct {
	InvocationID string          `json:"invocation_id"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	ReasonCode   string          `json:"reason_code,omitempty"`
}

// FunctionServer owns concurrent invocation callbacks, including actual handler
// returns after an outcome deadline. A timeout reply is not a completed join.
type FunctionServer struct {
	session      *Session
	parent       context.Context
	catalog      *functionCatalog
	handlers     map[string]FunctionHandler
	subscription *natsgo.Subscription
	mutex        sync.Mutex
	closed       bool
	active       int
	done         chan struct{}
	closeErr     error
}

func (server *FunctionServer) isFinished() bool {
	select {
	case <-server.done:
		return true
	default:
		return false
	}
}

func (server *FunctionServer) isOpen() bool {
	server.mutex.Lock()
	defer server.mutex.Unlock()
	return !server.closed
}

// Wait observes closure after actual handlers return; it never stops admission.
func (server *FunctionServer) Wait(ctx context.Context) error {
	select {
	case <-server.done:
		if err := server.session.sessionError(); err != nil {
			return err
		}
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops admission, unsubscribes, and joins actual callbacks. A timed-out
// wait does not cancel the join; callers may wait again with another context.
func (server *FunctionServer) Close(ctx context.Context) error {
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
		if !errors.Is(flushErr, natsgo.ErrConnectionClosed) {
			err = errors.Join(err, flushErr)
		}
	}
	select {
	case <-server.done:
		return err
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
}

func (server *FunctionServer) receive(message *natsgo.Msg) {
	server.mutex.Lock()
	if server.closed {
		server.mutex.Unlock()
		return
	}
	server.active++
	server.mutex.Unlock()
	go func() {
		defer func() {
			server.mutex.Lock()
			defer server.mutex.Unlock()
			server.active--
			if server.closed && server.active == 0 {
				close(server.done)
			}
		}()
		server.handle(message)
	}()
}

func (server *FunctionServer) handle(message *natsgo.Msg) {
	if message.Reply == "" || len(message.Data) > maximumFunctionEnvelopeBytes {
		return
	}
	request, err := natswire.Decode[functionInvokeRequest](server.session.validator,
		contractsv1.AdapterFunctionInvokeRequestSchemaID, message.Data)
	if err != nil {
		return
	}
	route, err := natswire.ParseFunctionInvokeSubject(message.Subject)
	if err != nil || route.AdapterID != server.session.adapterID || route.RuntimeID != server.session.runtimeID ||
		request.ID != request.Data.InvocationID {
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, request.Data.Deadline)
	if err != nil {
		return
	}
	parent := natswire.ExtractTrace(server.parent, message.Header)
	if request.Data.CatalogRevision != server.catalog.id {
		server.respond(
			parent,
			message,
			request,
			functionInvokeResponse{Status: functionNotExecuted, ReasonCode: "catalog_changed"},
		)
		return
	}
	function, known := server.catalog.functions[request.Data.FunctionKey]
	if !known {
		server.respond(
			parent,
			message,
			request,
			functionInvokeResponse{Status: functionNotExecuted, ReasonCode: "function_unavailable"},
		)
		return
	}
	if !time.Now().Before(deadline) {
		server.respond(
			parent,
			message,
			request,
			functionInvokeResponse{Status: functionNotExecuted, ReasonCode: functionDeadlineExpired},
		)
		return
	}
	arguments, err := function.input.Normalize(request.Data.Arguments)
	if err != nil {
		server.respond(
			parent,
			message,
			request,
			functionInvokeResponse{Status: functionNotExecuted, ReasonCode: "invalid_input"},
		)
		return
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	stop := context.AfterFunc(server.session.lifecycleCtx, cancel)
	defer stop()
	server.runHandler(ctx, message, request, function, arguments, deadline)
}

func (server *FunctionServer) runHandler(
	ctx context.Context,
	message *natsgo.Msg,
	request natswire.Envelope[functionInvokeRequest],
	function preparedFunction,
	arguments json.RawMessage,
	deadline time.Time,
) {
	// Serialize the final changing-facts check with Session/server admission.
	// Never hold either lock while waiting for user code.
	server.session.handlerMutex.Lock()
	server.mutex.Lock()
	unavailable := server.session.closing || server.closed || server.session.sessionError() != nil
	if unavailable || ctx.Err() != nil {
		server.mutex.Unlock()
		server.session.handlerMutex.Unlock()
		reason := "function_unavailable"
		if !time.Now().Before(deadline) {
			reason = functionDeadlineExpired
		}
		server.respond(
			ctx,
			message,
			request,
			functionInvokeResponse{Status: functionNotExecuted, ReasonCode: reason},
		)
		return
	}
	completed := make(chan FunctionResult, 1)
	go func() {
		completed <- callFunctionHandler(ctx, server.handlers[request.Data.FunctionKey], FunctionRequest{
			InvocationID: request.ID, FunctionKey: request.Data.FunctionKey, Arguments: arguments, Deadline: deadline,
		})
	}()
	server.mutex.Unlock()
	server.session.handlerMutex.Unlock()
	select {
	case result := <-completed:
		response := normalizeFunctionResult(function, result)
		if !time.Now().Before(deadline) {
			response = functionInvokeResponse{Status: functionOutcomeUnknown, ReasonCode: "outcome_timeout"}
		}
		server.respond(ctx, message, request, response)
	case <-ctx.Done():
		reason := "outcome_timeout"
		if time.Now().Before(deadline) {
			reason = "transport_error"
		}
		server.respond(
			ctx,
			message,
			request,
			functionInvokeResponse{Status: functionOutcomeUnknown, ReasonCode: reason},
		)
		// Keep the callback admitted until user code actually returns.
		<-completed
	}
}

//nolint:nonamedreturns // Recovery must replace the handler result without exposing the panic value.
func callFunctionHandler(
	ctx context.Context,
	handler FunctionHandler,
	request FunctionRequest,
) (result FunctionResult) {
	defer func() {
		if recover() != nil {
			result = FunctionUnknown{ReasonCode: "handler_panic"}
		}
	}()
	return handler(ctx, request)
}

func normalizeFunctionResult(function preparedFunction, result FunctionResult) functionInvokeResponse {
	invalid := functionInvokeResponse{Status: functionOutcomeUnknown, ReasonCode: functionInvalidResponse}
	var response functionInvokeResponse
	switch value := result.(type) {
	case FunctionSucceeded:
		normalized, err := function.output.Normalize(value.Result)
		if err != nil {
			return invalid
		}
		return functionInvokeResponse{Status: "succeeded", Result: normalized}
	case FunctionFailed:
		response = functionInvokeResponse{Status: "failed", ReasonCode: value.ReasonCode}
	case FunctionNotExecuted:
		response = functionInvokeResponse{Status: functionNotExecuted, ReasonCode: value.ReasonCode}
	case FunctionUnknown:
		response = functionInvokeResponse{Status: functionOutcomeUnknown, ReasonCode: value.ReasonCode}
	default:
		return invalid
	}
	if !functionReasonPattern.MatchString(response.ReasonCode) {
		return invalid
	}
	return response
}

func (server *FunctionServer) respond(ctx context.Context, message *natsgo.Msg,
	request natswire.Envelope[functionInvokeRequest], response functionInvokeResponse,
) {
	id, err := newID("rep")
	if err != nil {
		return
	}
	response.InvocationID = request.ID
	envelope := natswire.Envelope[functionInvokeResponse]{
		ID: id, Schema: contractsv1.AdapterFunctionInvokeResponseSchemaID, EmittedAt: nowString(),
		CorrelationID: request.CorrelationID, CausationID: &request.ID, Data: response,
	}
	payload, err := natswire.Encode(
		server.session.validator,
		contractsv1.AdapterFunctionInvokeResponseSchemaID,
		envelope,
	)
	if err != nil || len(payload) > maximumFunctionEnvelopeBytes {
		envelope.Data = functionInvokeResponse{
			InvocationID: request.ID,
			Status:       functionOutcomeUnknown,
			ReasonCode:   functionInvalidResponse,
		}
		payload, err = natswire.Encode(
			server.session.validator,
			contractsv1.AdapterFunctionInvokeResponseSchemaID,
			envelope,
		)
		if err != nil {
			return
		}
	}
	reply := &natsgo.Msg{Subject: message.Reply, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, reply.Header)
	_ = server.session.connection.PublishMsg(reply)
}
