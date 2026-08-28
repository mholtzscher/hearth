package devices

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
)

func TestGenericCatalogCarriesTypedBehaviorAcrossErasure(t *testing.T) {
	type state struct {
		Level int `json:"level"`
	}
	type stateSupport struct {
		Maximum int `json:"maximum"`
	}
	type operationSupport struct {
		Minimum int `json:"minimum"`
	}
	type operations struct {
		Set *operationSupport `json:"set,omitempty"`
	}
	type support struct {
		State      stateSupport `json:"state"`
		Operations operations   `json:"operations"`
	}
	type parameters struct {
		Target int `json:"target"`
	}

	stateCodec := compileTestCodec[state](
		t,
		"state",
		`{"type":"object","required":["level"],"properties":{"level":{"type":"integer"}},"additionalProperties":false}`,
	)
	supportCodec := compileTestCodec[support](t, "support", `{
		"type":"object","required":["state","operations"],"additionalProperties":false,
		"properties":{
			"state":{"type":"object","required":["maximum"],"properties":{"maximum":{"type":"integer"}},"additionalProperties":false},
			"operations":{"type":"object","properties":{"set":{"type":"object","required":["minimum"],"properties":{"minimum":{"type":"integer"}},"additionalProperties":false}},"additionalProperties":false}
		}}`)
	parametersCodec := compileTestCodec[parameters](
		t,
		"parameters",
		`{"type":"object","required":["target"],"properties":{"target":{"type":"integer"}},"additionalProperties":false}`,
	)

	set := DefineOperation(
		OperationNameSet,
		parametersCodec,
		func(value support) (operationSupport, bool) {
			if value.Operations.Set == nil {
				return operationSupport{}, false
			}
			return *value.Operations.Set, true
		},
		func(entitySupport support, operation operationSupport, parameters parameters) error {
			if parameters.Target < operation.Minimum || parameters.Target > entitySupport.State.Maximum {
				return errors.New("target is outside supported range")
			}
			return nil
		},
		3*time.Second,
		func(parameters parameters, state state) bool { return parameters.Target == state.Level },
	)
	definition, err := DefineEntityType(
		"test.level/v1",
		stateCodec,
		supportCodec,
		func(support support, state state) error {
			if state.Level > support.State.Maximum {
				return errors.New("level exceeds maximum")
			}
			return nil
		},
		func(left, right state) bool { return left.Level == right.Level },
		set,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewTypeCatalog([]EntityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}
	entity := Entity{
		TypeID:  "test.level/v1",
		Support: EntitySupport(`{"state":{"maximum":10},"operations":{"set":{"minimum":2}}}`),
	}

	normalized, err := catalog.NormalizeState(entity, Value(`{ "level": 5 }`))
	if err != nil || string(normalized) != `{"level":5}` {
		t.Fatalf("normalized state = %s, %v", normalized, err)
	}
	if _, err := catalog.NormalizeState(entity, Value(`{"level":11}`)); err == nil {
		t.Fatal("unsupported state unexpectedly accepted")
	}
	equal, err := catalog.EqualState(entity, Value(`{"level":5}`), Value(`{ "level": 5 }`))
	if err != nil || !equal {
		t.Fatalf("state equality = %v, %v", equal, err)
	}
	narrowed := entity
	narrowed.Support = EntitySupport(`{"state":{"maximum":4},"operations":{"set":{"minimum":2}}}`)
	equal, err = catalog.EqualState(narrowed, Value(`{"level":5}`), Value(`{"level":4}`))
	if err != nil || equal {
		t.Fatalf("equality after support narrowed = %v, %v", equal, err)
	}
	if _, err := catalog.EqualState(narrowed, Value(`{"level":4}`), Value(`{"level":5}`)); err == nil {
		t.Fatal("support-incompatible incoming State unexpectedly accepted by equality")
	}
	resolved, err := catalog.ResolveCommand(entity, OperationNameSet, CommandParameters(`{ "target": 5 }`))
	if err != nil || string(resolved.Parameters) != `{"target":5}` || resolved.Deadline != 3*time.Second {
		t.Fatalf("resolved command = %#v, %v", resolved, err)
	}
	if _, err := catalog.ResolveCommand(entity, OperationNameSet, CommandParameters(`{"target":1}`)); err == nil {
		t.Fatal("support-incompatible parameters unexpectedly accepted")
	}

	withoutOperation := entity
	withoutOperation.Support = EntitySupport(`{"state":{"maximum":10},"operations":{}}`)
	if _, err := catalog.ResolveCommand(
		withoutOperation,
		OperationNameSet,
		CommandParameters(`{"target":5}`),
	); err == nil {
		t.Fatal("absent operation support unexpectedly accepted")
	}

	record := CommandRecord{OperationName: OperationNameSet, Parameters: resolved.Parameters}
	withoutOperation.Support = EntitySupport(`{"state":{"maximum":1},"operations":{}}`)
	satisfied, err := catalog.Satisfies(withoutOperation, record, Value(`{"level":5}`))
	if err != nil || !satisfied {
		t.Fatalf("immutable outcome behavior = %v, %v", satisfied, err)
	}
}

func TestCatalogRejectsInvalidDefinitions(t *testing.T) {
	state := compileTestCodec[bool](t, "state", `{"type":"boolean"}`)
	support := compileTestCodec[struct{}](t, "support", `{"type":"object","maxProperties":0}`)
	parameters := compileTestCodec[struct{}](t, "parameters", `{"type":"object","maxProperties":0}`)
	validOperation := DefineOperation(
		OperationNameSet,
		parameters,
		func(struct{}) (struct{}, bool) { return struct{}{}, true },
		func(struct{}, struct{}, struct{}) error { return nil },
		time.Second,
		func(struct{}, bool) bool { return true },
	)
	valid, err := DefineEntityType(
		"test.value/v1",
		state,
		support,
		func(struct{}, bool) error { return nil },
		func(left, right bool) bool { return left == right },
		validOperation,
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewTypeCatalog([]EntityTypeDefinition{valid, valid}); err == nil {
		t.Fatal("duplicate type unexpectedly accepted")
	}
	if _, err := NewTypeCatalog([]EntityTypeDefinition{{}}); err == nil {
		t.Fatal("zero definition unexpectedly accepted")
	}
	duplicateOperation := DefineOperation(
		OperationNameSet,
		parameters,
		func(struct{}) (struct{}, bool) { return struct{}{}, true },
		func(struct{}, struct{}, struct{}) error { return nil },
		time.Second,
		func(struct{}, bool) bool { return true },
	)
	if _, err := DefineEntityType(
		"test.duplicate/v1",
		state,
		support,
		func(struct{}, bool) error { return nil },
		func(bool, bool) bool { return true },
		validOperation,
		duplicateOperation,
	); err == nil {
		t.Fatal("duplicate operation unexpectedly accepted")
	}
	invalidOperation := DefineOperation(
		OperationName("bad.name"),
		parameters,
		func(struct{}) (struct{}, bool) { return struct{}{}, true },
		func(struct{}, struct{}, struct{}) error { return nil },
		time.Second,
		func(struct{}, bool) bool { return true },
	)
	if _, err := DefineEntityType(
		"test.invalid/v1",
		state,
		support,
		func(struct{}, bool) error { return nil },
		func(bool, bool) bool { return true },
		invalidOperation,
	); err == nil {
		t.Fatal("unsafe operation name unexpectedly accepted")
	}
	if _, err := DefineEntityType(
		"test.nil/v1",
		state,
		support,
		nil,
		func(bool, bool) bool { return true },
	); err == nil {
		t.Fatal("nil supported-state validator unexpectedly accepted")
	}
}

func compileTestCodec[T any](t *testing.T, name, schema string) *entitytypes.JSONCodec[T] {
	t.Helper()
	codec, err := entitytypes.CompileJSONCodec[T]("urn:test:"+name, json.RawMessage(schema), nil)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}
