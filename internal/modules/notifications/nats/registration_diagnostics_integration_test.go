//go:build integration

package nats_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationnats "github.com/mholtzscher/hearth/internal/modules/notifications/nats"
)

func TestRegistrationFaultDiagnosticsPreserveNoReplyAndPrivacy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		cause error
		fault bool
	}{
		{"unexpected", errors.New("SENSITIVE-storage"), true},
		{"wrapped", fmt.Errorf("SENSITIVE-wrapper: %w", errors.New("SENSITIVE-storage")), true},
		{"stale runtime", notifications.ErrStaleRuntime, false},
		{"invalid input", notifications.ErrInvalidInput, false},
		{"success", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			exerciseRegistrationDiagnostic(t, test.cause, test.fault)
		})
	}
}

func exerciseRegistrationDiagnostic(t *testing.T, cause error, fault bool) {
	t.Helper()
	connection, validator := transportFixture(t)
	attempt := testAttempt(t)
	destinationID, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	var calls atomic.Int32
	registrar := registrarFunc(
		func(ctx context.Context, _ notifications.Registration) (notifications.Destination, error) {
			assertRegistrationContext(ctx, t)
			calls.Add(1)
			return notifications.Destination{ID: destinationID}, cause
		},
	)
	server, err := notificationnats.StartRegistrationServer(
		connection,
		validator,
		registrar,
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	subject, err := natswire.NotificationRegistrationSubject(attempt.Route.AdapterID, attempt.Route.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	request, payload := registrationPayload(t, "SENSITIVE-destination-name")
	reply, requestErr := connection.RequestMsg(&natsgo.Msg{
		Subject: subject, Data: payload,
		Header: natsgo.Header{"traceparent": {"00-01000000000000000000000000000000-0200000000000000-01"}},
	}, 200*time.Millisecond)
	if fault {
		if !errors.Is(requestErr, natsgo.ErrTimeout) {
			t.Fatalf("fault unexpectedly replied: %v", requestErr)
		}
	} else {
		assertRegistrationDiagnosticReply(t, validator, reply, requestErr, cause)
	}
	if err = server.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("registration calls=%d, want=1", calls.Load())
	}
	assertRegistrationDiagnosticLog(t, logs.Bytes(), fault, request.ID, attempt.Route, subject)
}

func assertRegistrationDiagnosticLog(
	t *testing.T,
	raw []byte,
	fault bool,
	id string,
	route notifications.RouteSnapshot,
	subject string,
) {
	t.Helper()
	if !fault {
		if len(raw) != 0 {
			t.Fatalf("modeled result logged as a fault: %s", raw)
		}
		return
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"level":           "ERROR",
		"event":           "notification.registration_failed",
		"stage":           "persist",
		"error_code":      "registration_write_failed",
		"registration_id": id,
		"adapter_id":      route.AdapterID,
		"runtime_id":      route.RuntimeID,
	}
	for key, value := range want {
		if record[key] != value {
			t.Errorf("%s=%v, want=%v: %s", key, record[key], value, raw)
		}
	}
	if len(record) != len(want)+2 {
		t.Errorf("unexpected fields beyond timestamp/message: %s", raw)
	}
	if strings.Contains(string(raw), "SENSITIVE") || strings.Contains(string(raw), subject) {
		t.Fatal("private cause, body, or full subject logged")
	}
}

func assertRegistrationDiagnosticReply(
	t *testing.T,
	validator *contractsv1.Validator,
	reply *natsgo.Msg,
	requestErr, cause error,
) {
	t.Helper()
	if requestErr != nil {
		t.Fatal(requestErr)
	}
	response, err := natswire.Decode[registrationResponse](
		validator,
		contractsv1.NotificationRegistrationResponseSchemaID,
		reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	status, code := "accepted", ""
	if errors.Is(cause, notifications.ErrStaleRuntime) {
		status, code = "rejected", "stale_runtime"
	}
	if errors.Is(cause, notifications.ErrInvalidInput) {
		status, code = "rejected", "invalid_registration"
	}
	if response.Data.Status != status || response.Data.ReasonCode != code {
		t.Fatalf("changed registration result: %+v", response.Data)
	}
}
