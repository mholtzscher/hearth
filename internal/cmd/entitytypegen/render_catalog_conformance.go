package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// catalogProbe is the deterministic compact wiring check for one built-in
// type: normalized support and State, one resolved command per operation with
// its manifest deadline, one satisfied and one unsatisfied outcome per
// operation, schema-decodable authored invalid support-State/params
// rejections, and equal/unequal State. The full example matrix remains at the
// contract seam; expectations below come from authored flags, the manifest,
// and raw value (in)equality, never from production validation.
type catalogProbe struct {
	model                 entityTypeModel
	support               json.RawMessage
	validState            json.RawMessage
	supportInvalidState   json.RawMessage
	supportInvalidSupport json.RawMessage
	unequalState          json.RawMessage
	operations            []catalogOperationProbe
}

// catalogOperationProbe carries one representative valid command, its
// deadline, and one satisfied/unsatisfied outcome pair for a single operation.
type catalogOperationProbe struct {
	model                 operationModel
	support               json.RawMessage
	parameters            json.RawMessage
	invalidSupport        json.RawMessage
	supportInvalidParams  json.RawMessage
	satisfiedParameters   json.RawMessage
	satisfiedState        json.RawMessage
	unsatisfiedParameters json.RawMessage
	unsatisfiedState      json.RawMessage
}

// catalogSchemaChecker reports whether a raw value satisfies a JSON schema
// without running any Entity-type DSL validation. Selection uses the authored
// valid/satisfied flags for expectations; schema decoding only identifies
// which authored-invalid examples are support-level rejections rather than
// schema rejections.
type catalogSchemaChecker struct {
	schemas map[string]*jsonschema.Schema
}

func newCatalogSchemaChecker() *catalogSchemaChecker {
	return &catalogSchemaChecker{schemas: make(map[string]*jsonschema.Schema)}
}

func (checker *catalogSchemaChecker) decodable(schemaPath string, value json.RawMessage) (bool, error) {
	schema, err := checker.compile(schemaPath)
	if err != nil {
		return false, err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if decodeErr := decoder.Decode(&decoded); decodeErr != nil {
		return false, decodeErr
	}
	return schema.Validate(decoded) == nil, nil
}

func (checker *catalogSchemaChecker) compile(schemaPath string) (*jsonschema.Schema, error) {
	if compiled, cached := checker.schemas[schemaPath]; cached {
		return compiled, nil
	}
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, err
	}
	var identifier struct {
		ID string `json:"$id"`
	}
	if unmarshalErr := json.Unmarshal(raw, &identifier); unmarshalErr != nil {
		return nil, unmarshalErr
	}
	resourceID := identifier.ID
	if resourceID == "" {
		resourceID = filepath.ToSlash(schemaPath)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	if addErr := compiler.AddResource(resourceID, document); addErr != nil {
		return nil, addErr
	}
	compiled, err := compiler.Compile(resourceID)
	if err != nil {
		return nil, err
	}
	checker.schemas[schemaPath] = compiled
	return compiled, nil
}

func selectCatalogProbe(model entityTypeModel, checker *catalogSchemaChecker) (catalogProbe, error) {
	probe := catalogProbe{model: model}
	first := model.Examples.Cases[0]
	probe.support = first.Support
	for _, state := range first.States {
		if state.Valid {
			probe.validState = state.Value
			break
		}
	}
	if len(probe.validState) == 0 {
		return catalogProbe{}, fmt.Errorf("case %q has no valid State", first.Name)
	}
	stateSchemaPath := filepath.Join(model.Directory, model.StateFile)
	supportInvalid, supportInvalidSupport, err := selectSupportInvalidState(model, checker, stateSchemaPath)
	if err != nil {
		return catalogProbe{}, err
	}
	probe.supportInvalidState = supportInvalid
	probe.supportInvalidSupport = supportInvalidSupport
	unequal, err := selectUnequalCatalogState(model, checker, stateSchemaPath, first, probe.validState)
	if err != nil {
		return catalogProbe{}, err
	}
	probe.unequalState = unequal
	for _, operation := range model.Operations {
		operationProbe, operationErr := selectCatalogOperationProbe(model, checker, operation)
		if operationErr != nil {
			return catalogProbe{}, operationErr
		}
		probe.operations = append(probe.operations, operationProbe)
	}
	return probe, nil
}

// selectSupportInvalidState returns the first authored-invalid State that
// still schema-decodes, proving support-validator wiring rather than mere
// schema rejection, along with its originating support. It returns nil when
// every invalid example is schema-invalid.
func selectSupportInvalidState(
	model entityTypeModel,
	checker *catalogSchemaChecker,
	stateSchemaPath string,
) (json.RawMessage, json.RawMessage, error) {
	for _, example := range model.Examples.Cases {
		for _, state := range example.States {
			if state.Valid {
				continue
			}
			decodable, err := checker.decodable(stateSchemaPath, state.Value)
			if err != nil {
				return nil, nil, err
			}
			if decodable {
				return state.Value, example.Support, nil
			}
		}
	}
	return nil, nil, nil
}

// selectUnequalCatalogState returns a second schema-decodable State whose
// value differs from the probe State, preferring sibling valid states before
// recorded outcome states. Expectations use raw value (in)equality, never
// production equality behavior.
func selectUnequalCatalogState(
	model entityTypeModel,
	checker *catalogSchemaChecker,
	stateSchemaPath string,
	first exampleCase,
	validState json.RawMessage,
) (json.RawMessage, error) {
	var candidates []json.RawMessage
	for _, state := range first.States {
		if state.Valid {
			candidates = append(candidates, state.Value)
		}
	}
	for _, operation := range model.Operations {
		if values, supported := first.Operations[operation.Name]; supported {
			for _, outcome := range values.Outcomes {
				candidates = append(candidates, outcome.State)
			}
		}
	}
	for _, candidate := range candidates {
		equal, err := equalCatalogJSON(candidate, validState)
		if err != nil {
			return nil, err
		}
		if equal {
			continue
		}
		decodable, err := checker.decodable(stateSchemaPath, candidate)
		if err != nil {
			return nil, err
		}
		if decodable {
			return candidate, nil
		}
	}
	return nil, nil
}

// selectCatalogOperationProbe picks one valid command and the first
// satisfied/unsatisfied outcome pair from a single originating case so the
// generated wiring reuses that case's support, plus the first support-level
// invalid parameters rep (with its own originating support when it lives in
// a different case), all in source order.
func selectCatalogOperationProbe(
	model entityTypeModel,
	checker *catalogSchemaChecker,
	operation operationModel,
) (catalogOperationProbe, error) {
	probe := catalogOperationProbe{model: operation}
	parametersSchemaPath := filepath.Join(model.Directory, operation.ParametersFile)
	for _, example := range model.Examples.Cases {
		values, supported := example.Operations[operation.Name]
		if !supported {
			continue
		}
		candidate := catalogOperationProbe{model: operation, support: example.Support}
		selectCatalogValidParams(values, &candidate)
		selectCatalogOutcomes(values, &candidate)
		if len(candidate.parameters) == 0 ||
			len(candidate.satisfiedParameters) == 0 ||
			len(candidate.unsatisfiedParameters) == 0 {
			continue
		}
		probe = candidate
		break
	}
	if len(probe.parameters) == 0 {
		return catalogOperationProbe{}, fmt.Errorf("operation %q has no valid parameters", operation.Name)
	}
	if len(probe.satisfiedParameters) == 0 || len(probe.unsatisfiedParameters) == 0 {
		return catalogOperationProbe{}, fmt.Errorf(
			"operation %q has no satisfied and unsatisfied outcome pair",
			operation.Name,
		)
	}
	invalid, invalidSupport, err := selectCatalogInvalidParams(model, checker, parametersSchemaPath, operation.Name)
	if err != nil {
		return catalogOperationProbe{}, err
	}
	probe.supportInvalidParams = invalid
	probe.invalidSupport = invalidSupport
	return probe, nil
}

// selectCatalogValidParams records the first valid parameters rep in source
// order.
func selectCatalogValidParams(values operationExamples, probe *catalogOperationProbe) {
	for _, parameters := range values.Parameters {
		if parameters.Valid && probe.parameters == nil {
			probe.parameters = parameters.Value
		}
	}
}

// selectCatalogInvalidParams returns the first authored-invalid parameters
// rep that still schema-decodes, along with its originating support. The
// originating case's rep is preferred; otherwise later cases supply the rep
// with their own support.
func selectCatalogInvalidParams(
	model entityTypeModel,
	checker *catalogSchemaChecker,
	schemaPath, operationName string,
) (json.RawMessage, json.RawMessage, error) {
	for _, example := range model.Examples.Cases {
		values, supported := example.Operations[operationName]
		if !supported {
			continue
		}
		for _, parameters := range values.Parameters {
			if parameters.Valid {
				continue
			}
			decodable, decodableErr := checker.decodable(schemaPath, parameters.Value)
			if decodableErr != nil {
				return nil, nil, decodableErr
			}
			if decodable {
				return parameters.Value, example.Support, nil
			}
		}
	}
	return nil, nil, nil
}

// selectCatalogOutcomes records the first satisfied and unsatisfied outcome
// pair in source order.
func selectCatalogOutcomes(values operationExamples, probe *catalogOperationProbe) {
	for _, outcome := range values.Outcomes {
		switch {
		case outcome.Satisfied && probe.satisfiedParameters == nil:
			probe.satisfiedParameters = outcome.Parameters
			probe.satisfiedState = outcome.State
		case !outcome.Satisfied && probe.unsatisfiedParameters == nil:
			probe.unsatisfiedParameters = outcome.Parameters
			probe.unsatisfiedState = outcome.State
		}
	}
}

// catalogJSONNumber is the canonical exact form of a JSON number. It is a
// distinct type so normalized numbers never collide with JSON strings.
type catalogJSONNumber string

// normalizeCatalogJSONValue replaces every [json.Number] with its canonical
// exact value ([big.Rat] RatString) so 1/1.0/1e0 and -0/0 compare equal while
// values beyond float64 precision stay distinct. Objects and arrays recurse.
func normalizeCatalogJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		rational, ok := new(big.Rat).SetString(value.String())
		if !ok {
			return nil, fmt.Errorf("invalid JSON number %q", value.String())
		}
		return catalogJSONNumber(rational.RatString()), nil
	case []any:
		for index, item := range value {
			normalized, err := normalizeCatalogJSONValue(item)
			if err != nil {
				return nil, err
			}
			value[index] = normalized
		}
		return value, nil
	case map[string]any:
		for key, item := range value {
			normalized, err := normalizeCatalogJSONValue(item)
			if err != nil {
				return nil, err
			}
			value[key] = normalized
		}
		return value, nil
	default:
		return value, nil
	}
}

// equalCatalogJSON compares decoded JSON values so probe selection treats
// whitespace, key-order, or numeric-spelling variants of one value as the
// same State. Numbers compare by exact rational value, never float64.
func equalCatalogJSON(left, right json.RawMessage) (bool, error) {
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return normalizeCatalogJSONValue(value)
	}
	leftValue, err := decode(left)
	if err != nil {
		return false, err
	}
	rightValue, err := decode(right)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}

// compactCatalogJSON returns the single-line form of an authored example value
// for normalized value assertions. Comparison is order-insensitive (see
// equalCatalogJSON), so this only strips insignificant whitespace.
func compactCatalogJSON(raw json.RawMessage) (string, error) {
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		return "", err
	}
	return compacted.String(), nil
}

// writeCatalogEqualityHelpers emits the generated-test imports and the exact
// rational JSON equality helpers shared by every catalog wiring assertion.
func writeCatalogEqualityHelpers(source *strings.Builder, probes []catalogProbe) {
	hasOperations := false
	for _, probe := range probes {
		if len(probe.operations) > 0 {
			hasOperations = true
			break
		}
	}
	if hasOperations {
		source.WriteString("import (\n\t\"bytes\"\n\t\"encoding/json\"\n\t\"math/big\"\n\t\"reflect\"\n")
		source.WriteString("\t\"testing\"\n\t\"time\"\n)\n\n")
	} else {
		source.WriteString("import (\n\t\"bytes\"\n\t\"encoding/json\"\n")
		source.WriteString("\t\"math/big\"\n\t\"reflect\"\n\t\"testing\"\n)\n\n")
	}
	source.WriteString("// equalGeneratedCatalogJSON compares normalized catalog output against the\n")
	source.WriteString("// authored example independent of key order, whitespace, or numeric spelling.\n")
	source.WriteString("// Numbers compare by exact rational value, never float64.\n")
	source.WriteString("func equalGeneratedCatalogJSON(left, right []byte) bool {\n")
	source.WriteString("\tleftValue, leftErr := decodeGeneratedCatalogJSON(left)\n")
	source.WriteString("\trightValue, rightErr := decodeGeneratedCatalogJSON(right)\n")
	source.WriteString("\tif leftErr != nil || rightErr != nil { return false }\n")
	source.WriteString("\treturn reflect.DeepEqual(leftValue, rightValue)\n")
	source.WriteString("}\n\n")
	source.WriteString("type generatedCatalogJSONNumber string\n\n")
	source.WriteString("func normalizeGeneratedCatalogJSON(value any) any {\n")
	source.WriteString("\tswitch value := value.(type) {\n")
	source.WriteString("\tcase json.Number:\n")
	source.WriteString("\t\trational, ok := new(big.Rat).SetString(value.String())\n")
	source.WriteString("\t\tif !ok { return value }\n")
	source.WriteString("\t\treturn generatedCatalogJSONNumber(rational.RatString())\n")
	source.WriteString("\tcase []any:\n")
	source.WriteString("\t\tfor index, item := range value {\n")
	source.WriteString("\t\t\tvalue[index] = normalizeGeneratedCatalogJSON(item)\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t\treturn value\n")
	source.WriteString("\tcase map[string]any:\n")
	source.WriteString("\t\tfor key, item := range value {\n")
	source.WriteString("\t\t\tvalue[key] = normalizeGeneratedCatalogJSON(item)\n")
	source.WriteString("\t\t}\n")
	source.WriteString("\t\treturn value\n")
	source.WriteString("\tdefault:\n")
	source.WriteString("\t\treturn value\n")
	source.WriteString("\t}\n")
	source.WriteString("}\n\n")
	source.WriteString("func decodeGeneratedCatalogJSON(raw []byte) (any, error) {\n")
	source.WriteString("\tdecoder := json.NewDecoder(bytes.NewReader(raw))\n")
	source.WriteString("\tdecoder.UseNumber()\n")
	source.WriteString("\tvar value any\n")
	source.WriteString("\tif err := decoder.Decode(&value); err != nil { return nil, err }\n")
	source.WriteString("\treturn normalizeGeneratedCatalogJSON(value), nil\n")
	source.WriteString("}\n\n")
}

func renderCatalogConformanceTest(models []entityTypeModel, moduleRoot string) (output, error) {
	ordered := append([]entityTypeModel(nil), models...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].TypeID < ordered[right].TypeID })
	checker := newCatalogSchemaChecker()
	probes := make([]catalogProbe, 0, len(ordered))
	for _, model := range ordered {
		probe, err := selectCatalogProbe(model, checker)
		if err != nil {
			return output{}, err
		}
		probes = append(probes, probe)
	}
	var source strings.Builder
	generatedHeader(&source)
	source.WriteString("package devices\n\n")
	writeCatalogEqualityHelpers(&source, probes)
	source.WriteString("func TestGeneratedBuiltinCatalogWiring(t *testing.T) {\n")
	source.WriteString("\tcatalog, err := NewBuiltinTypeCatalog()\n\tif err != nil { t.Fatal(err) }\n")
	for _, probe := range probes {
		if err := writeCatalogProbe(&source, probe); err != nil {
			return output{}, err
		}
	}
	source.WriteString("}\n")
	formatted, err := formatGenerated(source.String())
	if err != nil {
		return output{}, err
	}
	return output{
		path:    filepath.Join(moduleRoot, "internal", "modules", "devices", "zz_generated_entitytypes_test.go"),
		content: formatted,
	}, nil
}

func writeCatalogProbe(source *strings.Builder, probe catalogProbe) error {
	model := probe.model
	fmt.Fprintf(source, "\tt.Run(%s, func(t *testing.T) {\n", strconv.Quote(model.TypeID))
	support, err := compactCatalogJSON(probe.support)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\tentity := Entity{ID: EntityID(%s), TypeID: EntityType%s, Support: EntitySupport(%s)}\n",
		strconv.Quote("generated_"+model.Package),
		entityTypeGoName(model),
		strconv.Quote(support),
	)
	source.WriteString(
		"\t\tnormalizedSupport, err := catalog.NormalizeSupport(entity.TypeID, entity.Support)\n" +
			"\t\tif err != nil { t.Fatalf(\"catalog support: %v\", err) }\n",
	)
	fmt.Fprintf(
		source,
		"\t\tif !equalGeneratedCatalogJSON(normalizedSupport, []byte(%s)) { t.Errorf(\"catalog normalized support = %%s, want %%s\", normalizedSupport, %s) }\n",
		strconv.Quote(support),
		strconv.Quote(support),
	)
	validState, err := compactCatalogJSON(probe.validState)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\tnormalizedState, err := catalog.NormalizeState(entity, Value(%s))\n",
		strconv.Quote(validState),
	)
	source.WriteString("\t\tif err != nil { t.Fatalf(\"catalog State: %v\", err) }\n")
	fmt.Fprintf(
		source,
		"\t\tif !equalGeneratedCatalogJSON(normalizedState, []byte(%s)) { t.Errorf(\"catalog normalized State = %%s, want %%s\", normalizedState, %s) }\n",
		strconv.Quote(validState),
		strconv.Quote(validState),
	)
	if stateErr := writeCatalogInvalidState(source, probe); stateErr != nil {
		return stateErr
	}
	if operationsErr := writeCatalogOperations(source, probe); operationsErr != nil {
		return operationsErr
	}
	if equalityErr := writeCatalogEquality(source, probe, validState); equalityErr != nil {
		return equalityErr
	}
	source.WriteString("\t})\n")
	return nil
}

// writeCatalogInvalidState emits the support-invalid State rejection against
// its originating support.
func writeCatalogInvalidState(source *strings.Builder, probe catalogProbe) error {
	if len(probe.supportInvalidState) == 0 {
		return nil
	}
	invalidState, err := compactCatalogJSON(probe.supportInvalidState)
	if err != nil {
		return err
	}
	invalidEntity, entityErr := declareCatalogProbeEntity(
		source, probe, "entityInvalidState", probe.supportInvalidSupport,
	)
	if entityErr != nil {
		return entityErr
	}
	fmt.Fprintf(
		source,
		"\t\tif _, err := catalog.NormalizeState(%s, Value(%s)); err == nil { t.Error(\"catalog support-invalid State unexpectedly accepted\") }\n",
		invalidEntity,
		strconv.Quote(invalidState),
	)
	return nil
}

// writeCatalogOperations emits one wiring block per operation, each against
// its originating support.
func writeCatalogOperations(source *strings.Builder, probe catalogProbe) error {
	for _, operation := range probe.operations {
		opEntity, entityErr := declareCatalogProbeEntity(
			source, probe, "entity"+operation.model.GoName, operation.support,
		)
		if entityErr != nil {
			return entityErr
		}
		if needsCatalogInvalidParamsEntity(operation) {
			name := "entity" + operation.model.GoName + "Invalid"
			if _, invalidEntityErr := declareCatalogProbeEntity(
				source, probe, name, operation.invalidSupport,
			); invalidEntityErr != nil {
				return invalidEntityErr
			}
		}
		if operationErr := writeCatalogOperationProbe(source, probe, operation, opEntity); operationErr != nil {
			return operationErr
		}
	}
	return nil
}

// needsCatalogInvalidParamsEntity reports whether the invalid parameters rep
// needs its own entity because its originating support matches neither the
// shared probe support (handled by declaration reuse) nor the operation
// support.
func needsCatalogInvalidParamsEntity(operation catalogOperationProbe) bool {
	return len(operation.supportInvalidParams) > 0 && len(operation.invalidSupport) > 0 &&
		!catalogSupportsEqual(operation.support, operation.invalidSupport)
}

// writeCatalogEquality emits the equal-State check and the unequal-State
// check. The unequal probe keeps the authored valid State as incoming
// (validated against support) and the recorded candidate as persisted
// (decoded only), so a schema-valid but support-narrowed candidate cannot
// error the probe.
func writeCatalogEquality(source *strings.Builder, probe catalogProbe, validState string) error {
	fmt.Fprintf(
		source,
		"\t\tif equal, err := catalog.EqualState(entity, Value(%s), Value(%s)); err != nil || !equal { t.Errorf(\"catalog equal State = %%v, %%v\", equal, err) }\n",
		strconv.Quote(validState),
		strconv.Quote(validState),
	)
	if len(probe.unequalState) == 0 {
		return nil
	}
	unequalState, err := compactCatalogJSON(probe.unequalState)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\tif equal, err := catalog.EqualState(entity, Value(%s), Value(%s)); err != nil || equal { t.Errorf(\"catalog unequal State = %%v, %%v\", equal, err) }\n",
		strconv.Quote(unequalState),
		strconv.Quote(validState),
	)
	return nil
}

// catalogSupportsEqual reports whether two authored supports carry the same
// JSON value independent of whitespace or key order.
func catalogSupportsEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	equal, err := equalCatalogJSON(left, right)
	if err != nil {
		return false
	}
	return equal
}

// declareCatalogProbeEntity emits a dedicated entity variable for a
// support-dependent probe unless its support matches the shared probe
// support, in which case it reuses "entity" and emits nothing.
func declareCatalogProbeEntity(
	source *strings.Builder,
	probe catalogProbe,
	name string,
	support json.RawMessage,
) (string, error) {
	if len(support) == 0 || catalogSupportsEqual(probe.support, support) {
		return "entity", nil
	}
	compacted, err := compactCatalogJSON(support)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(
		source,
		"\t\t%s := Entity{ID: EntityID(%s), TypeID: EntityType%s, Support: EntitySupport(%s)}\n",
		name,
		strconv.Quote("generated_"+probe.model.Package),
		entityTypeGoName(probe.model),
		strconv.Quote(compacted),
	)
	return name, nil
}

func writeCatalogOperationProbe(
	source *strings.Builder,
	parent catalogProbe,
	probe catalogOperationProbe,
	opEntity string,
) error {
	operation := probe.model
	variable := "resolved" + operation.GoName
	parameters, err := compactCatalogJSON(probe.parameters)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\t%s, err := catalog.ResolveCommand(%s, OperationName(%s), CommandParameters(%s))\n",
		variable,
		opEntity,
		strconv.Quote(operation.Name),
		strconv.Quote(parameters),
	)
	fmt.Fprintf(
		source,
		"\t\tif err != nil { t.Fatalf(\"catalog resolve %s: %%v\", err) }\n",
		operation.Name,
	)
	fmt.Fprintf(
		source,
		"\t\tif !equalGeneratedCatalogJSON(%s.Parameters, []byte(%s)) { t.Errorf(\"catalog normalized %s parameters = %%s, want %%s\", %s.Parameters, %s) }\n",
		variable,
		strconv.Quote(parameters),
		operation.Name,
		variable,
		strconv.Quote(parameters),
	)
	fmt.Fprintf(
		source,
		"\t\tif %s.Deadline != %d*time.Millisecond { t.Errorf(\"catalog %s deadline = %%v, want %%v\", %s.Deadline, %d*time.Millisecond) }\n",
		variable,
		operation.DeadlineMS,
		operation.Name,
		variable,
		operation.DeadlineMS,
	)
	if invalidErr := writeCatalogInvalidParams(source, parent, probe, opEntity); invalidErr != nil {
		return invalidErr
	}
	satisfiedParameters, err := compactCatalogJSON(probe.satisfiedParameters)
	if err != nil {
		return err
	}
	satisfiedState, err := compactCatalogJSON(probe.satisfiedState)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\tif satisfied, err := catalog.Satisfies(%s, CommandRecord{OperationName: OperationName(%s), Parameters: CommandParameters(%s)}, Value(%s)); err != nil || !satisfied { t.Errorf(\"catalog %s satisfied outcome = %%v, %%v\", satisfied, err) }\n",
		opEntity,
		strconv.Quote(operation.Name),
		strconv.Quote(satisfiedParameters),
		strconv.Quote(satisfiedState),
		operation.Name,
	)
	unsatisfiedParameters, err := compactCatalogJSON(probe.unsatisfiedParameters)
	if err != nil {
		return err
	}
	unsatisfiedState, err := compactCatalogJSON(probe.unsatisfiedState)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\tif satisfied, err := catalog.Satisfies(%s, CommandRecord{OperationName: OperationName(%s), Parameters: CommandParameters(%s)}, Value(%s)); err != nil || satisfied { t.Errorf(\"catalog %s unsatisfied outcome = %%v, %%v\", satisfied, err) }\n",
		opEntity,
		strconv.Quote(operation.Name),
		strconv.Quote(unsatisfiedParameters),
		strconv.Quote(unsatisfiedState),
		operation.Name,
	)
	return nil
}

// writeCatalogInvalidParams emits the support-invalid parameters rejection
// against its originating support.
func writeCatalogInvalidParams(
	source *strings.Builder,
	parent catalogProbe,
	probe catalogOperationProbe,
	opEntity string,
) error {
	if len(probe.supportInvalidParams) == 0 {
		return nil
	}
	invalid, err := compactCatalogJSON(probe.supportInvalidParams)
	if err != nil {
		return err
	}
	operation := probe.model
	fmt.Fprintf(
		source,
		"\t\tif _, err := catalog.ResolveCommand(%s, OperationName(%s), CommandParameters(%s)); err == nil { t.Error(\"catalog support-invalid %s parameters unexpectedly accepted\") }\n",
		catalogInvalidParamsEntity(parent, probe, opEntity),
		strconv.Quote(operation.Name),
		strconv.Quote(invalid),
		operation.Name,
	)
	return nil
}

// catalogInvalidParamsEntity resolves the generated entity variable holding
// the invalid parameters originating support.
func catalogInvalidParamsEntity(parent catalogProbe, probe catalogOperationProbe, opEntity string) string {
	if len(probe.invalidSupport) == 0 || catalogSupportsEqual(probe.support, probe.invalidSupport) {
		return opEntity
	}
	if catalogSupportsEqual(parent.support, probe.invalidSupport) {
		return "entity"
	}
	return "entity" + probe.model.GoName + "Invalid"
}
