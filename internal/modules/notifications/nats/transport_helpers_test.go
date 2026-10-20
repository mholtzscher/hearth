package nats_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type sendRequest struct {
	AttemptID string `json:"attempt_id"`
	LocalKey  string `json:"local_key"`
	Title     string `json:"title,omitempty"`
	Message   string `json:"message"`
	Deadline  string `json:"deadline"`
}

type sendResponse struct {
	AttemptID  string `json:"attempt_id"`
	Outcome    string `json:"outcome"`
	ReasonCode string `json:"reason_code,omitempty"`
	Receipt    string `json:"receipt,omitempty"`
}

type registrationRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type registrationResponse struct {
	Status        string `json:"status"`
	DestinationID string `json:"destination_id,omitempty"`
	ReasonCode    string `json:"reason_code,omitempty"`
}

func testAttempt(t *testing.T) notifications.Attempt {
	t.Helper()
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	return notifications.Attempt{
		ID: id,
		Route: notifications.RouteSnapshot{
			AdapterID: "alerts",
			LocalKey:  "home",
			RuntimeID: "run_" + runtime.String(),
		},
		Message:   notifications.Message{Title: "Freezer", Text: "Too warm"},
		StartedAt: at,
		Deadline:  at.Add(notifications.SendTimeout),
	}
}

func replyID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return "rep_" + id.String()
}

type registrarFunc func(context.Context, notifications.Registration) (notifications.Destination, error)

func (fn registrarFunc) Register(ctx context.Context, r notifications.Registration) (notifications.Destination, error) {
	return fn(ctx, r)
}

func assertRegistrationContext(ctx context.Context, t *testing.T) {
	t.Helper()
	deadline, bounded := ctx.Deadline()
	if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > notifications.PersistenceTimeout {
		t.Error("registration persistence context lacks its five-second deadline")
	}
	span := trace.SpanContextFromContext(ctx)
	if span.TraceID().String() != "01000000000000000000000000000000" || span.SpanID().String() != "0200000000000000" {
		t.Error("registration persistence context lost request trace")
	}
}

func registrationPayload(t *testing.T, name string) (natswire.Envelope[registrationRequest], []byte) {
	t.Helper()
	id := strings.Replace(replyID(t), "rep_", "reg_", 1)
	request := natswire.Envelope[registrationRequest]{
		ID:            id,
		Schema:        contractsv1.NotificationRegistrationRequestSchemaID,
		EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		CorrelationID: strings.Replace(id, "reg_", "cor_", 1),
		Data:          registrationRequest{Key: "home", Name: name},
	}
	// Marshal directly: malformed external names must reach the wire decoder,
	// not be rejected by the test's outbound schema encoder.
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return request, payload
}
