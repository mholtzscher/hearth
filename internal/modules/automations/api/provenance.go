package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AdmissionCauseBody is one explicit public provenance variant.
type AdmissionCauseBody struct{ Variant AdmissionCauseVariant }

// AdmissionCauseVariant is a concrete public provenance DTO.
//
//sumtype:decl
type AdmissionCauseVariant interface{ isAdmissionCauseBody() }

type ManualCauseBody struct {
	Kind string `json:"kind" enum:"manual"`
}
type ScheduleCauseBody struct {
	Kind string `json:"kind" enum:"schedule"`
}
type DeviceFactCauseBody struct {
	Kind string         `json:"kind" enum:"device_fact"`
	Fact DeviceFactBody `json:"fact"`
}
type HeldStateCauseBody struct {
	Kind     string                `json:"kind"     enum:"held_state"`
	Evidence HeldStateEvidenceBody `json:"evidence"`
}

func (ManualCauseBody) isAdmissionCauseBody()     {}
func (ScheduleCauseBody) isAdmissionCauseBody()   {}
func (DeviceFactCauseBody) isAdmissionCauseBody() {}
func (HeldStateCauseBody) isAdmissionCauseBody()  {}

// DeviceFactBody retains the family-specific identity and complete evidence.
type DeviceFactBody struct{ Variant DeviceFactVariant }

// DeviceFactVariant is a concrete public Fact DTO.
//
//sumtype:decl
type DeviceFactVariant interface{ isDeviceFactBody() }

type ObservationFactBody struct {
	FactID        string          `json:"fact_id"`
	Family        string          `json:"family"                   enum:"observation"`
	EntityID      string          `json:"entity_id"`
	EmittedAt     time.Time       `json:"emitted_at"`
	ObservationID string          `json:"observation_id"`
	Disposition   string          `json:"disposition"`
	Value         json.RawMessage `json:"value"`
	PreviousValue json.RawMessage `json:"previous_value,omitempty"`
}
type EntityEventFactBody struct {
	FactID    string    `json:"fact_id"`
	Family    string    `json:"family"     enum:"entity_event"`
	EntityID  string    `json:"entity_id"`
	EmittedAt time.Time `json:"emitted_at"`
	EventID   string    `json:"event_id"`
	Name      string    `json:"name"`
}

func (ObservationFactBody) isDeviceFactBody() {}
func (EntityEventFactBody) isDeviceFactBody() {}

func admissionCauseBody(cause automations.AdmissionCause) AdmissionCauseBody {
	switch cause := cause.(type) {
	case automations.ManualCause:
		return AdmissionCauseBody{Variant: ManualCauseBody{Kind: "manual"}}
	case automations.ScheduleCause:
		return AdmissionCauseBody{Variant: ScheduleCauseBody{Kind: "schedule"}}
	case automations.DeviceFactCause:
		return AdmissionCauseBody{Variant: DeviceFactCauseBody{Kind: "device_fact", Fact: deviceFactBody(cause.Fact)}}
	case automations.HeldStateCause:
		return AdmissionCauseBody{
			Variant: HeldStateCauseBody{Kind: "held_state", Evidence: heldStateEvidenceBody(cause.Evidence)},
		}
	default:
		panic("invalid retained admission cause")
	}
}

func deviceFactBody(fact automations.DeviceFact) DeviceFactBody {
	switch fact := fact.(type) {
	case automations.ObservationFact:
		return DeviceFactBody{Variant: ObservationFactBody{
			FactID: string(
				fact.FactID,
			),
			Family:        "observation",
			EntityID:      string(fact.EntityID),
			EmittedAt:     fact.EmittedAt,
			ObservationID: string(fact.ObservationID),
			Disposition:   string(fact.Disposition),
			Value: append(
				json.RawMessage(nil),
				fact.Value...),
			PreviousValue: append(json.RawMessage(nil), fact.PreviousValue...),
		}}
	case automations.EntityEventFact:
		return DeviceFactBody{Variant: EntityEventFactBody{
			FactID: string(
				fact.FactID,
			),
			Family:    "entity_event",
			EntityID:  string(fact.EntityID),
			EmittedAt: fact.EmittedAt,
			EventID:   string(fact.EventID),
			Name:      string(fact.Name),
		}}
	default:
		panic("invalid retained Device Fact")
	}
}

func (AdmissionCauseBody) Schema(registry huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		registry.Schema(reflect.TypeFor[ManualCauseBody](), true, ""),
		registry.Schema(reflect.TypeFor[ScheduleCauseBody](), true, ""),
		registry.Schema(reflect.TypeFor[DeviceFactCauseBody](), true, ""),
		registry.Schema(reflect.TypeFor[HeldStateCauseBody](), true, ""),
	}}
}

func (DeviceFactBody) Schema(registry huma.Registry) *huma.Schema {
	return &huma.Schema{OneOf: []*huma.Schema{
		registry.Schema(reflect.TypeFor[ObservationFactBody](), true, ""),
		registry.Schema(reflect.TypeFor[EntityEventFactBody](), true, ""),
	}}
}

func (body AdmissionCauseBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case ManualCauseBody, ScheduleCauseBody, DeviceFactCauseBody, HeldStateCauseBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid Cause DTO %T", body.Variant)
	}
}

func (body DeviceFactBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case ObservationFactBody, EntityEventFactBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid Fact DTO %T", body.Variant)
	}
}
