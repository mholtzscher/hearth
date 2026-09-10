package adapter

import (
	"context"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// EntityEventID identifies one reported Entity Event across every publication
// retry. A Session mints it once and reuses it until JetStream acknowledges the
// report, so redelivery of one ID is the same event while a new ID is a new
// occurrence even when it repeats the same name.
type EntityEventID string

// EntityEvent is one named occurrence an Adapter reports for an Entity. It
// carries no State, Command link, arbitrary payload, or source time: the name
// is the whole report, and only Core decides whether the name is currently
// supported.
type EntityEvent struct {
	EntityID string
	Name     string
}

type wireEntityEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

// PublishEntityEvent publishes one Entity Event through JetStream and waits for
// JetStream storage acknowledgement, not Core acceptance. The Session mints the
// evt_ ID and a cor_ correlation ID once and encodes the envelope once, then
// reuses the same ID, bytes, subject, MsgId, and trace metadata for every
// transient retry until PubAck, caller cancellation or deadline, a permanent
// error, or Session termination. The minted ID is returned alongside any later
// error so the caller never assigns a new identity to an ambiguous report.
//
//nolint:gocognit // Retry classification mirrors command evidence publication and stays in one place.
func (session *Session) PublishEntityEvent(
	ctx context.Context,
	event EntityEvent,
) (EntityEventID, error) {
	if err := session.sessionError(); err != nil {
		return "", err
	}
	publicationContext, cancel := session.newPublicationContext(ctx, time.Time{})
	defer cancel()
	if err := publicationContext.Err(); err != nil {
		return "", err
	}

	generated, err := newID("evt")
	if err != nil {
		return "", err
	}
	entityEventID := EntityEventID(generated)
	correlationID, err := newID("cor")
	if err != nil {
		return entityEventID, err
	}
	envelope := natswire.Envelope[wireEntityEvent]{
		ID:            generated,
		Schema:        contractsv1.EntityEventSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		Data:          wireEntityEvent(event),
	}
	payload, err := natswire.Encode(session.validator, contractsv1.EntityEventSchemaID, envelope)
	if err != nil {
		return entityEventID, &ValidationError{Err: err}
	}
	subject, err := natswire.EntityEventSubject(session.adapterID, session.runtimeID, event.EntityID)
	if err != nil {
		return entityEventID, &ValidationError{Err: err}
	}
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, generated)
	natswire.InjectTrace(publicationContext, headers)

	//nolint:dupl // One encoded report stays one report across every transient retry.
	for {
		message := &natsgo.Msg{Subject: subject, Header: headers, Data: payload}
		if _, err = session.jetstream.PublishMsg(publicationContext, message); err == nil {
			if terminalErr := session.sessionError(); terminalErr != nil {
				return entityEventID, terminalErr
			}
			session.logPublishedEntityEvent(publicationContext, entityEventID, correlationID, event)
			return entityEventID, nil
		}
		if terminalErr := session.sessionError(); terminalErr != nil {
			return entityEventID, terminalErr
		}
		if ctxErr := publicationContext.Err(); ctxErr != nil {
			return entityEventID, ctxErr
		}
		if !isTransientPublishError(err) {
			return entityEventID, err
		}
		session.log().DebugContext(publicationContext, "retrying operation",
			slog.String("event", "dependency.retrying"),
			slog.String("operation", "entity_event_publish"),
			slog.String("dependency", "nats"),
			slog.String("error_code", requestErrorCode(err)),
		)
		timer := time.NewTimer(requestRetryWait)
		select {
		case <-publicationContext.Done():
			timer.Stop()
			if terminalErr := session.sessionError(); terminalErr != nil {
				return entityEventID, terminalErr
			}
			return entityEventID, publicationContext.Err()
		case <-timer.C:
		}
	}
}

// logPublishedEntityEvent emits the acknowledged publication evidence. The
// record carries only bounded identity and the schema-constrained name, never
// the encoded envelope.
func (session *Session) logPublishedEntityEvent(
	publicationContext context.Context,
	entityEventID EntityEventID,
	correlationID string,
	event EntityEvent,
) {
	session.log().LogAttrs(publicationContext, slog.LevelDebug, "entity event published",
		slog.String("event", "entity_event.published"),
		slog.String("entity_event_id", string(entityEventID)),
		slog.String("entity_id", event.EntityID),
		slog.String("name", event.Name),
		slog.String("correlation_id", correlationID),
	)
}
