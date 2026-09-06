package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

type wireObservation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

type observationLink struct {
	commandID     string
	correlationID string
	entityID      string
	runtimeID     string
	deadline      time.Time
	traceContext  trace.SpanContext
}

type commandEvidence struct {
	session *Session
	link    observationLink
}

func newCommandEvidence(ctx context.Context, session *Session, link observationLink) *commandEvidence {
	link.runtimeID = session.runtimeID
	link.traceContext = trace.SpanContextFromContext(ctx)
	return &commandEvidence{session: session, link: link}
}

func (evidence *commandEvidence) PublishObservation(
	ctx context.Context,
	observation Observation,
) (ObservationID, error) {
	if observation.EntityID != evidence.link.entityID {
		return "", &ValidationError{Err: errors.New("linked Observation Entity ID does not match the accepted Command")}
	}
	return evidence.session.publishObservation(ctx, observation, &evidence.link)
}

//nolint:funlen,gocognit // Retry and command-causality checks stay together around one publication.
func (session *Session) publishObservation(
	ctx context.Context,
	observation Observation,
	link *observationLink,
) (ObservationID, error) {
	if err := session.sessionError(); err != nil {
		return "", err
	}

	deadline := time.Time{}
	if link != nil {
		deadline = link.deadline
	}
	publicationContext, cancel := session.newPublicationContext(ctx, deadline)
	defer cancel()
	if err := publicationContext.Err(); err != nil {
		return "", err
	}
	if link != nil {
		publicationContext = trace.ContextWithSpanContext(publicationContext, link.traceContext)
	}

	generated, err := newID("obs")
	if err != nil {
		return "", err
	}
	observationID := ObservationID(generated)
	correlationID, err := newID("cor")
	if err != nil {
		return observationID, err
	}
	wire := wireObservation{
		EntityID:          observation.EntityID,
		Value:             observation.Value,
		AdapterReceivedAt: observation.AdapterReceivedAt,
		SourceUpdatedAt:   observation.SourceUpdatedAt,
	}
	var causationID *string
	if link != nil {
		correlationID = link.correlationID
		causationID = &link.commandID
		wire.RefreshForCommand = &link.commandID
	}
	event := natswire.Envelope[wireObservation]{
		ID:            generated,
		Schema:        contractsv1.ObservationSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		CausationID:   causationID,
		Data:          wire,
	}
	payload, err := natswire.Encode(session.validator, contractsv1.ObservationSchemaID, event)
	if err != nil {
		return observationID, &ValidationError{Err: err}
	}
	runtimeID := session.runtimeID
	if link != nil {
		runtimeID = link.runtimeID
	}
	subject, err := natswire.ObservationSubject(session.adapterID, runtimeID, observation.EntityID)
	if err != nil {
		return observationID, &ValidationError{Err: err}
	}
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, generated)
	natswire.InjectTrace(publicationContext, headers)

	for {
		message := &natsgo.Msg{Subject: subject, Header: headers, Data: payload}
		if _, err = session.jetstream.PublishMsg(publicationContext, message); err == nil {
			if terminalErr := session.sessionError(); terminalErr != nil {
				return observationID, terminalErr
			}
			session.logPublishedObservation(publicationContext, observationID, observation, link)
			return observationID, nil
		}
		if terminalErr := session.sessionError(); terminalErr != nil {
			return observationID, terminalErr
		}
		if ctxErr := publicationContext.Err(); ctxErr != nil {
			return observationID, ctxErr
		}
		if !isTransientPublishError(err) {
			return observationID, err
		}
		session.log().DebugContext(publicationContext, "retrying operation",
			slog.String("event", "dependency.retrying"),
			slog.String("operation", "observation_publish"),
			slog.String("dependency", "nats"),
			slog.String("error_code", requestErrorCode(err)),
		)
		timer := time.NewTimer(requestRetryWait)
		select {
		case <-publicationContext.Done():
			timer.Stop()
			if terminalErr := session.sessionError(); terminalErr != nil {
				return observationID, terminalErr
			}
			return observationID, publicationContext.Err()
		case <-timer.C:
		}
	}
}

// logPublishedObservation emits the acknowledged publication evidence. A
// command-linked publication carries the command and correlation IDs; an
// ordinary publication carries only its own Observation ID.
func (session *Session) logPublishedObservation(
	publicationContext context.Context,
	observationID ObservationID,
	observation Observation,
	link *observationLink,
) {
	attrs := []slog.Attr{
		slog.String("event", "observation.published"),
		slog.String("observation_id", string(observationID)),
		slog.String("entity_id", observation.EntityID),
	}
	if link != nil {
		attrs = append(attrs,
			slog.String("command_id", link.commandID),
			slog.String("correlation_id", link.correlationID),
		)
	}
	session.log().LogAttrs(publicationContext, slog.LevelDebug, "observation published", attrs...)
}

func (session *Session) newPublicationContext(
	ctx context.Context,
	deadline time.Time,
) (context.Context, context.CancelFunc) {
	var publicationContext context.Context
	var cancel context.CancelFunc
	if deadline.IsZero() {
		publicationContext, cancel = context.WithCancel(ctx)
	} else {
		publicationContext, cancel = context.WithDeadline(ctx, deadline)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-session.closed:
			cancel()
		case <-publicationContext.Done():
		case <-done:
		}
	}()
	return publicationContext, func() {
		close(done)
		cancel()
	}
}
