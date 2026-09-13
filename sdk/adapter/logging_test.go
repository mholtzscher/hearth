package adapter //nolint:testpackage // Tests exercise package-private logging behavior.

// This test protects the reduced SDK logging contract: canonical IDs,
// sentinel safety, context preservation, and actionable diagnostics.
// Business sequencing (fencing, release retries, accept/reject codes) is
// covered in session, availability, and command evidence tests.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

type lockedWriter struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (writer *lockedWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *lockedWriter) snapshot() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.String()
}

func recordingLogger(t *testing.T, writer *lockedWriter) *slog.Logger {
	t.Helper()
	logger, err := logging.NewApplicationLogger(writer, "hearth-simulator", logging.LogOptions{
		Level: "debug", Format: "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	return logger
}

func parseLogRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if unmarshalErr := json.Unmarshal([]byte(line), &record); unmarshalErr != nil {
			t.Fatalf("log line does not parse as JSON: %v\nline: %s", unmarshalErr, line)
		}
		records = append(records, record)
	}
	return records
}

//nolint:unparam // Shared with session_test; every caller currently waits 3s but keeps an explicit timeout.
func waitForLogRecord(
	t *testing.T,
	writer *lockedWriter,
	event string,
	match func(map[string]any) bool,
	timeout time.Duration,
) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, record := range parseLogRecords(t, writer.snapshot()) {
			if record["event"] == event && (match == nil || match(record)) {
				return record
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log event %q", event)
	return nil
}

func connectSessionWithLogger(t *testing.T, url string, logger *slog.Logger) *Session {
	t.Helper()
	startTestLifecycleResponder(t, url)
	config := testConfig(url)
	config.Logger = logger
	session, err := Connect(testContext(t), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func requireStringID(t *testing.T, record map[string]any, key string) {
	t.Helper()
	value, ok := record[key].(string)
	if !ok || value == "" {
		t.Errorf("record %q = %v, want non-empty string", key, record[key])
	}
}

func requireHeartbeatCadence(t *testing.T, session *Session, cadence time.Duration) {
	t.Helper()
	if session.heartbeatInterval != cadence {
		t.Fatalf("session heartbeat interval = %s, want %s", session.heartbeatInterval, cadence)
	}
}

// Serves accepted bindings and one rejected binding with a sentinel that must never reach logs.
func startLoggingRegistrationResponder(t *testing.T, connection *natsgo.Conn, validator *contractsv1.Validator) {
	t.Helper()
	_, err := connection.Subscribe(natswire.RegistrationWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[Registration](
			validator, contractsv1.RegistrationRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode registration request: %v", decodeErr)
			return
		}
		response := RegistrationResponse{Status: statusAccepted, Binding: &Binding{
			BindingKey: request.Data.BindingKey, DeviceID: testDeviceID,
			Entities: []EntityBinding{{Key: "power", EntityID: testEntityID, Enabled: true}},
		}}
		if request.Data.BindingKey == "rejected-light" {
			response = RegistrationResponse{Status: statusRejected, Error: &RegistrationError{
				Code: "identity_conflict", Message: "SENTINEL-binding-owned-by-another-adapter",
			}}
		}
		respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
			contractsv1.RegistrationResponseSchemaID, response)
	})
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := connection.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
}

func checkCanonicalClaim(t *testing.T, claimed map[string]any, runtimeID string) {
	t.Helper()
	if claimed["adapter_id"] != "simulator" {
		t.Errorf("adapter_id = %v, want simulator", claimed["adapter_id"])
	}
	if claimed["runtime_id"] != runtimeID || claimed["runtime_id"] == "" {
		t.Errorf("runtime_id = %v, want claimed session %q", claimed["runtime_id"], runtimeID)
	}
	if claimed["correlation_id"] == nil || claimed["correlation_id"] == "" {
		t.Errorf("correlation_id = %v, want claim request correlation", claimed["correlation_id"])
	}
	if claimed["app"] != "hearth-simulator" || claimed["component"] != "adapter_session" {
		t.Errorf("record identity = %v/%v, want hearth-simulator/adapter_session",
			claimed["app"], claimed["component"])
	}
	if pid, ok := claimed["pid"].(float64); !ok || int(pid) != os.Getpid() {
		t.Errorf("pid = %v, want %d", claimed["pid"], os.Getpid())
	}
}

func checkUniqueRootKeys(t *testing.T, writer *lockedWriter) {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSpace(writer.snapshot()), "\n") {
		for _, key := range []string{`"app":`, `"pid":`, `"component":`, `"adapter_id":`, `"event":`} {
			if strings.Count(line, key) != 1 {
				t.Errorf("log line has %d occurrences of %s, want 1:\n%s",
					strings.Count(line, key), key, line)
			}
		}
		if got := strings.Count(line, `"runtime_id":`); got > 1 {
			t.Errorf("log line has %d occurrences of runtime_id, want at most 1:\n%s", got, line)
		}
	}
}

func TestLoggingCanonicalSessionAndRegistration(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	startLoggingRegistrationResponder(t, core, compileValidator(t))

	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	claimed := waitForLogRecord(t, writer, "adapter.session_claimed", nil, 3*time.Second)
	checkCanonicalClaim(t, claimed, session.runtimeID)

	binding, err := session.Register(testContext(t), validRegistration("office-light"))
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForLogRecord(t, writer, "adapter.registration_completed", nil, 3*time.Second)
	if completed["device_id"] != binding.DeviceID || completed["entity_count"] != float64(1) {
		t.Errorf("registration summary = %v/%v, want %s/1",
			completed["device_id"], completed["entity_count"], binding.DeviceID)
	}
	if completed["entity_id"] != testEntityID {
		t.Errorf("single-entity summary entity_id = %v, want %s", completed["entity_id"], testEntityID)
	}
	mapping := waitForLogRecord(t, writer, "adapter.registration_mapping", nil, 3*time.Second)
	if mapping["level"] != "DEBUG" {
		t.Errorf("registration mapping level = %v, want DEBUG", mapping["level"])
	}
	if mapping["device_id"] != testDeviceID || mapping["entity_id"] != testEntityID ||
		mapping["entity_key"] != "power" {
		t.Errorf("registration mapping = %v", mapping)
	}

	checkUniqueRootKeys(t, writer)
}

func TestLoggingRejectionsHideRemoteDetail(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	startLoggingRegistrationResponder(t, core, compileValidator(t))

	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	if _, registerErr := session.Register(
		testContext(t), validRegistration("rejected-light"),
	); registerErr == nil {
		t.Fatal("rejected registration unexpectedly succeeded")
	}
	rejectedRegistration := waitForLogRecord(t, writer, "adapter.registration_rejected", nil, 3*time.Second)
	if rejectedRegistration["level"] != "WARN" ||
		rejectedRegistration["rejection_code"] != "identity_conflict" {
		t.Errorf("registration rejection = %v, want WARN/identity_conflict", rejectedRegistration)
	}

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext,
			func(_ context.Context, _ Command, responder Responder) error {
				return responder.RejectUnavailable("SENTINEL-entity-down-for-maintenance")
			})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	reply, err := sendCommand(context.Background(), core, session.runtimeID, false)
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[CommandResponse](
		compileValidator(t), contractsv1.CommandResponseSchemaID, reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	rejected := waitForLogRecord(t, writer, "command.rejected", func(record map[string]any) bool {
		return record["command_id"] == response.Data.CommandID
	}, 3*time.Second)
	if rejected["level"] != "WARN" || rejected["rejection_code"] != "entity_unavailable" {
		t.Errorf("command rejected = %v, want WARN/entity_unavailable", rejected)
	}
	requireStringID(t, rejected, "command_id")
	requireStringID(t, rejected, "correlation_id")

	for _, sentinel := range []string{"SENTINEL-binding-owned-by-another-adapter", "SENTINEL-entity-down-for-maintenance"} {
		if strings.Contains(writer.snapshot(), sentinel) {
			t.Errorf("remote detail %q leaked into logs", sentinel)
		}
	}

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

func TestLoggingDiscardedCommandHidesPayload(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext,
			func(context.Context, Command, Responder) error {
				t.Error("handler invoked for a discardable command")
				return nil
			})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	subject, err := natswire.CommandSubject("simulator", session.runtimeID, testEntityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	malicious := `{"SENTINEL-malicious-payload": "<script>alert(1)</script>"}`
	if publishErr := core.PublishRequest(subject, natsgo.NewInbox(), []byte(malicious)); publishErr != nil {
		t.Fatal(publishErr)
	}
	discarded := waitForLogRecord(t, writer, "command.discarded", func(record map[string]any) bool {
		return record["error_code"] == "invalid_envelope"
	}, 3*time.Second)
	if discarded["level"] != "WARN" {
		t.Errorf("discard level = %v, want WARN", discarded["level"])
	}
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "command.discarded" && record["subject"] != nil {
			t.Errorf("discard record carries raw subject: %v", record)
		}
	}
	if strings.Contains(writer.snapshot(), "SENTINEL-malicious-payload") {
		t.Error("malicious protocol body leaked into logs")
	}

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

type lifecycleContextKey struct{}

type contextValueHandler struct {
	slog.Handler

	key  any
	name string
}

func (handler *contextValueHandler) Handle(ctx context.Context, record slog.Record) error {
	if value, ok := ctx.Value(handler.key).(string); ok {
		record.AddAttrs(slog.String(handler.name, value))
	}
	return handler.Handler.Handle(ctx, record)
}

func (handler *contextValueHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextValueHandler{Handler: handler.Handler.WithAttrs(attrs), key: handler.key, name: handler.name}
}
func (handler *contextValueHandler) WithGroup(name string) slog.Handler {
	return &contextValueHandler{Handler: handler.Handler.WithGroup(name), key: handler.key, name: handler.name}
}

func valueRecordingLogger(writer *lockedWriter, key any) *slog.Logger {
	base := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(&contextValueHandler{Handler: base, key: key, name: "test_ctx_value"}).
		With("app", "hearth-simulator", "pid", os.Getpid())
}

func TestLoggingReleasePreservesContext(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	startTestLifecycleResponder(t, server.ClientURL())
	writer := &lockedWriter{}
	config := testConfig(server.ClientURL())
	config.Logger = valueRecordingLogger(writer, lifecycleContextKey{})
	ctx := context.WithValue(testContext(t), lifecycleContextKey{}, "lifecycle-value")
	session, err := Connect(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	claimed := waitForLogRecord(t, writer, "adapter.session_claimed", nil, 3*time.Second)
	if claimed["test_ctx_value"] != "lifecycle-value" {
		t.Fatalf("claim record misses lifecycle value, harness cannot observe context: %v", claimed)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	released := waitForLogRecord(t, writer, "adapter.session_released", nil, 3*time.Second)
	if released["test_ctx_value"] != "lifecycle-value" {
		t.Errorf("release record = %v, want lifecycle context value preserved", released)
	}
	requireStringID(t, released, "runtime_id")
}

func TestLoggingAcceptedHandlerFailure(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext,
			func(_ context.Context, _ Command, responder Responder) error {
				if _, acceptErr := responder.Accept(); acceptErr != nil {
					return acceptErr
				}
				return errors.New("post-acceptance bookkeeping failed")
			})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	reply, err := sendCommand(context.Background(), core, session.runtimeID, true)
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[CommandResponse](
		compileValidator(t), contractsv1.CommandResponseSchemaID, reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForLogRecord(t, writer, "command.handler_failed", func(record map[string]any) bool {
		return record["command_id"] == response.Data.CommandID
	}, 3*time.Second)
	if failed["level"] != "ERROR" || failed["error_code"] != "handler_failed" {
		t.Errorf("accepted-handler failure = %v, want ERROR/handler_failed", failed)
	}
	if failed["entity_id"] != testEntityID || failed["correlation_id"] != response.CorrelationID {
		t.Errorf("accepted-handler failure = %v", failed)
	}
	if strings.Contains(writer.snapshot(), "post-acceptance bookkeeping failed") {
		t.Error("handler error text leaked into logs")
	}
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "command.accepted" || record["event"] == "command.received" {
			t.Errorf("optional event %q must not be logged: %v", record["event"], record)
		}
	}

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

func TestLoggingNATSDiagnosticsHideDetail(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	subscription := &natsgo.Subscription{Subject: "SENTINEL-async-subject"}
	session.connection.Opts.AsyncErrorCB(session.connection, subscription,
		errors.New("SENTINEL-async-error token=hunter2"))
	asyncFailed := waitForLogRecord(t, writer, "dependency.operation_failed", nil, 3*time.Second)
	if asyncFailed["level"] != "ERROR" {
		t.Errorf("async error level = %v, want ERROR", asyncFailed["level"])
	}
	if asyncFailed["dependency"] != "nats" || asyncFailed["error_code"] != "nats_async_error" {
		t.Errorf("async error record = %v, want dependency nats code nats_async_error", asyncFailed)
	}

	session.onDisconnected(nil, errors.New("SENTINEL-disconnect"))
	disconnected := waitForLogRecord(t, writer, "dependency.disconnected", nil, 3*time.Second)
	if disconnected["level"] != "WARN" || disconnected["dependency"] != "nats" {
		t.Errorf("disconnect record = %v, want WARN dependency nats", disconnected)
	}
	if disconnected["error_code"] != "connection_lost" {
		t.Errorf("disconnect error_code = %v, want connection_lost", disconnected["error_code"])
	}

	for _, sentinel := range []string{"SENTINEL-async-error", "SENTINEL-async-subject", "hunter2", "SENTINEL-disconnect"} {
		if strings.Contains(writer.snapshot(), sentinel) {
			t.Errorf("NATS diagnostic leaked %q into logs", sentinel)
		}
	}
}

// logRecordEvent returns the event attribute carried by a log record.
func logRecordEvent(record slog.Record) string {
	event := ""
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "event" {
			event = attr.Value.String()
		}
		return true
	})
	return event
}

// blockingOptionalCommandLogHandler stalls any optional command debug record.
// A synchronous slog sink can block indefinitely on stderr, so handler and
// Accept progress must not depend on command.received or command.accepted.
type blockingOptionalCommandLogHandler struct {
	base    slog.Handler
	blockOn context.Context
}

func (handler *blockingOptionalCommandLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.base.Enabled(ctx, level)
}

func (handler *blockingOptionalCommandLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if event := logRecordEvent(record); event == "command.received" || event == "command.accepted" {
		<-handler.blockOn.Done()
		return nil
	}
	return handler.base.Handle(ctx, record)
}

func (handler *blockingOptionalCommandLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &blockingOptionalCommandLogHandler{base: handler.base.WithAttrs(attrs), blockOn: handler.blockOn}
}

func (handler *blockingOptionalCommandLogHandler) WithGroup(name string) slog.Handler {
	return &blockingOptionalCommandLogHandler{base: handler.base.WithGroup(name), blockOn: handler.blockOn}
}

// This test protects command handler and Accept progress and fails if the
// removed optional debug events return and stall on a blocked log sink.
func TestLoggingCommandProgressWithBlockedOptionalEvents(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	writer := &lockedWriter{}
	base, err := logging.NewApplicationLogger(writer, "hearth-simulator", logging.LogOptions{
		Level: "debug", Format: "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	blockContext, cancelBlock := context.WithCancel(context.Background())
	defer cancelBlock()
	blockingLogger := slog.New(&blockingOptionalCommandLogHandler{base: base.Handler(), blockOn: blockContext})
	session := connectSessionWithLogger(t, server.ClientURL(), blockingLogger)

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	handlerEntered := make(chan struct{}, 1)
	acceptedEvidence := make(chan CommandEvidence, 1)
	go func() {
		serveDone <- session.ServeCommands(serveContext, func(_ context.Context, _ Command, responder Responder) error {
			handlerEntered <- struct{}{}
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			if evidence == nil {
				return errors.New("accept returned no command evidence")
			}
			acceptedEvidence <- evidence
			return nil
		})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)

	reply, err := sendCommand(context.Background(), core, session.runtimeID, true)
	if err != nil {
		cancelBlock()
		t.Fatalf("command with blocked optional-event sink failed: %v", err)
	}
	response, err := natswire.Decode[CommandResponse](
		compileValidator(t), contractsv1.CommandResponseSchemaID, reply.Data,
	)
	if err != nil {
		cancelBlock()
		t.Fatal(err)
	}
	if response.Data.Status != statusAccepted {
		cancelBlock()
		t.Fatalf("command response = %#v, want accepted", response)
	}
	// The reply publishes before Accept returns, so the response alone cannot
	// prove Accept made progress: wait for the returned evidence itself.
	select {
	case <-handlerEntered:
	case <-time.After(3 * time.Second):
		cancelBlock()
		t.Fatal("command handler did not run while optional-event sink was blocked")
	}
	select {
	case evidence := <-acceptedEvidence:
		if evidence == nil {
			cancelBlock()
			t.Fatal("Accept returned no command evidence while optional-event sink was blocked")
		}
	case <-time.After(3 * time.Second):
		cancelBlock()
		t.Fatal("Accept did not return evidence while optional-event sink was blocked")
	}
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "command.received" || record["event"] == "command.accepted" {
			cancelBlock()
			t.Fatalf("optional event %q returned and risks blocking handler progress", record["event"])
		}
	}

	cancelBlock()
	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

// claimGateLogHandler blocks the session claim announcement until the test
// releases it, proving heartbeat startup does not wait for that log record.
type claimGateLogHandler struct {
	base    slog.Handler
	entered chan struct{}
	release chan struct{}
	once    *sync.Once
}

func (handler *claimGateLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.base.Enabled(ctx, level)
}

func (handler *claimGateLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if logRecordEvent(record) == "adapter.session_claimed" {
		handler.once.Do(func() { close(handler.entered) })
		<-handler.release
	}
	return handler.base.Handle(ctx, record)
}

func (handler *claimGateLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &claimGateLogHandler{
		base: handler.base.WithAttrs(attrs), entered: handler.entered, release: handler.release, once: handler.once,
	}
}

func (handler *claimGateLogHandler) WithGroup(name string) slog.Handler {
	return &claimGateLogHandler{
		base: handler.base.WithGroup(name), entered: handler.entered, release: handler.release, once: handler.once,
	}
}

// testHeartbeatCadence is a positive stand-in for the production
// heartbeatInterval, short enough that a passing run observes the first
// heartbeat immediately. Production callers reach the 5s interval through
// Connect; only tests use the connectWithHeartbeatInterval seam.
const testHeartbeatCadence = 25 * time.Millisecond

// This test protects newly claimed runtime heartbeat startup and fails if the
// session claim log gates the heartbeat goroutine launch.
func TestConnectHeartbeatStartsBeforeClaimAnnouncement(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	startClaimAndReleaseResponders(t, core, validator)
	heartbeatReceived := make(chan struct{}, 1)
	_, err := core.Subscribe(natswire.AdapterHeartbeatWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterHeartbeatRequest](
			validator, contractsv1.AdapterHeartbeatRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			return
		}
		select {
		case heartbeatReceived <- struct{}{}:
		default:
		}
		respondAcceptedHeartbeat(t, validator, message, request)
	})
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	writer := &lockedWriter{}
	base, err := logging.NewApplicationLogger(writer, "hearth-simulator", logging.LogOptions{
		Level: "debug", Format: "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	gate := &claimGateLogHandler{
		base: base.Handler(), entered: make(chan struct{}), release: make(chan struct{}), once: &sync.Once{},
	}
	gatedLogger := slog.New(gate)
	var releaseClaimAnnouncement sync.Once
	releaseGate := func() { releaseClaimAnnouncement.Do(func() { close(gate.release) }) }
	defer releaseGate()
	config := testConfig(server.ClientURL())
	config.Logger = gatedLogger
	var session *Session
	type connectResult struct {
		session *Session
		err     error
	}
	connected := make(chan connectResult, 1)
	connectFinished := false
	connectContext := testContext(t)
	t.Cleanup(func() {
		releaseGate()
		if !connectFinished {
			select {
			case result := <-connected:
				session = result.session
			case <-time.After(3 * time.Second):
				t.Error("Connect did not finish during cleanup")
				return
			}
		}
		if session != nil {
			_ = session.Close()
		}
	})
	go func() {
		connectedSession, connectErr := connectWithHeartbeatInterval(
			connectContext, config, testHeartbeatCadence,
		)
		connected <- connectResult{session: connectedSession, err: connectErr}
	}()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("session claim announcement did not start")
	}
	// The claim log stays blocked here: a heartbeat proves the loop started first.
	select {
	case <-heartbeatReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("heartbeat did not start while session claim announcement was blocked")
	}
	releaseGate()
	select {
	case result := <-connected:
		connectFinished = true
		if result.err != nil {
			t.Fatal(result.err)
		}
		session = result.session
	case <-time.After(3 * time.Second):
		t.Fatal("Connect did not return after releasing the claim announcement")
	}
	claimed := waitForLogRecord(t, writer, "adapter.session_claimed", nil, 3*time.Second)
	if claimed["runtime_id"] != session.runtimeID {
		t.Fatalf("claimed runtime = %v, want %q", claimed["runtime_id"], session.runtimeID)
	}
	requireStringID(t, claimed, "correlation_id")
	requireHeartbeatCadence(t, session, testHeartbeatCadence)
}

// spanCapturingLogHandler records the trace context of each log record so a
// test can prove which context reached the observation published evidence.
type spanCapturingLogHandler struct {
	base   slog.Handler
	shared *spanCaptureShared
}

type spanCaptureShared struct {
	mutex sync.Mutex
	spans map[string]trace.SpanContext
}

func (handler *spanCapturingLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.base.Enabled(ctx, level)
}

func (handler *spanCapturingLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if event := logRecordEvent(record); event != "" {
		handler.shared.mutex.Lock()
		if handler.shared.spans == nil {
			handler.shared.spans = make(map[string]trace.SpanContext)
		}
		handler.shared.spans[event] = trace.SpanContextFromContext(ctx)
		handler.shared.mutex.Unlock()
	}
	return handler.base.Handle(ctx, record)
}

func (handler *spanCapturingLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &spanCapturingLogHandler{base: handler.base.WithAttrs(attrs), shared: handler.shared}
}

func (handler *spanCapturingLogHandler) WithGroup(name string) slog.Handler {
	return &spanCapturingLogHandler{base: handler.base.WithGroup(name), shared: handler.shared}
}

func (handler *spanCapturingLogHandler) spanFor(event string) (trace.SpanContext, bool) {
	handler.shared.mutex.Lock()
	defer handler.shared.mutex.Unlock()
	span, ok := handler.shared.spans[event]
	return span, ok
}

// This test protects the restored command span on the observation published
// evidence and fails if the caller context is logged instead of the
// publication context carrying the restored span.
func TestLoggingObservationPublishedCarriesRestoredCommandSpan(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	base, err := logging.NewApplicationLogger(writer, "hearth-simulator", logging.LogOptions{
		Level: "debug", Format: "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	capture := &spanCapturingLogHandler{base: base.Handler(), shared: &spanCaptureShared{}}
	capturingLogger := slog.New(capture)
	session := connectSessionWithLogger(t, server.ClientURL(), capturingLogger)
	publisher := &sequenceJetStreamPublisher{}
	session.jetstream = publisher
	commandSpan := sampleSpanContext()
	callerSpan := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		SpanID:     trace.SpanID{8, 7, 6, 5, 4, 3, 2, 1},
		TraceFlags: trace.FlagsSampled,
	})
	responder := &commandResponder{
		context:       trace.ContextWithSpanContext(context.Background(), commandSpan),
		session:       session,
		connection:    session.connection,
		replySubject:  "_INBOX.restored-span",
		validator:     compileValidator(t),
		commandID:     mustID(t, "cmd"),
		correlationID: mustID(t, "cor"),
		entityID:      testEntityID,
		deadline:      time.Now().Add(time.Second),
	}
	evidence, err := responder.Accept()
	if err != nil {
		t.Fatal(err)
	}
	callerContext := trace.ContextWithSpanContext(context.Background(), callerSpan)
	if _, publishErr := evidence.PublishObservation(callerContext, Observation{
		EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	loggedSpan, ok := capture.spanFor("observation.published")
	if !ok {
		t.Fatal("observation.published was not logged")
	}
	if loggedSpan.TraceID() != commandSpan.TraceID() {
		t.Fatalf("published log trace = %s, want restored command trace %s",
			loggedSpan.TraceID(), commandSpan.TraceID())
	}
}
