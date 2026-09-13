package devices //nolint:testpackage // Tests exercise package-private domain seams and repository fixtures.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/entitytypes"
	"github.com/mholtzscher/hearth/internal/entitytypetest"
)

//nolint:gocognit // The type-erasure contract is clearer as one end-to-end test.
func TestGenericCatalogCarriesTypedBehaviorAcrossErasure(t *testing.T) {
	t.Parallel()
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
		OutcomeObserved,
		func(parameters parameters, state state) bool { return parameters.Target == state.Level },
	)
	definition, err := DefineEntityType(
		"test.level/v1",
		stateCodec,
		supportCodec,
		func(_ support) error { return nil },
		func(support support, state state) error {
			if state.Level > support.State.Maximum {
				return errors.New("level exceeds maximum")
			}
			return nil
		},
		func(left, right state) bool { return left.Level == right.Level },
		func(_, _ support) bool { return true },
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
	if _, normalizeErr := catalog.NormalizeState(entity, Value(`{"level":11}`)); normalizeErr == nil {
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
	if _, equalityErr := catalog.EqualState(narrowed, Value(`{"level":4}`), Value(`{"level":5}`)); equalityErr == nil {
		t.Fatal("support-incompatible incoming State unexpectedly accepted by equality")
	}
	resolved, err := catalog.ResolveCommand(entity, OperationNameSet, CommandParameters(`{ "target": 5 }`))
	if err != nil || string(resolved.Parameters) != `{"target":5}` || resolved.Deadline != 3*time.Second {
		t.Fatalf("resolved command = %#v, %v", resolved, err)
	}
	if _, resolveErr := catalog.ResolveCommand(
		entity, OperationNameSet, CommandParameters(`{"target":1}`),
	); resolveErr == nil {
		t.Fatal("support-incompatible parameters unexpectedly accepted")
	}

	withoutOperation := entity
	withoutOperation.Support = EntitySupport(`{"state":{"maximum":10},"operations":{}}`)
	if _, resolveErr := catalog.ResolveCommand(
		withoutOperation,
		OperationNameSet,
		CommandParameters(`{"target":5}`),
	); resolveErr == nil {
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
	t.Parallel()
	state := compileTestCodec[bool](t, "state", `{"type":"boolean"}`)
	support := compileTestCodec[struct{}](t, "support", `{"type":"object","maxProperties":0}`)
	parameters := compileTestCodec[struct{}](t, "parameters", `{"type":"object","maxProperties":0}`)
	validOperation := DefineOperation(
		OperationNameSet,
		parameters,
		func(struct{}) (struct{}, bool) { return struct{}{}, true },
		func(struct{}, struct{}, struct{}) error { return nil },
		time.Second,
		OutcomeObserved,
		func(struct{}, bool) bool { return true },
	)
	valid, err := DefineEntityType(
		"test.value/v1",
		state,
		support,
		func(struct{}) error { return nil },
		func(struct{}, bool) error { return nil },
		func(left, right bool) bool { return left == right },
		func(_, _ struct{}) bool { return true },
		validOperation,
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, catalogErr := NewTypeCatalog([]EntityTypeDefinition{valid, valid}); catalogErr == nil {
		t.Fatal("duplicate type unexpectedly accepted")
	}
	if _, catalogErr := NewTypeCatalog([]EntityTypeDefinition{{}}); catalogErr == nil {
		t.Fatal("zero definition unexpectedly accepted")
	}
	duplicateOperation := DefineOperation(
		OperationNameSet,
		parameters,
		func(struct{}) (struct{}, bool) { return struct{}{}, true },
		func(struct{}, struct{}, struct{}) error { return nil },
		time.Second,
		OutcomeObserved,
		func(struct{}, bool) bool { return true },
	)
	if _, defineErr := DefineEntityType(
		"test.duplicate/v1",
		state,
		support,
		func(struct{}) error { return nil },
		func(struct{}, bool) error { return nil },
		func(bool, bool) bool { return true },
		func(_, _ struct{}) bool { return true },
		validOperation,
		duplicateOperation,
	); defineErr == nil {
		t.Fatal("duplicate operation unexpectedly accepted")
	}
	invalidOperation := DefineOperation(
		OperationName("bad.name"),
		parameters,
		func(struct{}) (struct{}, bool) { return struct{}{}, true },
		func(struct{}, struct{}, struct{}) error { return nil },
		time.Second,
		OutcomeObserved,
		func(struct{}, bool) bool { return true },
	)
	if _, defineErr := DefineEntityType(
		"test.invalid/v1",
		state,
		support,
		func(struct{}) error { return nil },
		func(struct{}, bool) error { return nil },
		func(bool, bool) bool { return true },
		func(_, _ struct{}) bool { return true },
		invalidOperation,
	); defineErr == nil {
		t.Fatal("unsafe operation name unexpectedly accepted")
	}
	if _, defineErr := DefineEntityType(
		"test.nil/v1",
		state,
		support,
		func(struct{}) error { return nil },
		nil,
		func(bool, bool) bool { return true },
		func(_, _ struct{}) bool { return true },
	); defineErr == nil {
		t.Fatal("nil supported-state validator unexpectedly accepted")
	}
}

func TestCatalogSameSupportIdentityUsesDeclaredPaths(t *testing.T) {
	t.Parallel()
	type state float64
	type stateSupport struct {
		Kind    string  `json:"kind"`
		Minimum float64 `json:"minimum"`
		Maximum float64 `json:"maximum"`
	}
	type support struct {
		State      stateSupport `json:"state"`
		Operations struct{}     `json:"operations"`
	}
	stateCodec := compileTestCodec[state](t, "identity-state", `{"type":"number"}`)
	supportCodec := compileTestCodec[support](t, "identity-support", `{
		"type":"object","additionalProperties":false,"required":["state","operations"],
		"properties":{
			"state":{"type":"object","additionalProperties":false,"required":["kind","minimum","maximum"],
				"properties":{"kind":{"type":"string"},"minimum":{"type":"number"},"maximum":{"type":"number"}}},
			"operations":{"type":"object","maxProperties":0,"additionalProperties":false}}}`)
	definition, err := DefineEntityType[state, support](
		"test.identity/v1",
		stateCodec,
		supportCodec,
		func(support) error { return nil },
		func(support, state) error { return nil },
		func(left, right state) bool { return left == right },
		func(previous, next support) bool { return previous.State.Kind == next.State.Kind },
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewTypeCatalog([]EntityTypeDefinition{definition})
	if err != nil {
		t.Fatal(err)
	}

	base := EntitySupport(`{"state":{"kind":"temperature","minimum":0,"maximum":100},"operations":{}}`)
	widerBounds := EntitySupport(`{"state":{"kind":"temperature","minimum":-273.15,"maximum":1000},"operations":{}}`)
	otherKind := EntitySupport(`{"state":{"kind":"relative_humidity","minimum":0,"maximum":100},"operations":{}}`)

	same, err := catalog.SameSupportIdentity("test.identity/v1", base, widerBounds)
	if err != nil || !same {
		t.Fatalf("changed mutable bounds identity = %v, %v", same, err)
	}
	same, err = catalog.SameSupportIdentity("test.identity/v1", base, otherKind)
	if err != nil || same {
		t.Fatalf("changed immutable kind identity = %v, %v", same, err)
	}
	if _, unknownErr := catalog.SameSupportIdentity("test.unknown/v1", base, base); unknownErr == nil {
		t.Fatal("unknown type unexpectedly compared support identity")
	}
	if _, invalidErr := catalog.SameSupportIdentity(
		"test.identity/v1", base, EntitySupport(`{"state":{"minimum":0,"maximum":100},"operations":{}}`),
	); invalidErr == nil {
		t.Fatal("invalid next support unexpectedly compared support identity")
	}
}

// TestBuiltinCatalogUndeclaredTypesPermitSupportChanges pins the generated
// default: a type that declares no immutable support paths keeps permitting
// every currently valid support change.
func TestBuiltinCatalogUndeclaredTypesPermitSupportChanges(t *testing.T) {
	t.Parallel()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	previous := EntitySupport(`{"state":{"minimum":0,"maximum":255,"unit":"lqi"},"operations":{}}`)
	next := EntitySupport(`{"state":{"minimum":0,"maximum":100,"unit":"lqi"},"operations":{}}`)
	same, err := catalog.SameSupportIdentity(EntityTypeNumericsensorV1, previous, next)
	if err != nil || !same {
		t.Fatalf("undeclared support identity = %v, %v", same, err)
	}
}

func TestMeasurementTypeExposesStateWithoutOperations(t *testing.T) {
	t.Parallel()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	support := EntitySupport(
		`{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":-273.15,"maximum":1000},"operations":{}}`,
	)
	entity := Entity{
		ID: EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"), TypeID: EntityTypeMeasurementV1,
		Support: support,
	}

	normalized, err := catalog.NormalizeSupport(entity.TypeID, support)
	if err != nil || !entitytypetest.EqualJSON(t, normalized, []byte(support)) {
		t.Fatalf("normalized support = %s, %v", normalized, err)
	}
	for _, state := range []struct {
		value string
		valid bool
	}{
		{`-273.15`, true},
		{`21.5`, true},
		{`1000`, true},
		{`-273.16`, false},
		{`1000.5`, false},
		{`"21.5"`, false},
	} {
		if _, stateErr := catalog.NormalizeState(entity, Value(state.value)); (stateErr == nil) != state.valid {
			t.Errorf("NormalizeState(%s) error = %v, want valid = %t", state.value, stateErr, state.valid)
		}
	}

	for _, operation := range []OperationName{OperationNameSet, OperationName("get"), OperationName("unknown")} {
		if _, resolveErr := catalog.ResolveCommand(
			entity,
			operation,
			CommandParameters(`{"value":21.5}`),
		); resolveErr == nil {
			t.Errorf("measurement operation %q unexpectedly accepted", operation)
		}
		record := CommandRecord{OperationName: operation, Parameters: CommandParameters(`{"value":21.5}`)}
		if _, satisfiesErr := catalog.Satisfies(entity, record, Value(`21.5`)); satisfiesErr == nil {
			t.Errorf("measurement outcome %q unexpectedly accepted", operation)
		}
	}
}

// TestBuiltinCatalogMeasurementKindIsImmutable protects the manifest-declared
// immutable support path: a re-registration that changes measurement_kind is
// rejected, while changing the Entity's accepted bounds within its kind stays a
// legal support change.
func TestBuiltinCatalogMeasurementKindIsImmutable(t *testing.T) {
	t.Parallel()
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	previous := EntitySupport(
		`{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":-273.15,"maximum":1000},"operations":{}}`,
	)
	narrowerBounds := EntitySupport(
		`{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":0,"maximum":40},"operations":{}}`,
	)
	otherKind := EntitySupport(
		`{"state":{"measurement_kind":"relative_humidity","unit":"%","minimum":0,"maximum":100},"operations":{}}`,
	)

	same, err := catalog.SameSupportIdentity(EntityTypeMeasurementV1, previous, narrowerBounds)
	if err != nil || !same {
		t.Fatalf("changed bounds identity = %v, %v, want true", same, err)
	}
	same, err = catalog.SameSupportIdentity(EntityTypeMeasurementV1, previous, otherKind)
	if err != nil || same {
		t.Fatalf("changed measurement kind identity = %v, %v, want false", same, err)
	}
	invalidKind := EntitySupport(
		`{"state":{"measurement_kind":"pressure","unit":"Pa","minimum":0,"maximum":1000},"operations":{}}`,
	)
	if _, identityErr := catalog.SameSupportIdentity(
		EntityTypeMeasurementV1,
		previous,
		invalidKind,
	); identityErr == nil {
		t.Fatal("kind outside the v1 catalog unexpectedly compared support identity")
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

// TestBuiltinCatalogMatchesEntityTypeInventory protects registration
// completeness and fails if the generator drops a built-in registration
// even when the generated catalog test is omitted alongside it.
func TestBuiltinCatalogMatchesEntityTypeInventory(t *testing.T) {
	t.Parallel()
	expected := loadEntityTypeInventory(t)
	catalog, err := NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	checkCatalogIDs(t, expected, catalog)
	checkCatalogOperations(t, expected, catalog)
}

func loadEntityTypeInventory(t *testing.T) map[EntityTypeID]map[OperationName]struct{} {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate catalog test")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	matches, err := filepath.Glob(filepath.Join(root, "entitytypes", "*", "entitytype.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no entitytype manifests discovered")
	}
	sort.Strings(matches)
	expected := make(map[EntityTypeID]map[OperationName]struct{}, len(matches))
	for _, match := range matches {
		id, operations := decodeInventoryManifest(t, match)
		if _, duplicate := expected[id]; duplicate {
			t.Fatalf("duplicate entity type %q", id)
		}
		expected[id] = operations
	}
	return expected
}

func decodeInventoryManifest(t *testing.T, path string) (EntityTypeID, map[OperationName]struct{}) {
	t.Helper()
	var manifest struct {
		Type       string                     `json:"type"`
		Operations map[string]json.RawMessage `json:"operations"`
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if unmarshalErr := json.Unmarshal(raw, &manifest); unmarshalErr != nil {
		t.Fatalf("decode %s: %v", path, unmarshalErr)
	}
	if manifest.Type == "" {
		t.Fatalf("%s has no type ID", path)
	}
	operations := make(map[OperationName]struct{}, len(manifest.Operations))
	for name := range manifest.Operations {
		operations[OperationName(name)] = struct{}{}
	}
	return EntityTypeID(manifest.Type), operations
}

func checkCatalogIDs(
	t *testing.T,
	expected map[EntityTypeID]map[OperationName]struct{},
	catalog *TypeCatalog,
) {
	t.Helper()
	var missing, extra []string
	for id := range expected {
		if _, exists := catalog.types[id]; !exists {
			missing = append(missing, string(id))
		}
	}
	for id := range catalog.types {
		if _, exists := expected[id]; !exists {
			extra = append(extra, string(id))
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		sort.Strings(missing)
		sort.Strings(extra)
		t.Fatalf("catalog inventory mismatch: missing=%v extra=%v", missing, extra)
	}
}

func checkCatalogOperations(
	t *testing.T,
	expected map[EntityTypeID]map[OperationName]struct{},
	catalog *TypeCatalog,
) {
	t.Helper()
	for id, want := range expected {
		definition := catalog.types[id]
		if len(definition.operations) != len(want) {
			t.Errorf("entity type %q operations = %d, want %d", id, len(definition.operations), len(want))
		}
		for name := range want {
			if _, exists := definition.operations[name]; !exists {
				t.Errorf("entity type %q is missing operation %q", id, name)
			}
		}
		for name := range definition.operations {
			if _, exists := want[name]; !exists {
				t.Errorf("entity type %q has unexpected operation %q", id, name)
			}
		}
	}
}
