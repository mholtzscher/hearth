package nats //nolint:testpackage // Tests exercise package-private strict mapping.

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// rawDeviceFactSubject bypasses subject validation for malformed fixtures.
func rawDeviceFactSubject(entityID, family, variant string) string {
	return "hearth.v1.core.fact.entity." + entityID + "." + family + "." + variant
}

// Observation mapping must preserve identity, disposition, value, and emit time.
func TestMapObservationDeviceFactMapsExactEvidence(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	message := observationFactMessage(t, validator, defaultObservationFactInput())

	fact, err := mapDeviceFactMessage(validator, message.asWire())
	if err != nil {
		t.Fatal(err)
	}
	if fact.Family != automations.DeviceFactObservation || fact.Observation == nil || fact.EntityEvent != nil {
		t.Fatalf("mapped family payload = %#v", fact)
	}
	observation := fact.Observation
	emittedAt, parseErr := time.Parse(time.RFC3339Nano, testEmittedAtString)
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	switch {
	case observation.FactID != devices.DeviceFactID(testFactOneID):
		t.Fatalf("fact id = %q", observation.FactID)
	case observation.ObservationID != devices.ObservationID(testObservationID):
		t.Fatalf("observation id = %q", observation.ObservationID)
	case observation.EntityID != devices.EntityID(testEntityAID):
		t.Fatalf("entity id = %q", observation.EntityID)
	case observation.Disposition != devices.DispositionApplied:
		t.Fatalf("disposition = %q", observation.Disposition)
	case !bytes.Equal(observation.Value, []byte(`{"on":true,"level":42}`)):
		t.Fatalf("value = %s", observation.Value)
	case !observation.EmittedAt.Equal(emittedAt):
		t.Fatalf("emitted at = %s, want %s", observation.EmittedAt, emittedAt)
	}
}

func TestMapObservationDeviceFactPreservesAbsentAndNullPredecessors(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	for _, test := range []struct {
		name     string
		previous json.RawMessage
		want     []byte
	}{
		{name: "absent"},
		{name: "JSON null", previous: json.RawMessage("null"), want: []byte("null")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := defaultObservationFactInput()
			input.previousValue = test.previous
			message := observationFactMessage(t, validator, input)
			fact, err := mapDeviceFactMessage(validator, message.asWire())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(fact.Observation.PreviousValue, test.want) {
				t.Fatalf("previous value = %q, want %q", fact.Observation.PreviousValue, test.want)
			}
			if (fact.Observation.PreviousValue == nil) != (test.previous == nil) {
				t.Fatalf(
					"previous value presence = %v, want %v",
					fact.Observation.PreviousValue != nil, test.previous != nil,
				)
			}
		})
	}
}

// Entity Event mapping must preserve identity, event name, and emit time.
func TestMapEntityEventDeviceFactMapsExactEvidence(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	message := entityEventFactMessage(t, validator, defaultEntityEventFactInput())

	fact, err := mapDeviceFactMessage(validator, message.asWire())
	if err != nil {
		t.Fatal(err)
	}
	if fact.Family != automations.DeviceFactEntityEvent || fact.EntityEvent == nil || fact.Observation != nil {
		t.Fatalf("mapped family payload = %#v", fact)
	}
	event := fact.EntityEvent
	switch {
	case event.FactID != devices.DeviceFactID(testFactTwoID):
		t.Fatalf("fact id = %q", event.FactID)
	case event.EventID != devices.EntityEventID(testEntityEventID):
		t.Fatalf("event id = %q", event.EventID)
	case event.EntityID != devices.EntityID(testEntityAID):
		t.Fatalf("entity id = %q", event.EntityID)
	case event.Name != devices.EntityEventName(testEventName):
		t.Fatalf("event name = %q", event.Name)
	case event.EmittedAt.IsZero():
		t.Fatal("emit time is zero")
	}
}

// Malformed or inconsistent wire input must produce a permanent rejection.
func TestMapDeviceFactMessageRejectsDeterministicWireInput(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	mismatchedMessageID := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.messageID = testFactTwoID
	}))
	entityMismatch := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.payloadEntityID = testEntityBID
	}))
	variantMismatch := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.subjectVariant = string(devices.DispositionUnchanged)
	}))
	causationMismatch := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.causationID = new(testOtherObsID)
	}))
	eventCausationMismatch := entityEventFactMessage(t, validator, entityMutation(func(input *entityEventFactInput) {
		input.causationID = new(testOtherEventID)
	}))
	eventNameMismatch := entityEventFactMessage(t, validator, entityMutation(func(input *entityEventFactInput) {
		input.subjectVariant = "double_press"
	}))
	eventOnObservationSubject := entityEventFactMessage(t, validator, defaultEntityEventFactInput())
	eventOnObservationSubject.subject = rawDeviceFactSubject(testEntityAID, "observation", "applied")
	unknownFamily := observationFactMessage(t, validator, defaultObservationFactInput())
	unknownFamily.subject = rawDeviceFactSubject(testEntityAID, "command", "applied")
	unknownVariant := observationFactMessage(t, validator, defaultObservationFactInput())
	unknownVariant.subject = rawDeviceFactSubject(testEntityAID, "observation", "rejected")
	unsafeEntity := observationFactMessage(t, validator, defaultObservationFactInput())
	unsafeEntity.subject = rawDeviceFactSubject("ent_not-a-uuid", "observation", "applied")
	unsafeVersionIdentity := observationFactMessage(t, validator, defaultObservationFactInput())
	unsafeVersionIdentity.payload = bytes.ReplaceAll(
		unsafeVersionIdentity.payload,
		[]byte(testFactOneID),
		[]byte("fct_01890f47-7a6b-4c4d-8e9f-0123456789b1"),
	)

	tests := []struct {
		name    string
		message testDeviceFactMessage
		want    string
	}{
		{"malformed payload", testDeviceFactMessage{
			subject:   rawDeviceFactSubject(testEntityAID, "observation", "applied"),
			messageID: testFactOneID,
			payload:   []byte(`{"id":`),
		}, wireCodeDecodeFailed},
		{"schema-invalid payload", testDeviceFactMessage{
			subject:   rawDeviceFactSubject(testEntityAID, "observation", "applied"),
			messageID: testFactOneID,
			payload:   []byte(`{"id":"` + testFactOneID + `"}`),
		}, wireCodeDecodeFailed},
		{"unsafe entity identity", unsafeEntity, wireCodeSubjectInvalid},
		{"unsafe version identity", unsafeVersionIdentity, wireCodeDecodeFailed},
		{"unknown family", unknownFamily, wireCodeSubjectInvalid},
		{"unknown variant", unknownVariant, wireCodeSubjectInvalid},
		{"entity event payload on observation subject", eventOnObservationSubject, wireCodeDecodeFailed},
		{"message id mismatch", mismatchedMessageID, wireCodeMsgIDMismatch},
		{"entity mismatch", entityMismatch, wireCodeSubjectMismatch},
		{"variant mismatch", variantMismatch, wireCodeSubjectMismatch},
		{"observation causation mismatch", causationMismatch, wireCodeCausationMissing},
		{"entity event causation mismatch", eventCausationMismatch, wireCodeCausationMissing},
		{"event name mismatch", eventNameMismatch, wireCodeSubjectMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := mapDeviceFactMessage(validator, test.message.asWire())
			if err == nil {
				t.Fatal("mapping accepted deterministic malformed input")
			}
			rejection := asWireRejection(err)
			if rejection == nil {
				t.Fatalf("mapping error is not a permanent rejection: %v", err)
			}
			if rejection.code != test.want {
				t.Fatalf("rejection code = %q, want %q", rejection.code, test.want)
			}
			if rejectionCode(err) != test.want {
				t.Fatalf("reported code = %q, want %q", rejectionCode(err), test.want)
			}
		})
	}
}

func mutation(change func(*observationFactInput)) observationFactInput {
	input := defaultObservationFactInput()
	change(&input)
	return input
}

func entityMutation(change func(*entityEventFactInput)) entityEventFactInput {
	input := defaultEntityEventFactInput()
	change(&input)
	return input
}
