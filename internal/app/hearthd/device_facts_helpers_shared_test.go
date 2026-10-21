//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Fixed W3C trace context these tests inject into inbound reports and expect the
// relay to restore on the published fact.
const (
	factsTraceIDHex  = "4bf92f3577b34da6a3ce929d0e0e4736"
	factsSpanIDHex   = "00f067aa0ba902b7"
	factsTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	factsTracestate  = "hearth=facts"
)

// deviceFactEnvelope is the generic Device Fact envelope these tests decode.
type deviceFactEnvelope struct {
	ID            string          `json:"id"`
	Schema        string          `json:"schema"`
	EmittedAt     string          `json:"emitted_at"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id"`
	Data          json.RawMessage `json:"data"`
}

// deviceFactRecord is one received, schema-validated Device Fact plus the
// delivery metadata a durable publisher must reproduce.
type deviceFactRecord struct {
	envelope    deviceFactEnvelope
	route       natswire.DeviceFactRoute
	data        map[string]json.RawMessage
	messageID   string
	traceparent string
	tracestate  string
}

// deviceFactSubscriber is a plain external NATS subscriber: no SDK, no
// JetStream and no Core process coupling. It observes the fact as any language's
// NATS client would.
type deviceFactSubscriber struct {
	connection *natsgo.Conn
	messages   chan *natsgo.Msg
	validator  *contractsv1.Validator
	received   []deviceFactRecord
}

func newDeviceFactSubscriber(t *testing.T, url string, options ...natsgo.Option) *deviceFactSubscriber {
	t.Helper()
	connection, err := natsgo.Connect(url, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	messages := make(chan *natsgo.Msg, 256)
	if _, subscribeErr := connection.Subscribe(natswire.DeviceFactWildcard(), func(message *natsgo.Msg) {
		messages <- message
	}); subscribeErr != nil {
		t.Fatal(subscribeErr)
	}
	if flushErr := connection.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return &deviceFactSubscriber{connection: connection, messages: messages, validator: validator}
}

// waitForCausation returns the first fact that reports the given durable source
// identity, retaining every other decoded fact so an out-of-order arrival is
// searched rather than lost.
func (subscriber *deviceFactSubscriber) waitForCausation(
	t *testing.T,
	causationID string,
	family natswire.DeviceFactFamily,
) deviceFactRecord {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		for _, record := range subscriber.received {
			if record.route.Family == family && record.envelope.CausationID == causationID {
				return record
			}
		}
		select {
		case message := <-subscriber.messages:
			subscriber.received = append(subscriber.received, subscriber.decode(t, message))
		case <-deadline.C:
			t.Fatalf("timed out waiting for a %s fact caused by %q", family, causationID)
			return deviceFactRecord{}
		}
	}
}

// assertNone fails when any fact arrives inside a short bounded window, so a
// forbidden publication is caught without relying on a fixed sleep.
func (subscriber *deviceFactSubscriber) assertNone(t *testing.T) {
	t.Helper()
	select {
	case message := <-subscriber.messages:
		t.Fatalf("unexpected Device Fact on %q: %s", message.Subject, message.Data)
	case <-time.After(300 * time.Millisecond):
	}
}

func (subscriber *deviceFactSubscriber) decode(t *testing.T, message *natsgo.Msg) deviceFactRecord {
	t.Helper()
	route, err := natswire.ParseDeviceFactSubject(message.Subject)
	if err != nil {
		t.Fatalf("parse Device Fact subject: %v", err)
	}
	schemaID := factSchemaIDForFamily(t, route.Family)
	if validateErr := subscriber.validator.Validate(schemaID, message.Data); validateErr != nil {
		t.Fatalf("Device Fact payload is not schema-valid: %v", validateErr)
	}
	var envelope deviceFactEnvelope
	if unmarshalErr := json.Unmarshal(message.Data, &envelope); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if envelope.Schema != schemaID {
		t.Fatalf("fact schema = %q, want %q", envelope.Schema, schemaID)
	}
	if _, idErr := devices.ParseDeviceFactID(envelope.ID); idErr != nil {
		t.Fatalf("fact ID %q is not canonical: %v", envelope.ID, idErr)
	}
	// The relay publishes under the stable fact identity, so the broker's
	// duplicate window collapses a retried publish instead of storing it twice.
	if message.Header.Get(natsgo.MsgIdHdr) != envelope.ID {
		t.Fatalf("fact Nats-Msg-Id = %q, want the envelope identity %q",
			message.Header.Get(natsgo.MsgIdHdr), envelope.ID)
	}
	var data map[string]json.RawMessage
	if dataErr := json.Unmarshal(envelope.Data, &data); dataErr != nil {
		t.Fatal(dataErr)
	}
	var payloadEntity string
	if entityErr := json.Unmarshal(data["entity_id"], &payloadEntity); entityErr != nil {
		t.Fatal(entityErr)
	}
	if payloadEntity != route.EntityID {
		t.Fatalf("payload Entity %q disagrees with subject Entity %q", payloadEntity, route.EntityID)
	}
	subscriber.assertVariantAgrees(t, route, data)
	return deviceFactRecord{
		envelope:    envelope,
		route:       route,
		data:        data,
		messageID:   message.Header.Get(natsgo.MsgIdHdr),
		traceparent: message.Header.Get("traceparent"),
		tracestate:  message.Header.Get("tracestate"),
	}
}

// assertVariantAgrees enforces the subject/payload agreement a subscriber must
// also check: the family-specific payload field must repeat the subject variant.
func (subscriber *deviceFactSubscriber) assertVariantAgrees(
	t *testing.T,
	route natswire.DeviceFactRoute,
	data map[string]json.RawMessage,
) {
	t.Helper()
	field := ""
	switch route.Family {
	case natswire.DeviceFactFamilyObservation:
		field = "disposition"
	case natswire.DeviceFactFamilyEntityEvent:
		field = "name"
	}
	var value string
	if err := json.Unmarshal(data[field], &value); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	if value != route.Variant {
		t.Fatalf("payload %s %q disagrees with subject variant %q", field, value, route.Variant)
	}
}

func factSchemaIDForFamily(t *testing.T, family natswire.DeviceFactFamily) string {
	t.Helper()
	switch family {
	case natswire.DeviceFactFamilyObservation:
		return contractsv1.ObservationFactSchemaID
	case natswire.DeviceFactFamilyEntityEvent:
		return contractsv1.EntityEventFactSchemaID
	}
	t.Fatalf("unknown Device Fact family %q", family)
	return ""
}

// waitForDeviceFactOutboxEmpty waits until the relay has published and deleted
// every pending row, which is the durable proof the outbox settled rather than
// merely that one fact arrived.
func waitForDeviceFactOutboxEmpty(ctx context.Context, t *testing.T, databasePath string) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		var pending int
		if queryErr := database.QueryRowContext(
			ctx, "SELECT count(*) FROM device_facts_outbox",
		).Scan(&pending); queryErr != nil {
			return false, queryErr
		}
		return pending == 0, nil
	})
}

// startDeviceFactsCore starts Core over one embedded JetStream server and
// returns the HTTP address and the process error channel.
func startDeviceFactsCore(
	ctx context.Context,
	t *testing.T,
	serverURL string,
	databasePath string,
) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	httpAddress, httpOptions := reserveLoopbackListener(t)
	runContext, stopCore := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- runWithOptions(runContext, Config{
			HouseholdTimezone: "UTC", HTTPAddr: httpAddress,
			NATSURL: serverURL, SQLitePath: databasePath,
			Agent: requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler), httpOptions)
	}()
	return httpAddress, stopCore, runErrors
}

func stopDeviceFactsCore(t *testing.T, stopCore context.CancelFunc, runErrors <-chan error) {
	t.Helper()
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("hearthd run returned %v", runErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("hearthd did not stop")
	}
}

// waitForCoreReady waits until /readyz reports ready. Readiness is the
// assembly's own verdict that SQLite, the shared NATS connection, the fact
// stream, the relay, both durable resources and both consumers are live.
func waitForCoreReady(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	runErrors <-chan error,
) {
	t.Helper()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		request, requestErr := http.NewRequestWithContext(
			ctx, http.MethodGet, "http://"+httpAddress+"/readyz", nil,
		)
		if requestErr != nil {
			return false, requestErr
		}
		response, responseErr := http.DefaultClient.Do(request)
		if responseErr != nil {
			return false, nil
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, nil
	})
}

func mustDeviceFactValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}
