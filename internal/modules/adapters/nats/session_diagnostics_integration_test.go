//go:build integration

package nats //nolint:testpackage // Uses existing lifecycle doubles and production server registration.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

func TestSessionFaultDiagnosticsPreserveNoReplyAndPrivacy(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"claim", "heartbeat", "release"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
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
			cause := fmt.Errorf("SENSITIVE-wrapper: %w", errors.New("SENSITIVE-storage"))
			recorder := &lifecycleRecorder{claimErr: cause, heartbeatErr: cause, releaseErr: cause}
			var logs bytes.Buffer
			server, err := StartSessionServer(
				connection,
				validator,
				recorder,
				recorder,
				slog.New(slog.NewJSONHandler(&logs, nil)),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close(context.Background()) })
			request, idKey, id := sessionDiagnosticRequest(t, validator, operation)
			_, err = connection.RequestMsg(request, 200*time.Millisecond)
			if !errors.Is(err, natsgo.ErrTimeout) {
				t.Fatalf("fault unexpectedly replied: %v", err)
			}
			if err = server.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertSessionDiagnosticLog(t, logs.Bytes(), operation, idKey, id, request.Subject)
		})
	}
}

func assertSessionDiagnosticLog(t *testing.T, raw []byte, operation, idKey, id, subject string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"level": "ERROR", "event": "session." + operation + "_failed", "error_code": operation + "_failed",
		"stage": operation, "adapter_id": "simulator", idKey: id,
	}
	if operation != "claim" {
		want["runtime_id"] = testRuntimeID
	}
	for key, value := range want {
		if record[key] != value {
			t.Errorf("%s=%v, want=%v: %s", key, record[key], value, raw)
		}
	}
	if len(record) != len(want)+2 {
		t.Errorf("unexpected fields beyond timestamp/message: %s", raw)
	}
	if strings.Contains(strings.ToUpper(string(raw)), "SENSITIVE") || strings.Contains(string(raw), subject) {
		t.Fatal("private cause, body, or subject logged")
	}
}

func sessionDiagnosticRequest(
	t *testing.T,
	validator *contractsv1.Validator,
	operation string,
) (*natsgo.Msg, string, string) {
	t.Helper()
	var body any
	var schema, id, subject, idKey string
	var err error
	switch operation {
	case "claim":
		schema, id, idKey = contractsv1.AdapterClaimRequestSchemaID, "clm_01890f47-7a6b-7c4d-8e9f-0123456789ab", "claim_id"
		subject, err = natswire.AdapterClaimSubject("simulator")
		body = adapterClaimRequest{
			AdapterID:       "simulator",
			RuntimeID:       testRuntimeID,
			SoftwareName:    "sensitive-software",
			SoftwareVersion: "test",
		}
	case "heartbeat":
		schema, id, idKey = contractsv1.AdapterHeartbeatRequestSchemaID, "hbt_01890f47-7a6b-7c4d-8e9f-0123456789ab", "heartbeat_id"
		subject, err = natswire.AdapterHeartbeatSubject("simulator", testRuntimeID)
		body = adapterHeartbeatRequest{
			ExternalSystem: externalSystemHealth{Status: "healthy", SourceObservedAt: "2026-08-29T15:00:00Z"},
		}
	case "release":
		schema, id, idKey = contractsv1.AdapterReleaseRequestSchemaID, "rel_01890f47-7a6b-7c4d-8e9f-0123456789ab", "release_id"
		subject, err = natswire.AdapterReleaseSubject("simulator", testRuntimeID)
		body = adapterReleaseRequest{}
	default:
		t.Fatalf("unknown operation %q", operation)
	}
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(
		validator,
		schema,
		natswire.Envelope[any]{
			ID:            id,
			Schema:        schema,
			EmittedAt:     "2026-08-29T15:00:00Z",
			CorrelationID: testCorrelationID,
			Data:          body,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &natsgo.Msg{Subject: subject, Data: payload}, idKey, id
}
