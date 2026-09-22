package nats

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// observationFactData is the strict external Observation Fact payload carrying
// canonical committed data only.
type observationFactData struct {
	ObservationID     string          `json:"observation_id"`
	EntityID          string          `json:"entity_id"`
	Disposition       string          `json:"disposition"`
	Value             json.RawMessage `json:"value"`
	PreviousValue     json.RawMessage `json:"previous_value,omitempty"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	ObservedAt        string          `json:"observed_at"`
}

// entityEventFactData is the strict external Entity Event Fact payload.
type entityEventFactData struct {
	EventID    string `json:"event_id"`
	EntityID   string `json:"entity_id"`
	Name       string `json:"name"`
	ReportedAt string `json:"reported_at"`
	ReceivedAt string `json:"received_at"`
	RecordedAt string `json:"recorded_at"`
}

// Fixed wire-rejection codes are deterministic, so the consumer terminates rather than retries.
const (
	wireCodeSubjectInvalid   = "fact_subject_invalid"
	wireCodeFamilyInvalid    = "fact_family_invalid"
	wireCodeDecodeFailed     = "fact_decode_failed"
	wireCodeMsgIDMismatch    = "fact_msg_id_mismatch"
	wireCodeSubjectMismatch  = "fact_subject_mismatch"
	wireCodeCausationMissing = "fact_causation_mismatch"
	wireCodeIdentityInvalid  = "fact_identity_invalid"
	wireCodeEmittedAtInvalid = "fact_emitted_at_invalid"
	wireCodeFactInvalid      = "fact_invalid"
)

// deviceFactWireMessage groups the subject, message ID, and payload for agreement checks.
type deviceFactWireMessage struct {
	subject   string
	messageID string
	payload   []byte
}

// wireRejectionError marks malformed wire input that redelivery cannot repair.
type wireRejectionError struct {
	code  string
	cause error
}

func (rejection *wireRejectionError) Error() string {
	return fmt.Sprintf("device fact rejected (%s): %v", rejection.code, rejection.cause)
}

func (rejection *wireRejectionError) Unwrap() error { return rejection.cause }

func reject(code string, cause error) *wireRejectionError {
	return &wireRejectionError{code: code, cause: cause}
}

// asWireRejection distinguishes permanent wire rejection from retryable admission failures.
func asWireRejection(err error) *wireRejectionError {
	rejection, ok := errors.AsType[*wireRejectionError](err)
	if !ok {
		return nil
	}
	return rejection
}

// rejectionCode returns the fixed code of one wire rejection.
func rejectionCode(err error) string {
	if rejection := asWireRejection(err); rejection != nil {
		return rejection.code
	}
	return wireCodeFactInvalid
}

// mapDeviceFactMessage validates the route, strict schema, and subject, payload,
// Nats-Msg-Id, and causation agreement.
func mapDeviceFactMessage(
	validator *contractsv1.Validator,
	wire deviceFactWireMessage,
) (automations.DeviceFact, error) {
	route, routeErr := natswire.ParseDeviceFactSubject(wire.subject)
	if routeErr != nil {
		return automations.DeviceFact{}, reject(wireCodeSubjectInvalid, routeErr)
	}
	switch route.Family {
	case natswire.DeviceFactFamilyObservation:
		return mapObservationDeviceFact(validator, wire, route)
	case natswire.DeviceFactFamilyEntityEvent:
		return mapEntityEventDeviceFact(validator, wire, route)
	default:
		return automations.DeviceFact{}, reject(
			wireCodeFamilyInvalid, errors.New("device fact subject carries an unsupported family"),
		)
	}
}

// deviceFactEnvelope carries the decoded fields the shared wire check needs.
type deviceFactEnvelope struct {
	messageID        string
	envelopeID       string
	causationID      *string
	correlationID    string
	emittedAt        string
	route            natswire.DeviceFactRoute
	payloadEntityID  string
	payloadVariant   string
	causationTarget  string
	causationMessage string
}

// checkDeviceFactEnvelope verifies the agreement and shared identity fields
// common to both Device Fact families.
func checkDeviceFactEnvelope(
	fields deviceFactEnvelope,
) (devices.DeviceFactID, devices.EntityID, time.Time, error) {
	if fields.messageID != fields.envelopeID {
		return "", "", time.Time{}, reject(
			wireCodeMsgIDMismatch, errors.New("Nats-Msg-Id does not match the envelope identity"),
		)
	}
	if fields.route.EntityID != fields.payloadEntityID || fields.route.Variant != fields.payloadVariant {
		return "", "", time.Time{}, reject(
			wireCodeSubjectMismatch, errors.New("device fact subject disagrees with its payload"),
		)
	}
	if fields.causationID == nil || *fields.causationID != fields.causationTarget {
		return "", "", time.Time{}, reject(wireCodeCausationMissing, errors.New(fields.causationMessage))
	}
	factID, factIDErr := devices.ParseDeviceFactID(fields.envelopeID)
	entityID, entityIDErr := devices.ParseEntityID(fields.payloadEntityID)
	_, correlationErr := devices.ParseCorrelationID(fields.correlationID)
	if identityErr := errors.Join(factIDErr, entityIDErr, correlationErr); identityErr != nil {
		return "", "", time.Time{}, reject(wireCodeIdentityInvalid, identityErr)
	}
	emittedAt, emittedAtErr := time.Parse(time.RFC3339Nano, fields.emittedAt)
	if emittedAtErr != nil {
		return "", "", time.Time{}, reject(wireCodeEmittedAtInvalid, emittedAtErr)
	}
	return factID, entityID, emittedAt.UTC(), nil
}

// mapObservationDeviceFact decodes against the route's strict Observation schema.
func mapObservationDeviceFact(
	validator *contractsv1.Validator,
	wire deviceFactWireMessage,
	route natswire.DeviceFactRoute,
) (automations.DeviceFact, error) {
	envelope, decodeErr := natswire.Decode[observationFactData](
		validator, contractsv1.ObservationFactSchemaID, wire.payload,
	)
	if decodeErr != nil {
		return automations.DeviceFact{}, reject(wireCodeDecodeFailed, decodeErr)
	}
	factID, entityID, emittedAt, err := checkDeviceFactEnvelope(deviceFactEnvelope{
		messageID:        wire.messageID,
		envelopeID:       envelope.ID,
		causationID:      envelope.CausationID,
		correlationID:    envelope.CorrelationID,
		emittedAt:        envelope.EmittedAt,
		route:            route,
		payloadEntityID:  envelope.Data.EntityID,
		payloadVariant:   envelope.Data.Disposition,
		causationTarget:  envelope.Data.ObservationID,
		causationMessage: "observation fact causation does not name its observation",
	})
	if err != nil {
		return automations.DeviceFact{}, err
	}
	observationID, err := devices.ParseObservationID(envelope.Data.ObservationID)
	if err != nil {
		return automations.DeviceFact{}, reject(wireCodeIdentityInvalid, err)
	}
	fact := automations.DeviceFact{
		Family: automations.DeviceFactObservation,
		Observation: &automations.ObservationFact{
			FactID:        factID,
			ObservationID: observationID,
			EntityID:      entityID,
			Disposition:   devices.ObservationDisposition(envelope.Data.Disposition),
			Value:         append(devices.Value(nil), envelope.Data.Value...),
			PreviousValue: append(devices.Value(nil), envelope.Data.PreviousValue...),
			EmittedAt:     emittedAt,
		},
	}
	if validationErr := automations.ValidateDeviceFact(fact); validationErr != nil {
		return automations.DeviceFact{}, reject(wireCodeFactInvalid, validationErr)
	}
	return fact, nil
}

// mapEntityEventDeviceFact decodes against the route's strict Entity Event schema.
func mapEntityEventDeviceFact(
	validator *contractsv1.Validator,
	wire deviceFactWireMessage,
	route natswire.DeviceFactRoute,
) (automations.DeviceFact, error) {
	envelope, decodeErr := natswire.Decode[entityEventFactData](
		validator, contractsv1.EntityEventFactSchemaID, wire.payload,
	)
	if decodeErr != nil {
		return automations.DeviceFact{}, reject(wireCodeDecodeFailed, decodeErr)
	}
	factID, entityID, emittedAt, err := checkDeviceFactEnvelope(deviceFactEnvelope{
		messageID:        wire.messageID,
		envelopeID:       envelope.ID,
		causationID:      envelope.CausationID,
		correlationID:    envelope.CorrelationID,
		emittedAt:        envelope.EmittedAt,
		route:            route,
		payloadEntityID:  envelope.Data.EntityID,
		payloadVariant:   envelope.Data.Name,
		causationTarget:  envelope.Data.EventID,
		causationMessage: "entity event fact causation does not name its entity event",
	})
	if err != nil {
		return automations.DeviceFact{}, err
	}
	eventID, err := devices.ParseEntityEventID(envelope.Data.EventID)
	if err != nil {
		return automations.DeviceFact{}, reject(wireCodeIdentityInvalid, err)
	}
	fact := automations.DeviceFact{
		Family: automations.DeviceFactEntityEvent,
		EntityEvent: &automations.EntityEventFact{
			FactID:    factID,
			EventID:   eventID,
			EntityID:  entityID,
			Name:      devices.EntityEventName(envelope.Data.Name),
			EmittedAt: emittedAt,
		},
	}
	if validationErr := automations.ValidateDeviceFact(fact); validationErr != nil {
		return automations.DeviceFact{}, reject(wireCodeFactInvalid, validationErr)
	}
	return fact, nil
}
