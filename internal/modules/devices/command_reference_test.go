package devices //nolint:testpackage // Exercises owner APIs with narrow read-only fixtures and typed catalog definitions.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type commandReferenceReads struct {
	ReadRepository

	view EntityWithState
	err  error
}

func (reads commandReferenceReads) GetEntity(_ context.Context, id EntityID) (EntityWithState, error) {
	if id != commandTestEntityID {
		panic("reference lookup used the wrong Entity ID")
	}
	return reads.view, reads.err
}

func TestValidateCommandReferenceRejectsInvalidIdentityAndOperationBeforeReading(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		entityID  EntityID
		operation OperationName
	}{
		{"empty Entity", "", OperationNameSet},
		{"wrong identity kind", EntityID(commandTestDeviceID), OperationNameSet},
		{"uppercase Entity", "ent_01890F47-7a6b-7c4d-8e9f-0123456789ab", OperationNameSet},
		{"empty Operation", commandTestEntityID, ""},
		{"unsafe Operation", commandTestEntityID, "set.*"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := NewService(Stores{}, nil, firstLightCatalog(t), commandValidationDependencies())
			if err := service.ValidateCommandReference(
				t.Context(),
				test.entityID,
				test.operation,
			); !errors.Is(
				err,
				ErrInvalidCommand,
			) {
				t.Fatalf("reference error = %v, want invalid Command", err)
			}
		})
	}
}

func TestValidateCommandReferenceChecksCurrentTargetWithoutControlEligibility(t *testing.T) {
	t.Parallel()
	readFailure := errors.New("Entity read failed")
	for _, test := range []struct {
		name      string
		change    func(*commandReferenceReads)
		operation OperationName
		want      error
	}{
		{"supported offline disabled Entity", func(r *commandReferenceReads) {
			r.view.Entity.Enabled = false
			r.view.Availability.Status = EntityAvailabilityUnavailable
		}, OperationNameSet, nil},
		{"missing Entity", func(r *commandReferenceReads) { r.err = ErrEntityNotFound }, OperationNameSet, ErrEntityNotFound},
		{"read failure", func(r *commandReferenceReads) { r.err = readFailure }, OperationNameSet, readFailure},
		{"undefined Operation", func(*commandReferenceReads) {}, "toggle", ErrInvalidCommand},
		{"unknown type", func(r *commandReferenceReads) { r.view.Entity.TypeID = "missing/v1" }, OperationNameSet, ErrInvalidCommand},
		{"read-only sensor", func(r *commandReferenceReads) {
			r.view.Entity.TypeID = EntityTypeBinarysensorV1
			r.view.Entity.Support = EntitySupport(`{"state":{}}`)
		}, OperationNameSet, ErrInvalidCommand},
		{"malformed support", func(r *commandReferenceReads) { r.view.Entity.Support = EntitySupport(`{`) }, OperationNameSet, ErrInvalidCommand},
		{"invalid support shape", func(r *commandReferenceReads) { r.view.Entity.Support = EntitySupport(`{}`) }, OperationNameSet, ErrInvalidCommand},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reads := commandReferenceReads{view: newCommandRepository().view}
			test.change(&reads)
			// No health, writable store, or sender is provided. Closed admission
			// must not prevent a read-only reference check either.
			service := NewService(Stores{Reads: reads}, nil, firstLightCatalog(t), commandValidationDependencies())
			service.StopAdmission()
			err := service.ValidateCommandReference(t.Context(), commandTestEntityID, test.operation)
			if !errors.Is(err, test.want) {
				t.Fatalf("reference error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateCommandReferenceUsesTypedSupportWithoutParameterValidation(t *testing.T) {
	t.Parallel()
	type support struct {
		Writable bool `json:"writable"`
		Maximum  int  `json:"maximum"`
	}
	type parameters struct {
		Value int `json:"value"`
	}
	stateCodec := compileTestCodec[int](t, "reference-state", `{"type":"integer"}`)
	supportCodec := compileTestCodec[support](t, "reference-support", `{
		"type":"object","required":["writable","maximum"],"additionalProperties":false,
		"properties":{"writable":{"type":"boolean"},"maximum":{"type":"integer"}}}`)
	// A required parameter and a support selector unrelated to an
	// "operations" JSON member detect dummy-payload and JSON-guess shortcuts.
	parametersCodec := compileTestCodec[parameters](t, "reference-parameters", `{
		"type":"object","required":["value"],"additionalProperties":false,
		"properties":{"value":{"type":"integer"}}}`)
	parameterValidations := 0
	operation := DefineOperation(
		OperationNameSet, parametersCodec,
		func(s support) (int, bool) { return s.Maximum, s.Writable },
		func(_ support, maximum int, value parameters) error {
			parameterValidations++
			if value.Value < 1 || value.Value > maximum {
				return errors.New("value outside supported range")
			}
			return nil
		},
		time.Second, OutcomeObserved, func(value parameters, state int) bool { return value.Value == state },
	)
	definition, err := DefineEntityType("test.reference/v1", stateCodec, supportCodec,
		func(s support) error {
			if s.Maximum < 1 {
				return errors.New("maximum must be positive")
			}
			return nil
		},
		func(_ support, _ int) error { return nil }, func(left, right int) bool { return left == right }, operation)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewTypeCatalog([]EntityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, support string
		want          error
	}{
		{"supported", `{"writable":true,"maximum":10}`, nil},
		{"selector rejects", `{"writable":false,"maximum":10}`, ErrInvalidCommand},
		{"support rule rejects", `{"writable":true,"maximum":0}`, ErrInvalidCommand},
	} {
		reads := commandReferenceReads{view: EntityWithState{Entity: Entity{
			ID: commandTestEntityID, TypeID: "test.reference/v1", Support: EntitySupport(test.support),
		}}}
		service := NewService(Stores{Reads: reads}, nil, catalog, commandValidationDependencies())
		if err = service.ValidateCommandReference(
			t.Context(),
			commandTestEntityID,
			OperationNameSet,
		); !errors.Is(
			err,
			test.want,
		) {
			t.Fatalf("%s: reference error = %v, want %v", test.name, err, test.want)
		}
	}
	if parameterValidations != 0 {
		t.Fatalf("reference checks validated %d dummy parameters", parameterValidations)
	}
	reads := commandReferenceReads{view: EntityWithState{Entity: Entity{
		ID: commandTestEntityID, TypeID: "test.reference/v1", Support: EntitySupport(`{"writable":true,"maximum":10}`),
	}}}
	service := NewService(Stores{Reads: reads}, nil, catalog, commandValidationDependencies())
	for _, raw := range []string{`{}`, `{"value":0}`, `{"value":11}`, `{"value":"5"}`} {
		_, err = service.ValidateCommand(t.Context(), CommandInput{
			EntityID: commandTestEntityID, OperationName: OperationNameSet, Parameters: CommandParameters(raw),
		})
		if !errors.Is(err, ErrInvalidCommand) {
			t.Fatalf("concrete parameters %s: %v", raw, err)
		}
	}
	normalized, err := service.ValidateCommand(t.Context(), CommandInput{
		EntityID:      commandTestEntityID,
		OperationName: OperationNameSet,
		Parameters:    CommandParameters(` { "value" : 5 } `),
	})
	if err != nil || string(normalized) != `{"value":5}` {
		t.Fatalf("concrete validation = %s, %v", normalized, err)
	}
}
