package adapter

import (
	"context"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// DeviceEventID identifies one reported Device Event across every publication
// retry. A Session mints it once and reuses it until JetStream acknowledges the
// report, so redelivery of one ID is the same event while a new ID is a new
// occurrence even when it repeats the same name.
type DeviceEventID string

// DeviceEvent is one named occurrence an Adapter reports for an Entity. It
// carries no State, Command link, arbitrary payload, or source time: the name
// is the whole report, and only Core decides whether the name is currently
// supported.
type DeviceEvent struct {
	EntityID string
	Name     string
}

type wireDeviceEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

// PublishDeviceEvent publishes one Device Event through JetStream and waits for
// JetStream storage acknowledgement, not Core acceptance. The Session mints the
// evt_ ID and a cor_ correlation ID once and encodes the envelope once, then
// reuses the same ID, bytes, subject, MsgId, and trace metadata for every
// transient retry until PubAck, caller cancellation or deadline, a permanent
// error, or Session termination. The minted ID is returned alongside any later
// error so the caller never assigns a new identity to an ambiguous report.
//
//nolint:gocognit // Retry classification mirrors command evidence publication and stays in one place.
func (session *Session) PublishDeviceEvent(
	ctx context.Context,
	event DeviceEvent,
) (DeviceEventID, error) {
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
	deviceEventID := DeviceEventID(generated)
	correlationID, err := newID("cor")
	if err != nil {
		return deviceEventID, err
	}
	envelope := natswire.Envelope[wireDeviceEvent]{
		ID:            generated,
		Schema:        contractsv1.DeviceEventSchemaID,
		EmittedAt:     nowString(),
		CorrelationID: correlationID,
		Data:          wireDeviceEvent(event),
	}
	payload, err := natswire.Encode(session.validator, contractsv1.DeviceEventSchemaID, envelope)
	if err != nil {
		return deviceEventID, &ValidationError{Err: err}
	}
	subject, err := natswire.DeviceEventSubject(session.adapterID, session.runtimeID, event.EntityID)
	if err != nil {
		return deviceEventID, &ValidationError{Err: err}
	}
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, generated)
	natswire.InjectTrace(publicationContext, headers)

	//nolint:dupl // One encoded report stays one report across every transient retry.
	for {
		message := &natsgo.Msg{Subject: subject, Header: headers, Data: payload}
		if _, err = session.jetstream.PublishMsg(publicationContext, message); err == nil {
			if terminalErr := session.sessionError(); terminalErr != nil {
				return deviceEventID, terminalErr
			}
			session.logPublishedDeviceEvent(publicationContext, deviceEventID, correlationID, event)
			return deviceEventID, nil
		}
		if terminalErr := session.sessionError(); terminalErr != nil {
			return deviceEventID, terminalErr
		}
		if ctxErr := publicationContext.Err(); ctxErr != nil {
			return deviceEventID, ctxErr
		}
		if !isTransientPublishError(err) {
			return deviceEventID, err
		}
		session.log().DebugContext(publicationContext, "retrying operation",
			slog.String("event", "dependency.retrying"),
			slog.String("operation", "device_event_publish"),
			slog.String("dependency", "nats"),
			slog.String("error_code", requestErrorCode(err)),
		)
		timer := time.NewTimer(requestRetryWait)
		select {
		case <-publicationContext.Done():
			timer.Stop()
			if terminalErr := session.sessionError(); terminalErr != nil {
				return deviceEventID, terminalErr
			}
			return deviceEventID, publicationContext.Err()
		case <-timer.C:
		}
	}
}

// logPublishedDeviceEvent emits the acknowledged publication evidence. The
// record carries only bounded identity and the schema-constrained name, never
// the encoded envelope.
func (session *Session) logPublishedDeviceEvent(
	publicationContext context.Context,
	deviceEventID DeviceEventID,
	correlationID string,
	event DeviceEvent,
) {
	session.log().LogAttrs(publicationContext, slog.LevelDebug, "device event published",
		slog.String("event", "device_event.published"),
		slog.String("device_event_id", string(deviceEventID)),
		slog.String("entity_id", event.EntityID),
		slog.String("name", event.Name),
		slog.String("correlation_id", correlationID),
	)
}
