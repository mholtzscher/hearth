package adapter //nolint:testpackage // Tests exercise package-private logging behavior.

// This test protects the D3 SDK logging contract: scoped IDs without
// duplicate keys, acknowledged health transitions, fencing/release evidence,
// command lifecycle events, and payload safety. It fails if an emission site
// drops a required ID, duplicates a root key, logs remote error text, or
// emits repeated heartbeats at Info.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/platform/logging"
)

// lockedWriter is a concurrency-safe slog output. Tests must never read a
// [bytes.Buffer] while SDK goroutines write to it.
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

func recordsWithEvent(records []map[string]any, event string) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["event"] == event {
			matched = append(matched, record)
		}
	}
	return matched
}

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

func waitForLogEvent(
	t *testing.T,
	writer *lockedWriter,
	event string,
	timeout time.Duration,
) map[string]any {
	t.Helper()
	return waitForLogRecord(t, writer, event, func(map[string]any) bool { return true }, timeout)
}

func countLogRecords(t *testing.T, writer *lockedWriter, event string) int {
	t.Helper()
	return len(recordsWithEvent(parseLogRecords(t, writer.snapshot()), event))
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

// This test protects scoped session identity and fails if the SDK drops the
// runtime ID, reattaches app/pid/component, or emits a record twice for one key.
func TestSessionClaimedCarriesScopedIDsWithoutDuplicateKeys(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	claimed := waitForLogEvent(t, writer, "adapter.session_claimed", 3*time.Second)
	if claimed["adapter_id"] != "simulator" {
		t.Errorf("adapter_id = %v, want simulator", claimed["adapter_id"])
	}
	if claimed["runtime_id"] != session.runtimeID || claimed["runtime_id"] == "" {
		t.Errorf("runtime_id = %v, want claimed session %q", claimed["runtime_id"], session.runtimeID)
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

	for _, key := range []string{`"app":`, `"pid":`, `"component":`, `"adapter_id":`, `"event":`} {
		for line := range strings.SplitSeq(strings.TrimSpace(writer.snapshot()), "\n") {
			if strings.Count(line, key) != 1 {
				t.Errorf("log line has %d occurrences of %s, want 1:\n%s",
					strings.Count(line, key), key, line)
			}
		}
	}
	// Runtime identity labels only the claimed session, so pre-claim records
	// omit it while claimed records carry it exactly once.
	for line := range strings.SplitSeq(strings.TrimSpace(writer.snapshot()), "\n") {
		if got := strings.Count(line, `"runtime_id":`); got > 1 {
			t.Errorf("log line has %d occurrences of runtime_id, want at most 1:\n%s", got, line)
		}
	}
}

// This test protects claim retry evidence and fails if a blocked claim stays
// silent, floods Warn, or never reports recovery before the claim milestone.
func TestSessionClaimRetryEpisode(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	var attempts int
	_, err := core.Subscribe(natswire.AdapterClaimWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterClaimRequest](
			validator, contractsv1.AdapterClaimRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode claim: %v", decodeErr)
			return
		}
		attempts++
		if attempts == 1 {
			return
		}
		respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
			contractsv1.AdapterClaimResponseSchemaID, adapterClaimResponse{Status: statusAccepted})
	})
	if err != nil {
		t.Fatal(err)
	}
	startHeartbeatAndReleaseResponders(t, core, validator)
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	writer := &lockedWriter{}
	config := testConfig(server.ClientURL())
	config.Logger = recordingLogger(t, writer)
	session, err := Connect(testContext(t), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	retrying := waitForLogRecord(t, writer, "dependency.retrying", func(record map[string]any) bool {
		return record["operation"] == "session_claim"
	}, 5*time.Second)
	if retrying["level"] != "WARN" {
		t.Errorf("first claim retry level = %v, want WARN", retrying["level"])
	}
	if retrying["attempt"] != float64(1) || retrying["error_code"] != "request_timeout" {
		t.Errorf("claim retry = attempt %v code %v, want 1/request_timeout",
			retrying["attempt"], retrying["error_code"])
	}
	recovered := waitForLogRecord(t, writer, "dependency.recovered", func(record map[string]any) bool {
		return record["operation"] == "session_claim"
	}, 5*time.Second)
	if recovered["level"] != "INFO" {
		t.Errorf("claim recovery level = %v, want INFO", recovered["level"])
	}
	claimed := waitForLogEvent(t, writer, "adapter.session_claimed", 5*time.Second)
	if claimed["correlation_id"] == nil || claimed["correlation_id"] == "" {
		t.Errorf("claimed correlation_id = %v, want claim request correlation", claimed["correlation_id"])
	}

	output := writer.snapshot()
	retryIndex := strings.Index(output, `"event":"dependency.retrying"`)
	recoveredIndex := strings.Index(output, `"event":"dependency.recovered"`)
	claimedIndex := strings.Index(output, `"event":"adapter.session_claimed"`)
	ordered := retryIndex != -1 && recoveredIndex != -1 && claimedIndex != -1 &&
		retryIndex < recoveredIndex && recoveredIndex < claimedIndex
	if !ordered {
		t.Errorf("retry/recovery/claim records out of order in output:\n%s", output)
	}
	if got := countLogRecords(t, writer, "dependency.recovered"); got != 1 {
		t.Errorf("session_claim recoveries = %d, want 1", got)
	}
}

// This test protects registration completion evidence and fails if the SDK
// drops the bounded summary or the per-mapping Debug records.
// startRegistrationResponder serves accepted test bindings and one rejected
// binding whose message carries a sentinel that must never reach logs.
func startRegistrationResponder(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
) {
	t.Helper()
	_, err := connection.Subscribe(natswire.RegistrationWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[Registration](
			validator, contractsv1.RegistrationRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode registration request: %v", decodeErr)
			return
		}
		causationID := request.ID
		response := natswire.Envelope[RegistrationResponse]{
			ID: mustID(t, "rep"), Schema: contractsv1.RegistrationResponseSchemaID,
			EmittedAt: nowString(), CorrelationID: request.CorrelationID, CausationID: &causationID,
		}
		if request.Data.BindingKey == "rejected-light" {
			response.Data = RegistrationResponse{Status: "rejected", Error: &RegistrationError{
				Code: "identity_conflict", Message: "SENTINEL-binding-owned-by-another-adapter",
			}}
		} else {
			response.Data = RegistrationResponse{Status: "accepted", Binding: &Binding{
				BindingKey: request.Data.BindingKey,
				DeviceID:   testDeviceID,
				Entities:   []EntityBinding{{Key: "power", EntityID: testEntityID, Enabled: true}},
			}}
		}
		payload, encodeErr := natswire.Encode(
			validator, contractsv1.RegistrationResponseSchemaID, response,
		)
		if encodeErr != nil {
			t.Errorf("encode registration response: %v", encodeErr)
			return
		}
		if respondErr := message.Respond(payload); respondErr != nil {
			t.Errorf("respond to registration: %v", respondErr)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := connection.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
}

// This test protects registration completion evidence and fails if the SDK
// drops the bounded summary or the per-mapping Debug records.
func TestRegistrationCompletedLogging(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	startRegistrationResponder(t, core, compileValidator(t))

	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	binding, err := session.Register(testContext(t), validRegistration("office-light"))
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForLogEvent(t, writer, "adapter.registration_completed", 3*time.Second)
	if completed["device_id"] != binding.DeviceID || completed["entity_count"] != float64(1) {
		t.Errorf("registration summary = %v/%v, want %s/1",
			completed["device_id"], completed["entity_count"], binding.DeviceID)
	}
	if completed["entity_id"] != testEntityID {
		t.Errorf("single-entity summary entity_id = %v, want %s", completed["entity_id"], testEntityID)
	}
	if completed["correlation_id"] == nil || completed["correlation_id"] == "" {
		t.Errorf("registration summary correlation_id = %v, want request correlation",
			completed["correlation_id"])
	}
	mapping := waitForLogEvent(t, writer, "adapter.registration_mapping", 3*time.Second)
	if mapping["level"] != "DEBUG" {
		t.Errorf("registration mapping level = %v, want DEBUG", mapping["level"])
	}
	if mapping["device_id"] != testDeviceID || mapping["entity_id"] != testEntityID ||
		mapping["entity_key"] != "power" {
		t.Errorf("registration mapping = %v", mapping)
	}
}

// This test protects registration rejection evidence and fails if the SDK
// drops the typed rejection code or leaks the Core rejection message.
func TestRegistrationRejectedLogging(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	startRegistrationResponder(t, core, compileValidator(t))

	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	if _, registerErr := session.Register(
		testContext(t), validRegistration("rejected-light"),
	); registerErr == nil {
		t.Fatal("rejected registration unexpectedly succeeded")
	}
	rejected := waitForLogEvent(t, writer, "adapter.registration_rejected", 3*time.Second)
	if rejected["level"] != "WARN" || rejected["rejection_code"] != "identity_conflict" {
		t.Errorf("registration rejection = %v, want WARN/identity_conflict", rejected)
	}
	if strings.Contains(writer.snapshot(), "SENTINEL-binding-owned-by-another-adapter") {
		t.Error("core rejection message leaked into logs")
	}
}

// This test protects acknowledged health transitions and fails if a repeated
// heartbeat logs at Info, a status change is missed, or an unhealthy report
// is not Warn.
func TestHealthReportedTransitions(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	healthy := HealthReport{Status: HealthHealthy, SourceObservedAt: time.Now().UTC()}
	if err := session.SetHealth(testContext(t), healthy); err != nil {
		t.Fatal(err)
	}
	waitForLogRecord(t, writer, "adapter.health_reported", func(record map[string]any) bool {
		return record["status"] == "healthy"
	}, 3*time.Second)
	healthyCount := countLogRecords(t, writer, "adapter.health_reported")

	if err := session.SetHealth(testContext(t), healthy); err != nil {
		t.Fatal(err)
	}
	if got := countLogRecords(t, writer, "adapter.health_reported"); got != healthyCount {
		t.Errorf("repeated healthy heartbeat emitted health_reported (%d -> %d), want silence",
			healthyCount, got)
	}

	unhealthy := HealthReport{
		Status: HealthUnhealthy, SourceObservedAt: time.Now().UTC(),
		ReasonCode: "hearth.network_unreachable",
	}
	if err := session.SetHealth(testContext(t), unhealthy); err != nil {
		t.Fatal(err)
	}
	unhealthyRecord := waitForLogRecord(t, writer, "adapter.health_reported",
		func(record map[string]any) bool { return record["status"] == "unhealthy" }, 3*time.Second)
	if unhealthyRecord["level"] != "WARN" {
		t.Errorf("unhealthy report level = %v, want WARN", unhealthyRecord["level"])
	}
	if unhealthyRecord["reason_code"] != "hearth.network_unreachable" {
		t.Errorf("unhealthy reason_code = %v, want hearth.network_unreachable",
			unhealthyRecord["reason_code"])
	}
	unhealthyCount := countLogRecords(t, writer, "adapter.health_reported")
	if err := session.SetHealth(testContext(t), unhealthy); err != nil {
		t.Fatal(err)
	}
	if got := countLogRecords(t, writer, "adapter.health_reported"); got != unhealthyCount {
		t.Errorf("repeated unhealthy heartbeat emitted health_reported (%d -> %d), want silence",
			unhealthyCount, got)
	}
}

// This test protects fencing evidence and fails if concurrent fencing paths
// emit duplicate records or omit runtime identity and code.
func TestSessionFencedLogsOnce(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	session.markFenced(testContext(t))
	session.markFenced(context.Background())

	fenced := waitForLogEvent(t, writer, "adapter.session_fenced", 3*time.Second)
	if fenced["level"] != "WARN" {
		t.Errorf("fencing level = %v, want WARN", fenced["level"])
	}
	if fenced["runtime_id"] != session.runtimeID || fenced["reason_code"] != "runtime_fenced" {
		t.Errorf("fencing record = %v, want runtime %q and runtime_fenced",
			fenced, session.runtimeID)
	}
	// The fenced Close path is expected teardown: no unexpected-close Error may follow.
	time.Sleep(200 * time.Millisecond)
	if got := countLogRecords(t, writer, "adapter.session_fenced"); got != 1 {
		t.Errorf("session_fenced records = %d, want 1", got)
	}
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "dependency.closed" && record["level"] == "ERROR" {
			t.Errorf("expected fenced close emitted unexpected-close error: %v", record)
		}
	}
}

// This test protects release evidence and fails if graceful release stays
// silent or a failed release omits its Warn diagnostic.
func TestSessionReleaseLogging(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	released := waitForLogEvent(t, writer, "adapter.session_released", 3*time.Second)
	if released["level"] != "INFO" {
		t.Errorf("release level = %v, want INFO", released["level"])
	}
	if released["runtime_id"] != session.runtimeID {
		t.Errorf("release runtime_id = %v, want %q", released["runtime_id"], session.runtimeID)
	}
}

func TestSessionReleaseFailureLogsWarning(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	_, err := core.Subscribe(natswire.AdapterClaimWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterClaimRequest](
			validator, contractsv1.AdapterClaimRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode claim: %v", decodeErr)
			return
		}
		respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
			contractsv1.AdapterClaimResponseSchemaID, adapterClaimResponse{Status: statusAccepted})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.Subscribe(natswire.AdapterHeartbeatWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[adapterHeartbeatRequest](
			validator, contractsv1.AdapterHeartbeatRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode heartbeat: %v", decodeErr)
			return
		}
		respondAcceptedHeartbeat(t, validator, message, request)
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseSubscription, err := core.Subscribe(
		natswire.AdapterReleaseWildcard(),
		func(message *natsgo.Msg) {
			request, decodeErr := natswire.Decode[adapterReleaseRequest](
				validator, contractsv1.AdapterReleaseRequestSchemaID, message.Data,
			)
			if decodeErr != nil {
				t.Errorf("decode release: %v", decodeErr)
				return
			}
			respondTestLifecycle(t, validator, message, request.ID, request.CorrelationID,
				contractsv1.AdapterReleaseResponseSchemaID,
				adapterReleaseResponse{Status: statusAccepted})
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	writer := &lockedWriter{}
	config := testConfig(server.ClientURL())
	config.Logger = recordingLogger(t, writer)
	session, err := Connect(testContext(t), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// With no release responder, graceful release exhausts its retry budget
	// waiting for Core instead of succeeding silently.
	if unsubscribeErr := releaseSubscription.Unsubscribe(); unsubscribeErr != nil {
		t.Fatal(unsubscribeErr)
	}
	// Ensure the server processed the unsubscribe before Close sends release.
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	// Graceful release failure is Warn evidence only: Close still reports the
	// drain outcome, preserving existing return semantics.
	if closeErr := session.Close(); closeErr != nil {
		t.Fatalf("Close with unacknowledged release = %v, want drain success", closeErr)
	}
	failed := waitForLogEvent(t, writer, "adapter.session_release_failed", 15*time.Second)
	if failed["level"] != "WARN" {
		t.Errorf("release failure level = %v, want WARN", failed["level"])
	}
	if failed["error_code"] != "request_timeout" {
		t.Errorf("release failure error_code = %v, want request_timeout", failed["error_code"])
	}
	if got := countLogRecords(t, writer, "adapter.session_release_failed"); got != 1 {
		t.Errorf("session_release_failed records = %d, want 1", got)
	}
}

// This test protects command lifecycle evidence and fails if receipt,
// acceptance, or typed rejection is missing, mismatches IDs, or leaks the
// upstream rejection message.
func TestCommandLifecycleLogging(t *testing.T) {
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
			func(_ context.Context, command Command, responder Responder) error {
				if string(command.Parameters) == `{"value":false}` {
					return responder.RejectUnavailable("SENTINEL-entity-down-for-maintenance")
				}
				_, err := responder.Accept()
				return err
			})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)
	waitForLogEvent(t, writer, "adapter.commands_listening", 3*time.Second)

	acceptedReply, err := sendCommand(context.Background(), core, session.runtimeID, true)
	if err != nil {
		t.Fatal(err)
	}
	validator := compileValidator(t)
	acceptedResponse, err := natswire.Decode[CommandResponse](
		validator, contractsv1.CommandResponseSchemaID, acceptedReply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	received := waitForLogRecord(t, writer, "command.received", func(record map[string]any) bool {
		return record["command_id"] == acceptedResponse.Data.CommandID
	}, 3*time.Second)
	if received["entity_id"] != testEntityID || received["operation"] != "set" {
		t.Errorf("command received = %v, want entity %s operation set", received, testEntityID)
	}
	if received["correlation_id"] != acceptedResponse.CorrelationID {
		t.Errorf("received correlation = %v, want %s",
			received["correlation_id"], acceptedResponse.CorrelationID)
	}
	accepted := waitForLogRecord(t, writer, "command.accepted", func(record map[string]any) bool {
		return record["command_id"] == acceptedResponse.Data.CommandID
	}, 3*time.Second)
	if accepted["correlation_id"] != acceptedResponse.CorrelationID ||
		accepted["entity_id"] != testEntityID {
		t.Errorf("command accepted = %v", accepted)
	}

	rejectedReply, err := sendCommand(context.Background(), core, session.runtimeID, false)
	if err != nil {
		t.Fatal(err)
	}
	rejectedResponse, err := natswire.Decode[CommandResponse](
		validator, contractsv1.CommandResponseSchemaID, rejectedReply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	rejected := waitForLogRecord(t, writer, "command.rejected", func(record map[string]any) bool {
		return record["command_id"] == rejectedResponse.Data.CommandID
	}, 3*time.Second)
	if rejected["level"] != "WARN" || rejected["rejection_code"] != "entity_unavailable" {
		t.Errorf("command rejected = %v, want WARN/entity_unavailable", rejected)
	}
	if strings.Contains(writer.snapshot(), "SENTINEL-entity-down-for-maintenance") {
		t.Error("upstream rejection message leaked into logs")
	}

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

// This test protects discard diagnostics and fails if invalid input is silent
// or echoes raw subjects, payloads, or errors.
func TestCommandDiscardedLogging(t *testing.T) {
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

	// Expired deadline: the request itself times out because no reply is sent.
	_, err := sendCommandWithDeadline(
		context.Background(), core, session.runtimeID, true,
		300*time.Millisecond, time.Now().UTC().Add(-time.Second),
	)
	if err == nil {
		t.Fatal("expired command unexpectedly received a reply")
	}
	expired := waitForLogRecord(t, writer, "command.discarded", func(record map[string]any) bool {
		return record["error_code"] == "command_expired"
	}, 3*time.Second)
	if expired["level"] != "WARN" || expired["command_id"] == nil || expired["command_id"] == "" {
		t.Errorf("expired discard = %v, want WARN with command_id", expired)
	}

	// Missing reply subject.
	validator := compileValidator(t)
	commandID, err := newID("cmd")
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.CommandSubject("simulator", session.runtimeID, testEntityID, "set")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(validator, contractsv1.CommandRequestSchemaID,
		natswire.Envelope[Command]{
			ID: commandID, Schema: contractsv1.CommandRequestSchemaID, EmittedAt: nowString(),
			CorrelationID: mustID(t, "cor"),
			Data: Command{
				EntityID:      testEntityID,
				OperationName: "set",
				Parameters:    json.RawMessage(`{"value":true}`),
				Deadline:      time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano),
			},
		})
	if err != nil {
		t.Fatal(err)
	}
	if publishErr := core.Publish(subject, payload); publishErr != nil {
		t.Fatal(publishErr)
	}
	waitForLogRecord(t, writer, "command.discarded", func(record map[string]any) bool {
		return record["error_code"] == "missing_reply_subject"
	}, 3*time.Second)

	// Malicious wire body must not appear in logs at any level.
	maliciousSubject, err := natswire.CommandSubject(
		"simulator", session.runtimeID, testEntityID, "set",
	)
	if err != nil {
		t.Fatal(err)
	}
	inbox := natsgo.NewInbox()
	reply, err := core.SubscribeSync(inbox)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reply.Unsubscribe() }()
	malicious := `{"SENTINEL-malicious-payload": "<script>alert(1)</script>"}`
	if publishErr := core.PublishRequest(maliciousSubject, inbox, []byte(malicious)); publishErr != nil {
		t.Fatal(publishErr)
	}
	waitForLogRecord(t, writer, "command.discarded", func(record map[string]any) bool {
		return record["error_code"] == "invalid_envelope"
	}, 3*time.Second)

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

// This test protects observation publication evidence and fails if an acked
// publication stays silent or a command-linked publication drops its IDs.
func TestObservationPublishedLogging(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	stream := createObservationStream(t, core)
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	ordinaryID, err := session.PublishObservation(testContext(t), Observation{
		EntityID: testEntityID, Value: json.RawMessage(`true`), AdapterReceivedAt: nowString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ordinary := waitForLogRecord(t, writer, "observation.published",
		func(record map[string]any) bool {
			return record["observation_id"] == string(ordinaryID)
		}, 3*time.Second)
	if ordinary["level"] != "DEBUG" || ordinary["entity_id"] != testEntityID {
		t.Errorf("ordinary publication = %v, want DEBUG for %s", ordinary, testEntityID)
	}
	if ordinary["command_id"] != nil {
		t.Errorf("ordinary publication carries command_id: %v", ordinary)
	}

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext,
			func(ctx context.Context, _ Command, responder Responder) error {
				evidence, acceptErr := responder.Accept()
				if acceptErr != nil {
					return acceptErr
				}
				_, publishErr := evidence.PublishObservation(ctx, Observation{
					EntityID: testEntityID, Value: json.RawMessage(`true`),
					AdapterReceivedAt: nowString(),
				})
				return publishErr
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
	if _, streamErr := stream.GetMsg(testContext(t), 2); streamErr != nil {
		t.Fatal(streamErr)
	}
	linked := waitForLogRecord(t, writer, "observation.published",
		func(record map[string]any) bool {
			return record["command_id"] == response.Data.CommandID
		}, 3*time.Second)
	if linked["correlation_id"] != response.CorrelationID || linked["entity_id"] != testEntityID {
		t.Errorf("linked publication = %v", linked)
	}

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

// This test protects blocked availability evidence and fails if a stalled
// acknowledgement stays silent, floods, or logs a success milestone at Info.
func TestAvailabilityBlockedRetryRecovery(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	validator := compileValidator(t)
	var attempts int
	if _, err := core.Subscribe(natswire.EntityAvailabilityWildcard(), func(message *natsgo.Msg) {
		request, decodeErr := natswire.Decode[entityAvailabilityRequest](
			validator, contractsv1.EntityAvailabilityRequestSchemaID, message.Data,
		)
		if decodeErr != nil {
			t.Errorf("decode availability request: %v", decodeErr)
			return
		}
		attempts++
		if attempts == 1 {
			return
		}
		respondAvailability(t, validator, message, request, entityAvailabilityResponse{
			Status: statusAccepted, ReportedAt: nowString(), Count: len(request.Data.Entities),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if flushErr := core.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))
	report := EntityAvailabilityReport{
		EntityID: testEntityID, Status: AvailabilityAvailable, SourceObservedAt: time.Now().UTC(),
	}
	if err := session.ReportEntityAvailability(testContext(t), []EntityAvailabilityReport{report}); err != nil {
		t.Fatal(err)
	}

	retrying := waitForLogRecord(t, writer, "dependency.retrying", func(record map[string]any) bool {
		return record["operation"] == "entity_availability"
	}, 5*time.Second)
	if retrying["level"] != "WARN" || retrying["attempt"] != float64(1) {
		t.Errorf("blocked availability retry = %v, want WARN attempt 1", retrying)
	}
	recovered := waitForLogRecord(t, writer, "dependency.recovered", func(record map[string]any) bool {
		return record["operation"] == "entity_availability"
	}, 5*time.Second)
	if recovered["level"] != "INFO" || recovered["duration_ms"] == nil {
		t.Errorf("availability recovery = %v, want INFO with duration", recovered)
	}
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		event, _ := record["event"].(string)
		if strings.HasPrefix(event, "adapter.") && event != "adapter.session_claimed" {
			t.Errorf("acknowledged availability emitted unexpected adapter event: %v", record)
		}
	}
}

// This test protects connection lifecycle evidence and fails if NATS loss or
// recovery stays silent, or if reconnect falsely claims Adapter health.
func TestConnectionCallbacksLogging(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	server := startServer(t, -1, storeDir)
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))
	port := server.Addr().(*net.TCPAddr).Port
	waitForLogEvent(t, writer, "dependency.connected", 3*time.Second)

	server.Shutdown()
	server.WaitForShutdown()
	waitForConnectionStatus(t, session.connection, natsgo.RECONNECTING)
	disconnected := waitForLogEvent(t, writer, "dependency.disconnected", 5*time.Second)
	if disconnected["level"] != "WARN" || disconnected["dependency"] != "nats" {
		t.Errorf("disconnect record = %v, want WARN dependency nats", disconnected)
	}

	restarted := startServer(t, port, storeDir)
	defer func() {
		restarted.Shutdown()
		restarted.WaitForShutdown()
	}()
	waitForConnectionStatus(t, session.connection, natsgo.CONNECTED)
	reconnected := waitForLogEvent(t, writer, "dependency.reconnected", 5*time.Second)
	if reconnected["level"] != "INFO" {
		t.Errorf("reconnect level = %v, want INFO", reconnected["level"])
	}
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "adapter.health_reported" && record["status"] == "healthy" {
			t.Errorf("transport reconnect falsely reported healthy Adapter: %v", record)
		}
		if url, ok := record["nats_url"]; ok {
			t.Errorf("connection record carries configured URL: %v (%v)", record, url)
		}
	}

	// Close against the live restarted server so release is acknowledged.
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

// This test protects the retry noise policy and fails if identical failures
// Warn repeatedly, a new failure class stays at Debug, or an unretried
// operation emits recovery.
func TestRetryEpisodeLevels(t *testing.T) {
	t.Parallel()
	writer := &lockedWriter{}
	logger := recordingLogger(t, writer)
	ctx := context.Background()

	episode := newRetryEpisode("registration", "core")
	if err := episode.waitRetry(ctx, logger, slog.String("error_code", "no_responders"),
		10*time.Millisecond, natsgo.ErrNoResponders); err != nil {
		t.Fatal(err)
	}
	if err := episode.waitRetry(ctx, logger, slog.String("error_code", "no_responders"),
		10*time.Millisecond, natsgo.ErrNoResponders); err != nil {
		t.Fatal(err)
	}
	if err := episode.waitRetry(ctx, logger, slog.String("error_code", "nats_disconnected"),
		10*time.Millisecond, natsgo.ErrDisconnected); err != nil {
		t.Fatal(err)
	}
	episode.succeeded(ctx, logger)
	newRetryEpisode("heartbeat", "core").succeeded(ctx, logger)

	var retrying, recovered []map[string]any
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		switch record["event"] {
		case "dependency.retrying":
			retrying = append(retrying, record)
		case "dependency.recovered":
			recovered = append(recovered, record)
		}
	}
	if len(retrying) != 3 {
		t.Fatalf("retrying records = %d, want 3", len(retrying))
	}
	if retrying[0]["level"] != "WARN" || retrying[0]["attempt"] != float64(1) {
		t.Errorf("first identical failure = %v, want WARN attempt 1", retrying[0])
	}
	if retrying[1]["level"] != "DEBUG" || retrying[1]["attempt"] != float64(2) {
		t.Errorf("second identical failure = %v, want DEBUG attempt 2", retrying[1])
	}
	if retrying[2]["level"] != "WARN" || retrying[2]["attempt"] != float64(3) {
		t.Errorf("distinct failure class = %v, want WARN attempt 3", retrying[2])
	}
	if len(recovered) != 1 || recovered[0]["level"] != "INFO" ||
		recovered[0]["attempt"] != float64(4) {
		t.Errorf("recovery records = %v, want one INFO attempt 4", recovered)
	}
	if recovered[0]["duration_ms"] == nil {
		t.Errorf("recovery record omits duration: %v", recovered[0])
	}
}

// lifecycleContextKey carries a test value through Connect so the release
// path can prove it preserves incoming context instead of substituting a
// background context.
type lifecycleContextKey struct{}

// contextValueHandler exposes one context value as a log attribute so tests
// can observe which context an emission site actually used.
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

// WithAttrs and WithGroup preserve the wrapper: session scopes its logger
// with runtime identity, and a bare embedded Handler would drop the
// context-value observation on every scoped record.
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

func requireStringID(t *testing.T, record map[string]any, key string) {
	t.Helper()
	value, ok := record[key].(string)
	if !ok || value == "" {
		t.Errorf("record %q = %v, want non-empty string", key, record[key])
	}
}

// This test protects lifecycle context preservation through release and
// fails if release substitutes a background context that drops incoming
// values while keeping the existing timeout semantics.
func TestReleasePreservesLifecycleContextValues(t *testing.T) {
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

	claimed := waitForLogEvent(t, writer, "adapter.session_claimed", 3*time.Second)
	if claimed["test_ctx_value"] != "lifecycle-value" {
		t.Fatalf("claim record misses lifecycle value, harness cannot observe context: %v", claimed)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	released := waitForLogEvent(t, writer, "adapter.session_released", 3*time.Second)
	if released["test_ctx_value"] != "lifecycle-value" {
		t.Errorf("release record = %v, want lifecycle context value preserved", released)
	}
	requireStringID(t, released, "runtime_id")
}

// This test protects expected-close classification and fails if a lifecycle
// cancellation before Close is reported as an unexpected terminal close.
func TestClosedAfterLifecycleCancelIsExpected(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	// Cancel the lifecycle without calling Close: the resulting connection
	// close is teardown, not a new unexpected failure.
	session.markClosed()
	closed := waitForLogEvent(t, writer, "dependency.closed", 3*time.Second)
	if closed["level"] != "DEBUG" {
		t.Errorf("closed level after lifecycle cancel = %v, want DEBUG", closed["level"])
	}
	if closed["dependency"] != "nats" {
		t.Errorf("closed dependency = %v, want nats", closed["dependency"])
	}
	time.Sleep(200 * time.Millisecond)
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "dependency.closed" && record["level"] == "ERROR" {
			t.Errorf("lifecycle-cancelled close emitted unexpected-close error: %v", record)
		}
	}
}

// This test protects accepted-handler failure evidence and fails if an
// accepted command whose handler then fails stays silent or leaks error text.
func TestAcceptedHandlerFailureIsDiagnosed(t *testing.T) {
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
	waitForLogEvent(t, writer, "adapter.commands_listening", 3*time.Second)

	acceptedReply, err := sendCommand(context.Background(), core, session.runtimeID, true)
	if err != nil {
		t.Fatal(err)
	}
	acceptedResponse, err := natswire.Decode[CommandResponse](
		compileValidator(t), contractsv1.CommandResponseSchemaID, acceptedReply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	accepted := waitForLogRecord(t, writer, "command.accepted", func(record map[string]any) bool {
		return record["command_id"] == acceptedResponse.Data.CommandID
	}, 3*time.Second)
	requireStringID(t, accepted, "command_id")
	requireStringID(t, accepted, "correlation_id")
	requireStringID(t, accepted, "entity_id")

	// An accepted command whose handler then fails must emit a safe Error
	// diagnostic without leaking the handler error text.
	failed := waitForLogRecord(t, writer, "command.handler_failed", func(record map[string]any) bool {
		return record["command_id"] == acceptedResponse.Data.CommandID
	}, 3*time.Second)
	assertHandlerFailure(t, writer, failed, acceptedResponse)

	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
}

func assertHandlerFailure(
	t *testing.T,
	writer *lockedWriter,
	failed map[string]any,
	response natswire.Envelope[CommandResponse],
) {
	t.Helper()
	if failed["level"] != "ERROR" {
		t.Errorf("accepted-handler failure level = %v, want ERROR", failed["level"])
	}
	if failed["error_code"] != "handler_failed" {
		t.Errorf("accepted-handler failure error_code = %v, want handler_failed", failed["error_code"])
	}
	if failed["entity_id"] != testEntityID {
		t.Errorf("accepted-handler failure entity_id = %v, want %s", failed["entity_id"], testEntityID)
	}
	if failed["correlation_id"] != response.CorrelationID {
		t.Errorf("accepted-handler failure correlation = %v, want %s",
			failed["correlation_id"], response.CorrelationID)
	}
	if strings.Contains(writer.snapshot(), "post-acceptance bookkeeping failed") {
		t.Error("handler error text leaked into logs")
	}
}

// This test protects expected-rejection silence and fails if a successfully
// published rejection is additionally logged at Error merely because the
// handler returned an error.
func TestRejectedHandlerErrorIsNotError(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	handlerDone := make(chan struct{})
	var handlerOnce sync.Once
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext,
			func(_ context.Context, _ Command, responder Responder) error {
				defer handlerOnce.Do(func() { close(handlerDone) })
				if rejectErr := responder.RejectUnavailable("SENTINEL-entity-down"); rejectErr != nil {
					return rejectErr
				}
				return errors.New("post-rejection bookkeeping failed")
			})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)
	waitForLogEvent(t, writer, "adapter.commands_listening", 3*time.Second)

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
	if rejected["rejection_code"] != "entity_unavailable" {
		t.Errorf("command rejected = %v, want entity_unavailable", rejected)
	}
	requireStringID(t, rejected, "command_id")
	requireStringID(t, rejected, "correlation_id")
	requireStringID(t, rejected, "entity_id")
	if strings.Contains(writer.snapshot(), "SENTINEL-entity-down") {
		t.Error("upstream rejection message leaked into logs")
	}

	// Handler completion plus session release proves the rejected command
	// finished processing: Close waits for in-flight handlers, and the
	// release record is emitted after their diagnostics. Silence after that
	// point means the rejection is the complete record.
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("command handler did not return after rejection")
	}
	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	waitForLogEvent(t, writer, "adapter.session_released", 3*time.Second)
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "command.handler_failed" {
			t.Errorf("rejected command emitted internal failure: %v", record)
		}
		if record["level"] == "ERROR" {
			t.Errorf("post-rejection handler error emitted Error record: %v", record)
		}
	}
	if strings.Contains(writer.snapshot(), "post-rejection bookkeeping failed") {
		t.Error("handler error text leaked into logs")
	}
}

// This test protects cancellation after acceptance and fails if a handler
// error caused by command cancellation is reported as an internal Error.
func TestAcceptedHandlerCancellationIsNotError(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	core := connectNATS(t, server.ClientURL())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	serveContext, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	serveDone := make(chan error, 1)
	handlerDone := make(chan struct{})
	var handlerOnce sync.Once
	subscriptions := server.NumSubscriptions()
	go func() {
		serveDone <- session.ServeCommands(serveContext,
			func(ctx context.Context, _ Command, responder Responder) error {
				defer handlerOnce.Do(func() { close(handlerDone) })
				if _, acceptErr := responder.Accept(); acceptErr != nil {
					return acceptErr
				}
				<-ctx.Done()
				return ctx.Err()
			})
	}()
	waitForSubscription(t, server, subscriptions, serveDone)
	waitForLogEvent(t, writer, "adapter.commands_listening", 3*time.Second)

	requestDone := make(chan error, 1)
	go func() {
		_, err := sendCommandWithDeadline(
			context.Background(), core, session.runtimeID, true,
			3*time.Second, time.Now().UTC().Add(300*time.Millisecond),
		)
		requestDone <- err
	}()
	waitForLogEvent(t, writer, "command.accepted", 3*time.Second)
	// Let the deadline cancel the handler context, then require silence: the
	// acceptance is the complete record when cancellation ends the handler.
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("command request did not finish after deadline")
	}
	// Handler completion plus session release proves the cancelled command
	// finished processing: Close waits for in-flight handlers, and the
	// release record is emitted after their diagnostics.
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled command handler did not return after deadline")
	}
	cancelServe()
	if serveErr := <-serveDone; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v, want canceled", serveErr)
	}
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	waitForLogEvent(t, writer, "adapter.session_released", 3*time.Second)
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "command.handler_failed" {
			t.Errorf("cancelled accepted command emitted internal failure: %v", record)
		}
		if record["level"] == "ERROR" {
			t.Errorf("cancelled handler emitted Error record: %v", record)
		}
	}
}

// This test protects initial-connection evidence and fails if the SDK claims
// dependency.connected before any successful NATS dial.
func TestUnavailableNATSDoesNotClaimConnected(t *testing.T) {
	t.Parallel()
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	writer := &lockedWriter{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type connectResult struct {
		session *Session
		err     error
	}
	connected := make(chan connectResult, 1)
	go func() {
		config := testConfig("nats://" + address)
		config.Logger = recordingLogger(t, writer)
		session, err := Connect(ctx, config)
		connected <- connectResult{session: session, err: err}
	}()
	// Let the client enter RECONNECTING and retry while no server listens.
	time.Sleep(600 * time.Millisecond)
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "dependency.connected" {
			t.Fatalf("claimed connected while NATS unavailable: %v", record)
		}
	}

	server := startServer(t, port, t.TempDir())
	startTestLifecycleResponder(t, server.ClientURL())
	var result connectResult
	select {
	case result = <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("Connect did not succeed after NATS became available")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	t.Cleanup(func() { _ = result.session.Close() })
	connectedRecord := waitForLogEvent(t, writer, "dependency.connected", 5*time.Second)
	if connectedRecord["level"] != "INFO" || connectedRecord["dependency"] != "nats" {
		t.Errorf("connected record = %v, want INFO dependency nats", connectedRecord)
	}
	if got := countLogRecords(t, writer, "dependency.connected"); got != 1 {
		t.Errorf("dependency.connected records = %d, want 1", got)
	}
}

// This test protects cancelled-claim teardown and fails if the deliberate
// connection close after claim cancellation is logged as unexpected.
func TestCanceledClaimCloseIsExpected(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	// No lifecycle responder: the claim never answers, so cancellation ends it.
	writer := &lockedWriter{}
	config := testConfig(server.ClientURL())
	config.Logger = recordingLogger(t, writer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Connect(ctx, config)
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Connect error = %v, want canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Connect did not return after cancellation")
	}
	// Allow the async Closed callback to arrive, then require no unexpected
	// terminal close.
	time.Sleep(500 * time.Millisecond)
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "dependency.closed" && record["level"] == "ERROR" {
			t.Errorf("cancelled claim close emitted unexpected-close error: %v", record)
		}
		if record["level"] == "ERROR" {
			t.Errorf("cancelled claim emitted Error record: %v", record)
		}
	}
}

// This test protects concurrent acknowledgement transitions and fails if
// repeated heartbeats log at Info or if acked health state races under the
// race detector.
func TestConcurrentHealthTransitionsLogOnce(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))
	ctx := testContext(t)

	const callers = 8
	setConcurrent := func(report HealthReport) {
		t.Helper()
		done := make(chan error, callers)
		for range callers {
			go func() { done <- session.SetHealth(ctx, report) }()
		}
		for range callers {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}
	setConcurrent(HealthReport{Status: HealthHealthy, SourceObservedAt: time.Now().UTC()})
	waitForLogRecord(t, writer, "adapter.health_reported", func(record map[string]any) bool {
		return record["status"] == "healthy"
	}, 3*time.Second)
	setConcurrent(HealthReport{
		Status: HealthUnhealthy, SourceObservedAt: time.Now().UTC(),
		ReasonCode: "hearth.network_unreachable",
	})
	unhealthy := waitForLogRecord(t, writer, "adapter.health_reported",
		func(record map[string]any) bool { return record["status"] == "unhealthy" }, 3*time.Second)
	if unhealthy["reason_code"] != "hearth.network_unreachable" {
		t.Errorf("unhealthy reason_code = %v, want hearth.network_unreachable",
			unhealthy["reason_code"])
	}

	time.Sleep(300 * time.Millisecond)
	if got := countLogRecords(t, writer, "adapter.health_reported"); got != 2 {
		t.Errorf("health_reported records = %d, want 2 (one per transition)", got)
	}
}
