package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const (
	maximumFunctionRegistrationBytes = 256 * 1024
	maximumFunctionEnvelopeBytes     = 128 * 1024
	maximumFunctionDescriptionSize   = 2000
)

type FunctionDescriptor struct {
	Key          string          `json:"key"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
	TimeoutMS    int             `json:"timeout_ms"`
}

type Function struct {
	Descriptor FunctionDescriptor
	Handler    FunctionHandler
}

type FunctionRequest struct {
	InvocationID string
	FunctionKey  string
	Arguments    json.RawMessage
	Deadline     time.Time
}

type FunctionHandler func(context.Context, FunctionRequest) FunctionResult
type FunctionResult interface{ functionResult() }
type FunctionSucceeded struct{ Result json.RawMessage }
type FunctionFailed struct{ ReasonCode string }
type FunctionNotExecuted struct{ ReasonCode string }
type FunctionUnknown struct{ ReasonCode string }

func (FunctionSucceeded) functionResult()   {}
func (FunctionFailed) functionResult()      {}
func (FunctionNotExecuted) functionResult() {}
func (FunctionUnknown) functionResult()     {}

type FunctionRegistrationRejectedError struct{ ReasonCode string }

func (err *FunctionRegistrationRejectedError) Error() string {
	return "function registration rejected: " + err.ReasonCode
}

type preparedFunction struct {
	input  *functionschema.Schema
	output *functionschema.Schema
}

// The Session retains this preparation even after an ambiguous registration or
// a server close. Handlers belong to the server, not to the retained catalog.
type functionCatalog struct {
	id                 string
	descriptors        []FunctionDescriptor
	encodedDescriptors []byte
	functions          map[string]preparedFunction
	registration       preparedRequest
	headers            natsgo.Header
}

type functionRegistrationRequest struct {
	CatalogRevision string               `json:"catalog_revision"`
	Functions       []FunctionDescriptor `json:"functions"`
}

type functionRegistrationResponse struct {
	Status          string `json:"status"`
	CatalogRevision string `json:"catalog_revision,omitempty"`
	ReasonCode      string `json:"reason_code,omitempty"`
}

func prepareFunctionCatalog(functions []Function) (*functionCatalog, map[string]FunctionHandler, error) {
	if len(functions) < 1 || len(functions) > 64 {
		return nil, nil, errors.New("function catalog requires 1 to 64 functions")
	}
	catalog := &functionCatalog{functions: make(map[string]preparedFunction, len(functions))}
	handlers := make(map[string]FunctionHandler, len(functions))
	for _, function := range functions {
		descriptor := function.Descriptor
		if function.Handler == nil || !slugPattern.MatchString(descriptor.Key) ||
			!utf8.ValidString(descriptor.Description) || strings.TrimSpace(descriptor.Description) == "" ||
			!validTextLength(
				descriptor.Description,
				maximumFunctionDescriptionSize,
			) || descriptor.TimeoutMS < 100 || descriptor.TimeoutMS > 60000 {
			return nil, nil, errors.New("invalid function descriptor or missing handler")
		}
		if _, exists := handlers[descriptor.Key]; exists {
			return nil, nil, errors.New("duplicate function key")
		}
		input, err := functionschema.Compile(descriptor.InputSchema)
		if err != nil {
			return nil, nil, errors.New("invalid function input schema")
		}
		output, err := functionschema.Compile(descriptor.OutputSchema)
		if err != nil {
			return nil, nil, errors.New("invalid function output schema")
		}
		descriptor.InputSchema = input.Document()
		descriptor.OutputSchema = output.Document()
		catalog.descriptors = append(catalog.descriptors, descriptor)
		catalog.functions[descriptor.Key] = preparedFunction{input: input, output: output}
		handlers[descriptor.Key] = function.Handler
	}
	slices.SortFunc(catalog.descriptors, func(a, b FunctionDescriptor) int { return strings.Compare(a.Key, b.Key) })
	encoded, err := json.Marshal(catalog.descriptors)
	if err != nil {
		return nil, nil, errors.New("encode function descriptors")
	}
	catalog.encodedDescriptors = encoded
	return catalog, handlers, nil
}

func (catalog *functionCatalog) prepareRegistration(ctx context.Context, session *Session) error {
	id, err := newID("afc")
	if err != nil {
		return err
	}
	correlation, err := newID("cor")
	if err != nil {
		return err
	}
	subject, err := natswire.FunctionRegistrationSubject(session.adapterID, session.runtimeID)
	if err != nil {
		return err
	}
	payload, err := natswire.Encode(session.validator, contractsv1.AdapterFunctionRegistrationRequestSchemaID,
		natswire.Envelope[functionRegistrationRequest]{
			ID:            id,
			Schema:        contractsv1.AdapterFunctionRegistrationRequestSchemaID,
			EmittedAt:     nowString(),
			CorrelationID: correlation,
			Data:          functionRegistrationRequest{CatalogRevision: id, Functions: catalog.descriptors},
		})
	if err != nil || len(payload) > maximumFunctionRegistrationBytes {
		return &ValidationError{Err: errors.New("function registration exceeds wire contract")}
	}
	catalog.id = id
	catalog.registration = preparedRequest{id: id, correlationID: correlation, subject: subject, payload: payload}
	catalog.headers = make(natsgo.Header)
	natswire.InjectTrace(ctx, catalog.headers)
	return nil
}

// StartFunctionServer activates and flushes serving before registering the
// immutable catalog. Handlers may run before this method returns.
func (session *Session) StartFunctionServer(ctx context.Context, functions []Function) (*FunctionServer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := session.sessionError(); err != nil {
		return nil, err
	}
	catalog, handlers, err := prepareFunctionCatalog(functions)
	if err != nil {
		return nil, &ValidationError{Err: err}
	}
	server, err := session.activateFunctionServer(ctx, catalog, handlers)
	if err != nil {
		return nil, err
	}
	if err = server.register(ctx); err != nil {
		// A reply may be lost after Core commits. Join any work already admitted
		// before returning cancellation or another start error.
		_ = server.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-session.closed:
		case <-server.done:
			return
		}
		_ = server.Close(context.WithoutCancel(ctx))
	}()
	return server, nil
}

func (session *Session) activateFunctionServer(
	ctx context.Context, catalog *functionCatalog, handlers map[string]FunctionHandler,
) (*FunctionServer, error) {
	session.handlerMutex.Lock()
	if session.closing || session.sessionError() != nil {
		session.handlerMutex.Unlock()
		if err := session.sessionError(); err != nil {
			return nil, err
		}
		return nil, ErrClosed
	}
	if session.functionServer != nil && !session.functionServer.isFinished() {
		session.handlerMutex.Unlock()
		return nil, &ValidationError{Err: errors.New("function server is already active")}
	}
	if session.functionCatalog != nil {
		if !bytes.Equal(catalog.encodedDescriptors, session.functionCatalog.encodedDescriptors) {
			session.handlerMutex.Unlock()
			return nil, &ValidationError{Err: errors.New("function catalog cannot change within a Session")}
		}
		catalog = session.functionCatalog
	} else {
		if err := catalog.prepareRegistration(ctx, session); err != nil {
			session.handlerMutex.Unlock()
			return nil, err
		}
		session.functionCatalog = catalog
	}
	subject, err := natswire.FunctionInvokeSubject(session.adapterID, session.runtimeID)
	if err != nil {
		session.handlerMutex.Unlock()
		return nil, &ValidationError{Err: err}
	}
	server := &FunctionServer{session: session, catalog: catalog, handlers: handlers, done: make(chan struct{}),
		parent: context.WithoutCancel(ctx)}
	server.subscription, err = session.connection.Subscribe(subject, server.receive)
	if err != nil {
		session.handlerMutex.Unlock()
		return nil, fmt.Errorf("subscribe to functions: %w", err)
	}
	session.functionServer = server
	session.handlerMutex.Unlock()
	flushCtx, cancel := context.WithTimeout(ctx, jetStreamFlushTimeout)
	err = session.connection.FlushWithContext(flushCtx)
	cancel()
	if err != nil {
		_ = server.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return server, nil
}

func (server *FunctionServer) register(ctx context.Context) error {
	for {
		if err := server.registrationOpen(ctx); err != nil {
			return err
		}
		attemptCtx, cancel := context.WithTimeout(ctx, requestAttemptTimeout)
		response, err := server.registerAttempt(attemptCtx)
		cancel()
		if err != nil {
			if stateErr := server.registrationOpen(ctx); stateErr != nil {
				return stateErr
			}
			if retryErr := waitForRequestRetryLogged(
				ctx,
				server.session.log(),
				"function_registration",
				err,
			); retryErr != nil {
				return retryErr
			}
			continue
		}
		if response.Status == statusRejected {
			if response.ReasonCode == "stale_runtime" {
				server.session.markFenced(ctx)
				return ErrRuntimeFenced
			}
			return &FunctionRegistrationRejectedError{ReasonCode: response.ReasonCode}
		}
		if response.CatalogRevision != server.catalog.id {
			return errors.New("function registration revision mismatch")
		}
		return server.registrationOpen(ctx)
	}
}

func (server *FunctionServer) registrationOpen(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := server.session.sessionError(); err != nil {
		return err
	}
	if !server.isOpen() {
		return ErrClosed
	}
	return nil
}

func (server *FunctionServer) registerAttempt(ctx context.Context) (functionRegistrationResponse, error) {
	request := server.catalog.registration
	reply, err := server.session.connection.RequestMsgWithContext(ctx, &natsgo.Msg{
		Subject: request.subject, Header: server.catalog.headers, Data: request.payload,
	})
	if err != nil {
		return functionRegistrationResponse{}, err
	}
	if len(reply.Data) > maximumFunctionRegistrationBytes {
		return functionRegistrationResponse{}, errors.New("oversized registration response")
	}
	response, err := natswire.Decode[functionRegistrationResponse](server.session.validator,
		contractsv1.AdapterFunctionRegistrationResponseSchemaID, reply.Data)
	if err != nil {
		return functionRegistrationResponse{}, errors.New("invalid function registration response")
	}
	if response.CorrelationID != request.correlationID || response.CausationID == nil ||
		*response.CausationID != request.id {
		return functionRegistrationResponse{}, errors.New("function registration response identity mismatch")
	}
	return response.Data, nil
}
