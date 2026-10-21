//go:build integration

package nats_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

type endpointRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type endpointResponse struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

func requestReplyFixture(t *testing.T) (*natsgo.Conn, *contractsv1.Validator) {
	t.Helper()
	broker, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go broker.Start()
	if !broker.ReadyForConnections(testLifecycleTimeout) {
		t.Fatal("NATS did not start")
	}
	t.Cleanup(func() { broker.Shutdown(); broker.WaitForShutdown() })
	connection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return connection, validator
}

func startTestEndpoint(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	logger *slog.Logger,
	respond func(context.Context, string, natswire.Envelope[endpointRequest]) (endpointResponse, bool),
) *platformnats.RequestReplyServer {
	t.Helper()
	endpoint, err := platformnats.StartRequestReplyServer(
		connection, validator, "platform.test.requests", "test request", "registration_id",
		contractsv1.NotificationRegistrationRequestSchemaID, contractsv1.NotificationRegistrationResponseSchemaID,
		logger, respond,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = endpoint.Close(context.Background()) })
	return endpoint
}

// Close must join admitted callbacks, including after unexpected connection
// closure; caller cancellation and concurrent closes must not cancel that work.
//
//nolint:gocognit // The live broker scenarios keep callback entry, timeout, release, and dependency closure in one workflow.
func TestRequestReplyCloseJoinsCallbacksWithBoundedConcurrentWaits(t *testing.T) {
	t.Parallel()
	for _, disconnect := range []bool{false, true} {
		name := "subscription drain"
		if disconnect {
			name = "unexpected connection closure"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			connection, validator := requestReplyFixture(t)
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			endpoint := startTestEndpoint(t, connection, validator, slog.New(slog.DiscardHandler),
				func(ctx context.Context, _ string, _ natswire.Envelope[endpointRequest]) (endpointResponse, bool) {
					calls.Add(1)
					started <- ctx
					<-release
					return endpointResponse{}, false
				})
			// Unblock before the endpoint's cleanup join, even if an assertion fails.
			t.Cleanup(unblock)
			payload := []byte(
				`{"id":"reg_01900000-0000-7000-8000-000000000001","schema":"urn:hearth:schema:notification-registration-request:v1","emitted_at":"2026-10-11T12:00:00Z","correlation_id":"cor_01900000-0000-7000-8000-000000000002","data":{"key":"home","name":"Home"}}`,
			)
			if err := connection.PublishRequest("platform.test.requests", "platform.test.reply", payload); err != nil {
				t.Fatal(err)
			}
			var callbackContext context.Context
			select {
			case callbackContext = <-started:
			case <-time.After(testLifecycleTimeout):
				t.Fatal("callback did not enter")
			}
			if disconnect {
				connection.Close()
			} else if err := endpoint.Drain(); err != nil {
				t.Fatal(err)
			}
			assertConcurrentCloseTimesOut(t, endpoint)
			if callbackContext.Err() != nil {
				t.Fatalf("caller timeout canceled callback: %v", callbackContext.Err())
			}
			unblock()
			joinContext, cancelJoin := context.WithTimeout(context.Background(), testLifecycleTimeout)
			defer cancelJoin()
			if err := endpoint.Close(joinContext); err != nil {
				t.Fatalf("Close after callback release = %v", err)
			}
			if !disconnect {
				if _, err := connection.Request(
					"platform.test.requests",
					payload,
					time.Second,
				); !errors.Is(
					err,
					natsgo.ErrNoResponders,
				) {
					t.Fatalf("request after Close = %v, want no responders", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("callbacks = %d, want only admitted callback", calls.Load())
			}
		})
	}
}

func assertConcurrentCloseTimesOut(t *testing.T, endpoint *platformnats.RequestReplyServer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- endpoint.Close(ctx) }()
	}
	for range 2 {
		select {
		case err := <-results:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Close while callback is blocked = %v", err)
			}
		case <-time.After(testLifecycleTimeout):
			t.Fatal("Close exceeded caller deadline")
		}
	}
}

type capturedTransportLog struct {
	record slog.Record
	span   trace.SpanContext
}

type transportLogHandler struct {
	slog.Handler

	logs chan capturedTransportLog
}

func (*transportLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *transportLogHandler) Handle(ctx context.Context, record slog.Record) error {
	handler.logs <- capturedTransportLog{record: record.Clone(), span: trace.SpanContextFromContext(ctx)}
	return nil
}

// A real subscription must discard missing-reply input with stable safe fields
// and the operation's trace, before decoding or calling the module handler.
func TestRequestReplyMissingReplyLogsDiscardWithTrace(t *testing.T) {
	t.Parallel()
	connection, validator := requestReplyFixture(t)
	logs := make(chan capturedTransportLog, 1)
	logger := slog.New(&transportLogHandler{Handler: slog.DiscardHandler, logs: logs})
	startTestEndpoint(t, connection, validator, logger,
		func(context.Context, string, natswire.Envelope[endpointRequest]) (endpointResponse, bool) {
			t.Error("request without reply subject reached handler")
			return endpointResponse{}, false
		})
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled,
	})
	headers := make(natsgo.Header)
	natswire.InjectTrace(trace.ContextWithSpanContext(context.Background(), span), headers)
	if err := connection.PublishMsg(
		&natsgo.Msg{Subject: "platform.test.requests", Header: headers, Data: []byte(`{}`)},
	); err != nil {
		t.Fatal(err)
	}
	select {
	case log := <-logs:
		fields := make(map[string]string)
		log.record.Attrs(func(attr slog.Attr) bool { fields[attr.Key] = attr.Value.String(); return true })
		if log.record.Level != slog.LevelWarn || fields["event"] != "transport.request_discarded" ||
			fields["error_code"] != "missing_reply_subject" || fields["kind"] != "test request" {
			t.Fatalf("discard diagnostic = %v, level %v", fields, log.record.Level)
		}
		if log.span.TraceID() != span.TraceID() || log.span.SpanID() != span.SpanID() {
			t.Fatal("discard lost extracted trace context")
		}
	case <-time.After(testLifecycleTimeout):
		t.Fatal("discard diagnostic not emitted")
	}
}
