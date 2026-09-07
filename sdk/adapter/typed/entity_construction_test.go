package typed_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
	"github.com/mholtzscher/hearth/sdk/adapter"
	"github.com/mholtzscher/hearth/sdk/adapter/typed"
)

type constructionSupport struct {
	Level int `json:"level"`
}

type constructionState struct {
	Value int `json:"value"`
}

const (
	constructionSupportSchema = `{
		"type": "object",
		"properties": {"level": {"type": "integer", "minimum": 0, "maximum": 100}},
		"required": ["level"],
		"additionalProperties": false
	}`
	constructionStateSchema = `{
		"type": "object",
		"properties": {"value": {"type": "integer", "minimum": 0}},
		"required": ["value"],
		"additionalProperties": false
	}`
)

// compileConstructionCodecs builds schema-backed codecs independent of any
// generated Entity type so the shared helpers are tested once, not per facade.
func compileConstructionCodecs(
	t *testing.T,
) (*entitytypes.JSONCodec[constructionState], *entitytypes.JSONCodec[constructionSupport]) {
	t.Helper()
	state, err := entitytypes.CompileJSONCodec[constructionState](
		"test:construction:state",
		json.RawMessage(constructionStateSchema),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	support, err := entitytypes.CompileJSONCodec[constructionSupport](
		"test:construction:support",
		json.RawMessage(constructionSupportSchema),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return state, support
}

// validateConstructionState rejects State values above the supported level,
// mirroring the support-dependent validators passed by generated facades.
func validateConstructionState(support constructionSupport, state constructionState) error {
	if state.Value > support.Level {
		return errors.New("construction value exceeds supported level")
	}
	return nil
}

func requireConstructionValidationError(t *testing.T, err error, action, fragment string) {
	t.Helper()
	var validationErr *adapter.ValidationError
	if err == nil || !errors.As(err, &validationErr) {
		t.Fatalf("%s: expected adapter validation error, got %v", action, err)
	}
	if !strings.Contains(err.Error(), fragment) {
		t.Fatalf("%s: error %q does not contain %q", action, err, fragment)
	}
}

// This test protects descriptor metadata copying, type ID wiring, and support
// normalization, and fails if the helper drops or reorders any of them.
func TestTypedEntityDescriptorCopiesMetadataAndNormalizesSupport(t *testing.T) {
	t.Parallel()
	_, supportCodec := compileConstructionCodecs(t)
	descriptor, err := typed.NewTypedEntityDescriptor(
		adapter.EntityMetadata{Key: "key", ExternalID: "ext", Name: "name"},
		"test.construction/v1",
		constructionSupport{Level: 80},
		supportCodec,
	)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Key != "key" || descriptor.ExternalID != "ext" ||
		descriptor.Name != "name" || descriptor.Type != "test.construction/v1" {
		t.Fatalf("descriptor metadata = %#v", descriptor)
	}
	if string(descriptor.Support) != `{"level":80}` {
		t.Fatalf("descriptor support = %s", descriptor.Support)
	}
}

// This test protects schema validation of descriptor support, and fails if the
// helper accepts support the schema rejects or misclassifies the error.
func TestTypedEntityDescriptorRejectsInvalidSupport(t *testing.T) {
	t.Parallel()
	_, supportCodec := compileConstructionCodecs(t)
	_, err := typed.NewTypedEntityDescriptor(
		adapter.EntityMetadata{Key: "key"},
		"test.construction/v1",
		constructionSupport{Level: 101},
		supportCodec,
	)
	requireConstructionValidationError(t, err, "invalid support", "invalid Entity support")
}

// This test protects observation value normalization and UTC time formatting,
// and fails if the helper loses the normalized value or emits non-UTC times.
func TestTypedEntityObservationNormalizesValueAndTimes(t *testing.T) {
	t.Parallel()
	stateCodec, supportCodec := compileConstructionCodecs(t)
	received := time.Date(2026, 3, 10, 14, 30, 0, 123456789, time.FixedZone("CET", 3600))
	source := time.Date(2026, 3, 10, 14, 29, 0, 0, time.FixedZone("CET", 3600))
	observation, err := typed.NewTypedEntityObservation(
		typed.EntityObservationInput[constructionState, constructionSupport]{
			EntityID:          "ent_one",
			Support:           constructionSupport{Level: 80},
			State:             constructionState{Value: 70},
			AdapterReceivedAt: received,
			SourceUpdatedAt:   &source,
		},
		stateCodec,
		supportCodec,
		validateConstructionState,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observation.EntityID != "ent_one" || string(observation.Value) != `{"value":70}` {
		t.Fatalf("observation identity/value = %#v", observation)
	}
	if observation.AdapterReceivedAt != "2026-03-10T13:30:00.123456789Z" {
		t.Fatalf("adapter received time = %q", observation.AdapterReceivedAt)
	}
	if observation.SourceUpdatedAt == nil || *observation.SourceUpdatedAt != "2026-03-10T13:29:00Z" {
		t.Fatalf("source updated time = %v", observation.SourceUpdatedAt)
	}
}

// This test protects the constructor validation order and error
// classification, and fails if a later check shadows an earlier one or an
// error escapes without adapter.ValidationError wrapping.
func TestTypedEntityObservationValidationOrder(t *testing.T) {
	t.Parallel()
	stateCodec, supportCodec := compileConstructionCodecs(t)
	received := time.Date(2026, 3, 10, 14, 30, 0, 0, time.UTC)
	zero := time.Time{}
	valid := typed.EntityObservationInput[constructionState, constructionSupport]{
		EntityID:          "ent_one",
		Support:           constructionSupport{Level: 80},
		State:             constructionState{Value: 70},
		AdapterReceivedAt: received,
	}
	cases := map[string]struct {
		mutate   func(*typed.EntityObservationInput[constructionState, constructionSupport])
		fragment string
	}{
		"empty entity ID wins over invalid support": {
			mutate: func(
				input *typed.EntityObservationInput[constructionState, constructionSupport],
			) {
				input.EntityID = ""
				input.Support.Level = 101
			},
			fragment: "Observation entity ID is required",
		},
		"zero received time": {
			mutate: func(
				input *typed.EntityObservationInput[constructionState, constructionSupport],
			) {
				input.AdapterReceivedAt = time.Time{}
			},
			fragment: "Observation adapter received time is required",
		},
		"present zero source time": {
			mutate: func(
				input *typed.EntityObservationInput[constructionState, constructionSupport],
			) {
				input.SourceUpdatedAt = &zero
			},
			fragment: "Observation source updated time must be non-zero",
		},
		"invalid support precedes State validation": {
			mutate: func(
				input *typed.EntityObservationInput[constructionState, constructionSupport],
			) {
				input.Support.Level = 101
				input.State.Value = 999
			},
			fragment: "invalid Entity support",
		},
		"unsupported State precedes State schema validation": {
			mutate: func(
				input *typed.EntityObservationInput[constructionState, constructionSupport],
			) {
				input.State.Value = 90
			},
			fragment: "unsupported State",
		},
		"schema-invalid State fails after support validation": {
			mutate: func(
				input *typed.EntityObservationInput[constructionState, constructionSupport],
			) {
				input.State.Value = -1
			},
			fragment: "invalid State",
		},
	}
	for name, calling := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := valid
			calling.mutate(&input)
			_, err := typed.NewTypedEntityObservation(
				input,
				stateCodec,
				supportCodec,
				validateConstructionState,
			)
			requireConstructionValidationError(t, err, name, calling.fragment)
		})
	}
}
