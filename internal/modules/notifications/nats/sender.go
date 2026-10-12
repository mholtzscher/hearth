// Package nats maps notification registration and one-shot sends to Hearth envelopes.
package nats

import (
	"context"
	"errors"
	"strings"
	"time"

	natsgo "github.com/nats-io/nats.go"

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
type Sender struct {
	connection *natsgo.Conn
	validator  *contractsv1.Validator
}

const invalidResponse = "invalid_response"

func NewSender(connection *natsgo.Conn, validator *contractsv1.Validator) *Sender {
	return &Sender{connection: connection, validator: validator}
}

var _ notifications.Sender = (*Sender)(nil)

func (s *Sender) Send(ctx context.Context, a notifications.Attempt) notifications.Evidence {
	if !time.Now().Before(a.Deadline) {
		return notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired"}
	}
	subject, err := natswire.NotificationSendSubject(a.Route.AdapterID, a.Route.RuntimeID)
	if err != nil {
		return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "transport_error"}
	}
	id := string(a.ID)
	correlationID := "cor_" + strings.TrimPrefix(id, "ntf_")
	envelope := natswire.Envelope[sendRequest]{
		ID:            id,
		Schema:        contractsv1.NotificationSendRequestSchemaID,
		EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		CorrelationID: correlationID,
		Data: sendRequest{
			AttemptID: id,
			LocalKey:  a.Route.LocalKey,
			Title:     a.Message.Title,
			Message:   a.Message.Text,
			Deadline:  a.Deadline.UTC().Format(time.RFC3339Nano),
		},
	}
	payload, err := natswire.Encode(s.validator, contractsv1.NotificationSendRequestSchemaID, envelope)
	if err != nil {
		return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: invalidResponse}
	}
	bounded, cancel := context.WithDeadline(ctx, a.Deadline)
	defer cancel()
	if bounded.Err() != nil {
		return notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "deadline_expired"}
	}
	request := &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload}
	natswire.InjectTrace(ctx, request.Header)
	response, err := s.connection.RequestMsgWithContext(bounded, request)
	if errors.Is(err, natsgo.ErrNoResponders) {
		return notifications.Evidence{Outcome: notifications.NotSent, ReasonCode: "destination_unavailable"}
	}
	if err != nil {
		reason := "transport_error"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsgo.ErrTimeout) {
			reason = "outcome_timeout"
		}
		return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: reason}
	}
	if !time.Now().Before(a.Deadline) {
		return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: "outcome_timeout"}
	}
	wire, err := natswire.Decode[sendResponse](s.validator, contractsv1.NotificationSendResponseSchemaID, response.Data)
	if err != nil || wire.CorrelationID != correlationID || wire.CausationID == nil || *wire.CausationID != id ||
		wire.Data.AttemptID != id {
		return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: invalidResponse}
	}
	e := notifications.Evidence{
		Outcome:    notifications.Outcome(wire.Data.Outcome),
		ReasonCode: wire.Data.ReasonCode,
		Receipt:    wire.Data.Receipt,
	}
	if notifications.ValidateEvidence(e) != nil {
		return notifications.Evidence{Outcome: notifications.Unknown, ReasonCode: invalidResponse}
	}
	return e
}
