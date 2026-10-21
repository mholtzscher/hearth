//go:build integration

package nats_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationnats "github.com/mholtzscher/hearth/internal/modules/notifications/nats"
)

func transportFixture(t *testing.T) (*natsgo.Conn, *contractsv1.Validator) {
	t.Helper()
	_, connection, validator := transportBrokerFixture(t)
	return connection, validator
}

func transportBrokerFixture(t *testing.T) (*server.Server, *natsgo.Conn, *contractsv1.Validator) {
	t.Helper()
	broker, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go broker.Start()
	if !broker.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS not ready")
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
	return broker, connection, validator
}

// A2/A3/A6: actual broker replies are strict correlated evidence, not retry cues.
//
//nolint:gocognit // The table drives independently specified broker fault scenarios.
func TestSenderRealNATSEvidenceAndOneShotFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		outcome notifications.Outcome
		reason  string
		calls   int32
	}{
		{"accepted", notifications.Accepted, "", 1},
		{"rejected", notifications.Rejected, "provider_forbidden", 1},
		{"not_sent", notifications.NotSent, "adapter_busy", 1},
		{"unknown", notifications.Unknown, "provider_error", 1},
		{"malformed", notifications.Unknown, "invalid_response", 1},
		{"wrong_correlation", notifications.Unknown, "invalid_response", 1},
		{"wrong_attempt", notifications.Unknown, "invalid_response", 1},
		{"lost", notifications.Unknown, "outcome_timeout", 1},
		{"no_responders", notifications.NotSent, "destination_unavailable", 0},
		{"expired", notifications.NotSent, "deadline_expired", 0},
		{"closed", notifications.Unknown, "transport_error", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scenario := tc.name
			connection, validator := transportFixture(t)
			a := testAttempt(t)
			sender := notificationnats.NewSender(connection, validator)
			var calls atomic.Int32
			subject, err := natswire.NotificationSendSubject(a.Route.AdapterID, a.Route.RuntimeID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "no_responders" && scenario != "closed" {
				_, err = connection.Subscribe(
					subject,
					func(message *natsgo.Msg) { calls.Add(1); respondToSend(t, validator, message, a, scenario) },
				)
				if err != nil {
					t.Fatal(err)
				}
				if err = connection.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "expired" {
				a.Deadline = time.Now().Add(-time.Second)
			}
			if scenario == "lost" {
				a.Deadline = time.Now().Add(100 * time.Millisecond)
			}
			if scenario == "closed" {
				connection.Close()
			}
			e := sender.Send(context.Background(), a)
			if e.Outcome != tc.outcome || e.ReasonCode != tc.reason {
				t.Fatalf("evidence: %+v want %s/%s", e, tc.outcome, tc.reason)
			}
			if calls.Load() != tc.calls {
				t.Fatalf("calls=%d want=%d", calls.Load(), tc.calls)
			}
		})
	}
}

//nolint:gocognit // Registration response and storage-silence cases share one live server.
func TestRegistrationRealNATSMapsRuntimeAndCorrelatesRejections(t *testing.T) {
	t.Parallel()
	connection, validator := transportFixture(t)
	a := testAttempt(t)
	destinationID, err := notifications.NewDestinationID()
	if err != nil {
		t.Fatal(err)
	}
	var registrations atomic.Int32
	registrar := registrarFunc(
		func(ctx context.Context, r notifications.Registration) (notifications.Destination, error) {
			assertRegistrationContext(ctx, t)
			registrations.Add(1)
			if r.AdapterID != a.Route.AdapterID || r.RuntimeID != a.Route.RuntimeID || r.Key != "home" {
				t.Errorf("registration route: %+v", r)
			}
			switch r.Name {
			case "stale":
				return notifications.Destination{}, notifications.ErrStaleRuntime
			case "invalid":
				return notifications.Destination{}, notifications.ErrInvalidInput
			case "fault":
				return notifications.Destination{}, errors.New("storage fault")
			}
			return notifications.Destination{ID: destinationID, Name: r.Name}, nil
		},
	)
	s, err := notificationnats.StartRegistrationServer(connection, validator, registrar, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	subject, err := natswire.NotificationRegistrationSubject(a.Route.AdapterID, a.Route.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Home", "stale", "invalid", "fault", "caused", "\v", "\vHome", "Home\v", "\u0085Home", "Home\u3000"} {
		request, payload := registrationPayload(t, name)
		if name == "caused" {
			request.CausationID = &request.ID
			payload, err = json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
		}
		before := registrations.Load()
		message, requestErr := connection.RequestMsg(&natsgo.Msg{
			Subject: subject, Data: payload,
			Header: natsgo.Header{"traceparent": {"00-01000000000000000000000000000000-0200000000000000-01"}},
		}, 200*time.Millisecond)
		malformed := name == "caused" || strings.TrimSpace(name) != name || strings.TrimSpace(name) == ""
		if name == "fault" || malformed {
			if !errors.Is(requestErr, natsgo.ErrTimeout) {
				t.Fatalf("silent registration %q answered: %v", name, requestErr)
			}
			if malformed && registrations.Load() != before {
				t.Fatalf("malformed name %q reached registrar", name)
			}
			continue
		}
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, decodeErr := natswire.Decode[registrationResponse](
			validator,
			contractsv1.NotificationRegistrationResponseSchemaID,
			message.Data,
		)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if response.CorrelationID != request.CorrelationID || response.CausationID == nil ||
			*response.CausationID != request.ID {
			t.Fatalf("correlation: %+v", response)
		}
		if name == "Home" {
			if response.Data.DestinationID != string(destinationID) {
				t.Fatalf("identity: %+v", response.Data)
			}
		} else {
			want := "stale_runtime"
			if name == "invalid" {
				want = "invalid_registration"
			}
			if response.Data.Status != "rejected" || response.Data.ReasonCode != want {
				t.Fatalf("rejection: %+v", response.Data)
			}
		}
	}
}

func respondToSend(
	t *testing.T,
	validator *contractsv1.Validator,
	message *natsgo.Msg,
	a notifications.Attempt,
	scenario string,
) {
	t.Helper()
	request, err := natswire.Decode[sendRequest](validator, contractsv1.NotificationSendRequestSchemaID, message.Data)
	if err != nil {
		t.Error(err)
		return
	}
	if request.ID != string(a.ID) || request.Data.AttemptID != string(a.ID) || request.Data.LocalKey != "home" ||
		request.Data.Message != "Too warm" ||
		request.Data.Deadline != a.Deadline.UTC().Format(time.RFC3339Nano) {
		t.Errorf("incorrect request: %+v", request)
	}
	if scenario == "lost" {
		return
	}
	if scenario == "malformed" {
		_ = message.Respond([]byte(`{}`))
		return
	}
	data := sendResponse{AttemptID: string(a.ID), Outcome: "accepted", Receipt: "receipt"}
	switch scenario {
	case "rejected":
		data = sendResponse{AttemptID: string(a.ID), Outcome: "rejected", ReasonCode: "provider_forbidden"}
	case "not_sent":
		data = sendResponse{AttemptID: string(a.ID), Outcome: "not_sent", ReasonCode: "adapter_busy"}
	case "unknown":
		data = sendResponse{AttemptID: string(a.ID), Outcome: "unknown", ReasonCode: "provider_error"}
	case "wrong_attempt":
		data.AttemptID = strings.Replace(replyID(t), "rep_", "ntf_", 1)
	}
	response := natswire.Envelope[sendResponse]{
		ID:            replyID(t),
		Schema:        contractsv1.NotificationSendResponseSchemaID,
		EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		CorrelationID: request.CorrelationID,
		CausationID:   &request.ID,
		Data:          data,
	}
	if scenario == "wrong_correlation" {
		response.CorrelationID = "cor_" + strings.TrimPrefix(replyID(t), "rep_")
	}
	payload, err := natswire.Encode(validator, contractsv1.NotificationSendResponseSchemaID, response)
	if err != nil {
		t.Error(err)
		return
	}
	_ = message.Respond(payload)
}
