package nats

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Poison stages reported by device_fact.poison. Every poison diagnostic names
// the stage that rejected the pending row: mapping one stored row, encoding its
// payload, or reading the durable outbox at all.
const (
	deviceFactStageMap    = "map"
	deviceFactStageEncode = "encode"
)

// Fixed poison codes. A poison failure is deterministic: the same stored row
// fails the same way on every attempt, so the relay must never retry it, never
// delete it and never publish a partially mapped substitute.
const (
	deviceFactCodeFactInvalid    = "fact_invalid"
	deviceFactCodeSubjectInvalid = "subject_invalid"
	deviceFactCodeEncodeFailed   = "encode_failed"
	deviceFactCodeUnknownFamily  = "unknown_family"
	deviceFactCodeInvalidRow     = "invalid_row"
)

// W3C trace header keys. The persisted traceparent and tracestate are restored
// under these names, which are the names natswire's propagation carrier reads.
const (
	traceparentHeaderKey = "traceparent"
	tracestateHeaderKey  = "tracestate"
)

// observationFactData is the strict external Observation fact payload. It
// carries canonical committed data only: no Adapter or runtime identity, and no
// rejected or duplicate input.
type observationFactData struct {
	ObservationID     string          `json:"observation_id"`
	EntityID          string          `json:"entity_id"`
	Disposition       string          `json:"disposition"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	ObservedAt        string          `json:"observed_at"`
}

// entityEventFactData is the strict external Entity Event fact payload.
type entityEventFactData struct {
	EventID    string `json:"event_id"`
	EntityID   string `json:"entity_id"`
	Name       string `json:"name"`
	ReportedAt string `json:"reported_at"`
	ReceivedAt string `json:"received_at"`
	RecordedAt string `json:"recorded_at"`
}

// deviceFactMessage is one pending fact mapped to the exact bytes and headers
// the relay publishes. Mapping is a pure function of the stored row, so retrying
// one pending fact reuses its identity, subject and payload byte for byte.
type deviceFactMessage struct {
	factID      devices.DeviceFactID
	family      devices.DeviceFactFamily
	sourceID    string
	subject     string
	payload     []byte
	traceparent string
	tracestate  string
}

// natsMessage builds the publication message: the mapped subject and payload,
// the stored trace context, the stable Nats-Msg-Id that makes a retried publish
// a broker-side duplicate, and the expected stream that rejects a publication
// into any other stream.
func (message deviceFactMessage) natsMessage() *natsgo.Msg {
	headers := make(natsgo.Header)
	headers.Set(natsgo.MsgIdHdr, string(message.factID))
	headers.Set(natsgo.ExpectedStreamHdr, DeviceFactStreamName)
	if message.traceparent != "" {
		headers.Set(traceparentHeaderKey, message.traceparent)
	}
	if message.tracestate != "" {
		headers.Set(tracestateHeaderKey, message.tracestate)
	}
	return &natsgo.Msg{Subject: message.subject, Header: headers, Data: message.payload}
}

// deviceFactPoisonError is the permanent failure class of one pending outbox
// row. It is deterministic, so nothing about the row may be retried, rewritten
// or dropped; the relay faults and leaves the row for an operator. It covers
// both a row Core cannot map and a row Core cannot even decode from the durable
// outbox.
type deviceFactPoisonError struct {
	factID   string
	family   devices.DeviceFactFamily
	sourceID string
	stage    string
	code     string
	cause    error
}

func (poisonErr *deviceFactPoisonError) Error() string {
	row := poisonErr.factID
	if poisonErr.family != "" {
		row = fmt.Sprintf("%s/%s", poisonErr.factID, poisonErr.family)
	}
	return fmt.Sprintf(
		"device fact poison %s (stage %s, code %s): %v",
		row, poisonErr.stage, poisonErr.code, poisonErr.cause,
	)
}

func (poisonErr *deviceFactPoisonError) Unwrap() error { return poisonErr.cause }

// poison builds the permanent failure for the message's own identity, so a
// fault diagnostic names the stored row without depending on raw error text.
func (message deviceFactMessage) poison(stage string, code string, cause error) *deviceFactPoisonError {
	return &deviceFactPoisonError{
		factID:   string(message.factID),
		family:   message.family,
		sourceID: message.sourceID,
		stage:    stage,
		code:     code,
		cause:    cause,
	}
}

// asDeviceFactPoisonError reports whether an error is the permanent poison
// class, so the relay can distinguish a poison row from a retryable one.
func asDeviceFactPoisonError(err error) *deviceFactPoisonError {
	poisonErr, ok := errors.AsType[*deviceFactPoisonError](err)
	if !ok {
		return nil
	}
	return poisonErr
}

// asInvalidOutboxRowPoison converts a permanent outbox decode failure into the
// poison class the relay faults on, so a row whose stored bytes Core cannot read
// stops the relay and is preserved exactly like a row Core cannot map. It names
// the raw stored fact identity in the diagnostic. A transient read failure does
// not carry the permanent row class, so it returns nil and stays retryable.
func asInvalidOutboxRowPoison(err error) *deviceFactPoisonError {
	rowErr, ok := errors.AsType[*devices.DeviceFactRowError](err)
	if !ok {
		return nil
	}
	return &deviceFactPoisonError{
		factID: rowErr.FactID,
		stage:  deviceFactStageList,
		code:   deviceFactCodeInvalidRow,
		cause:  rowErr,
	}
}

// mapPendingDeviceFact maps one typed pending fact to its stable strict wire
// message. It is a pure function of the stored row: the same row always maps to
// the same identity, subject and payload, so a retry is byte-for-byte stable and
// the broker's Nats-Msg-Id duplicate window collapses it into one stored fact
// for as long as that window lasts.
//
// Mapping is at-least-once: a retry after the window has elapsed is stored
// again, so a reader must stay idempotent on the stable fact identity.
func mapPendingDeviceFact(
	validator *contractsv1.Validator,
	pending devices.PendingDeviceFact,
) (deviceFactMessage, error) {
	switch fact := pending.Fact.(type) {
	case devices.ObservationFact:
		return mapObservationDeviceFact(validator, fact)
	case devices.EntityEventFact:
		return mapEntityEventDeviceFact(validator, fact)
	default:
		return deviceFactMessage{}, &deviceFactPoisonError{
			stage: deviceFactStageMap,
			code:  deviceFactCodeUnknownFamily,
			cause: errors.New("pending device fact carries no known family"),
		}
	}
}

func mapObservationDeviceFact(
	validator *contractsv1.Validator,
	fact devices.ObservationFact,
) (deviceFactMessage, error) {
	message := deviceFactMessage{
		factID:      fact.ID,
		family:      devices.DeviceFactFamilyObservation,
		sourceID:    string(fact.ObservationID),
		traceparent: fact.Trace.Traceparent,
		tracestate:  fact.Trace.Tracestate,
	}
	if invalidErr := invalidObservationDeviceFact(fact); invalidErr != nil {
		return deviceFactMessage{}, message.poison(deviceFactStageMap, deviceFactCodeFactInvalid, invalidErr)
	}
	subject, subjectErr := natswire.ObservationFactSubject(string(fact.EntityID), string(fact.Disposition))
	if subjectErr != nil {
		return deviceFactMessage{}, message.poison(deviceFactStageMap, deviceFactCodeSubjectInvalid, subjectErr)
	}
	message.subject = subject
	data := observationFactData{
		ObservationID:     string(fact.ObservationID),
		EntityID:          string(fact.EntityID),
		Disposition:       string(fact.Disposition),
		Value:             json.RawMessage(fact.Value),
		AdapterReceivedAt: formatFactTime(fact.AdapterReceivedAt),
		ObservedAt:        formatFactTime(fact.ObservedAt),
	}
	if fact.SourceUpdatedAt != nil {
		formatted := formatFactTime(*fact.SourceUpdatedAt)
		data.SourceUpdatedAt = &formatted
	}
	payload, encodeErr := natswire.Encode(
		validator,
		contractsv1.ObservationFactSchemaID,
		natswire.Envelope[observationFactData]{
			ID:            string(fact.ID),
			Schema:        contractsv1.ObservationFactSchemaID,
			EmittedAt:     formatFactTime(fact.CreatedAt),
			CorrelationID: string(fact.CorrelationID),
			CausationID:   new(string(fact.ObservationID)),
			Data:          data,
		},
	)
	if encodeErr != nil {
		return deviceFactMessage{}, message.poison(deviceFactStageEncode, deviceFactCodeEncodeFailed, encodeErr)
	}
	message.payload = payload
	return message, nil
}

func mapEntityEventDeviceFact(
	validator *contractsv1.Validator,
	fact devices.EntityEventFact,
) (deviceFactMessage, error) {
	message := deviceFactMessage{
		factID:      fact.ID,
		family:      devices.DeviceFactFamilyEntityEvent,
		sourceID:    string(fact.EventID),
		traceparent: fact.Trace.Traceparent,
		tracestate:  fact.Trace.Tracestate,
	}
	if invalidErr := invalidEntityEventDeviceFact(fact); invalidErr != nil {
		return deviceFactMessage{}, message.poison(deviceFactStageMap, deviceFactCodeFactInvalid, invalidErr)
	}
	subject, subjectErr := natswire.EntityEventFactSubject(string(fact.EntityID), string(fact.Name))
	if subjectErr != nil {
		return deviceFactMessage{}, message.poison(deviceFactStageMap, deviceFactCodeSubjectInvalid, subjectErr)
	}
	message.subject = subject
	payload, encodeErr := natswire.Encode(
		validator,
		contractsv1.EntityEventFactSchemaID,
		natswire.Envelope[entityEventFactData]{
			ID:            string(fact.ID),
			Schema:        contractsv1.EntityEventFactSchemaID,
			EmittedAt:     formatFactTime(fact.CreatedAt),
			CorrelationID: string(fact.CorrelationID),
			CausationID:   new(string(fact.EventID)),
			Data: entityEventFactData{
				EventID:    string(fact.EventID),
				EntityID:   string(fact.EntityID),
				Name:       string(fact.Name),
				ReportedAt: formatFactTime(fact.ReportedAt),
				ReceivedAt: formatFactTime(fact.ReceivedAt),
				RecordedAt: formatFactTime(fact.RecordedAt),
			},
		},
	)
	if encodeErr != nil {
		return deviceFactMessage{}, message.poison(deviceFactStageEncode, deviceFactCodeEncodeFailed, encodeErr)
	}
	message.payload = payload
	return message, nil
}

// invalidObservationDeviceFact rejects the missing or zero stored fields that
// would otherwise silently produce a wire fact claiming absent evidence. The
// strict schema catches the rest, so this check is deliberately the smallest set
// that keeps a zero value from being published as if it were recorded.
func invalidObservationDeviceFact(fact devices.ObservationFact) error {
	switch {
	case fact.ID == "":
		return errors.New("device fact identity is empty")
	case fact.ObservationID == "":
		return errors.New("observation identity is empty")
	case fact.CreatedAt.IsZero():
		return errors.New("device fact commit time is zero")
	case fact.AdapterReceivedAt.IsZero():
		return errors.New("adapter receive time is zero")
	case fact.ObservedAt.IsZero():
		return errors.New("observation storage time is zero")
	case len(fact.Value) == 0:
		return errors.New("observation value is empty")
	}
	return nil
}

// invalidEntityEventDeviceFact rejects the missing or zero stored fields that
// would otherwise silently publish a fact claiming absent evidence.
func invalidEntityEventDeviceFact(fact devices.EntityEventFact) error {
	switch {
	case fact.ID == "":
		return errors.New("device fact identity is empty")
	case fact.EventID == "":
		return errors.New("entity event identity is empty")
	case fact.CreatedAt.IsZero():
		return errors.New("device fact commit time is zero")
	case fact.ReportedAt.IsZero():
		return errors.New("entity event reported time is zero")
	case fact.ReceivedAt.IsZero():
		return errors.New("entity event storage time is zero")
	case fact.RecordedAt.IsZero():
		return errors.New("entity event record time is zero")
	}
	return nil
}

// formatFactTime renders one required committed timestamp as the canonical UTC
// RFC 3339 form the strict fact schemas require.
func formatFactTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
