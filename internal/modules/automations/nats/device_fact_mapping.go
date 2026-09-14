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

// observationFactData is the strict external Observation Fact payload. It
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

// entityEventFactData is the strict external Entity Event Fact payload.
type entityEventFactData struct {
	EventID    string `json:"event_id"`
	EntityID   string `json:"entity_id"`
	Name       string `json:"name"`
	ReportedAt string `json:"reported_at"`
	ReceivedAt string `json:"received_at"`
	RecordedAt string `json:"recorded_at"`
}

// Fixed wire-rejection codes keep diagnostics free of payloads and error text.
// These failures are deterministic, so the consumer terminates rather than retries.
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

// rejectionCode returns the fixed code of one wire rejection, or the generic
// fact-invalid code when the error is not a named rejection.
func rejectionCode(err error) string {
	if rejection := asWireRejection(err); rejection != nil {
		return rejection.code
	}
	return wireCodeFactInvalid
}

// mapDeviceFactMessage validates the route, strict schema, and agreement of
// subject, payload, Nats-Msg-Id, and causation before admission. Failures are permanent.
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
	if wire.messageID != envelope.ID {
		return automations.DeviceFact{}, reject(
			wireCodeMsgIDMismatch, errors.New("Nats-Msg-Id does not match the envelope identity"),
		)
	}
	if route.EntityID != envelope.Data.EntityID || route.Variant != envelope.Data.Disposition {
		return automations.DeviceFact{}, reject(
			wireCodeSubjectMismatch, errors.New("device fact subject disagrees with its payload"),
		)
	}
	if envelope.CausationID == nil || *envelope.CausationID != envelope.Data.ObservationID {
		return automations.DeviceFact{}, reject(
			wireCodeCausationMissing,
			errors.New("observation fact causation does not name its observation"),
		)
	}
	factID, factIDErr := devices.ParseDeviceFactID(envelope.ID)
	observationID, observationIDErr := devices.ParseObservationID(envelope.Data.ObservationID)
	entityID, entityIDErr := devices.ParseEntityID(envelope.Data.EntityID)
	_, correlationErr := devices.ParseCorrelationID(envelope.CorrelationID)
	identityErr := errors.Join(factIDErr, observationIDErr, entityIDErr, correlationErr)
	if identityErr != nil {
		return automations.DeviceFact{}, reject(wireCodeIdentityInvalid, identityErr)
	}
	emittedAt, emittedAtErr := time.Parse(time.RFC3339Nano, envelope.EmittedAt)
	if emittedAtErr != nil {
		return automations.DeviceFact{}, reject(wireCodeEmittedAtInvalid, emittedAtErr)
	}
	fact := automations.DeviceFact{
		Family: automations.DeviceFactObservation,
		Observation: &automations.ObservationFact{
			FactID:        factID,
			ObservationID: observationID,
			EntityID:      entityID,
			Disposition:   devices.ObservationDisposition(envelope.Data.Disposition),
			Value:         append(devices.Value(nil), envelope.Data.Value...),
			EmittedAt:     emittedAt.UTC(),
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
	if wire.messageID != envelope.ID {
		return automations.DeviceFact{}, reject(
			wireCodeMsgIDMismatch, errors.New("Nats-Msg-Id does not match the envelope identity"),
		)
	}
	if route.EntityID != envelope.Data.EntityID || route.Variant != envelope.Data.Name {
		return automations.DeviceFact{}, reject(
			wireCodeSubjectMismatch, errors.New("device fact subject disagrees with its payload"),
		)
	}
	if envelope.CausationID == nil || *envelope.CausationID != envelope.Data.EventID {
		return automations.DeviceFact{}, reject(
			wireCodeCausationMissing,
			errors.New("entity event fact causation does not name its entity event"),
		)
	}
	factID, factIDErr := devices.ParseDeviceFactID(envelope.ID)
	eventID, eventIDErr := devices.ParseEntityEventID(envelope.Data.EventID)
	entityID, entityIDErr := devices.ParseEntityID(envelope.Data.EntityID)
	_, correlationErr := devices.ParseCorrelationID(envelope.CorrelationID)
	identityErr := errors.Join(factIDErr, eventIDErr, entityIDErr, correlationErr)
	if identityErr != nil {
		return automations.DeviceFact{}, reject(wireCodeIdentityInvalid, identityErr)
	}
	emittedAt, emittedAtErr := time.Parse(time.RFC3339Nano, envelope.EmittedAt)
	if emittedAtErr != nil {
		return automations.DeviceFact{}, reject(wireCodeEmittedAtInvalid, emittedAtErr)
	}
	fact := automations.DeviceFact{
		Family: automations.DeviceFactEntityEvent,
		EntityEvent: &automations.EntityEventFact{
			FactID:    factID,
			EventID:   eventID,
			EntityID:  entityID,
			Name:      devices.EntityEventName(envelope.Data.Name),
			EmittedAt: emittedAt.UTC(),
		},
	}
	if validationErr := automations.ValidateDeviceFact(fact); validationErr != nil {
		return automations.DeviceFact{}, reject(wireCodeFactInvalid, validationErr)
	}
	return fact, nil
}
