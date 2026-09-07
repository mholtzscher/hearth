package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	model               entityTypeModel
	support             json.RawMessage
	validState          json.RawMessage
	supportInvalidState json.RawMessage
	unequalState        json.RawMessage
	operations          []catalogOperationProbe
}

// catalogOperationProbe carries one representative valid command, its
// deadline, and one satisfied/unsatisfied outcome pair for a single operation.
type catalogOperationProbe struct {
	model                 operationModel
	parameters            json.RawMessage
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
	supportInvalid, err := selectSupportInvalidState(model, checker, stateSchemaPath)
	if err != nil {
		return catalogProbe{}, err
	}
	probe.supportInvalidState = supportInvalid
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
// schema rejection. It returns nil when every invalid example is
// schema-invalid.
func selectSupportInvalidState(
	model entityTypeModel,
	checker *catalogSchemaChecker,
	stateSchemaPath string,
) (json.RawMessage, error) {
	for _, example := range model.Examples.Cases {
		for _, state := range example.States {
			if state.Valid {
				continue
			}
			decodable, err := checker.decodable(stateSchemaPath, state.Value)
			if err != nil {
				return nil, err
			}
			if decodable {
				return state.Value, nil
			}
		}
	}
	return nil, nil
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

// selectCatalogOperationProbe picks one valid command, one support-level
// invalid parameters rep when the examples include one, and the first
// satisfied/unsatisfied outcome pair, all in source order.
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
		if paramsErr := selectCatalogCommandParams(checker, parametersSchemaPath, values, &probe); paramsErr != nil {
			return catalogOperationProbe{}, paramsErr
		}
		selectCatalogOutcomes(values, &probe)
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
	return probe, nil
}

// selectCatalogCommandParams picks the first valid parameters and the first
// support-level invalid parameters rep in source order.
func selectCatalogCommandParams(
	checker *catalogSchemaChecker,
	schemaPath string,
	values operationExamples,
	probe *catalogOperationProbe,
) error {
	for _, parameters := range values.Parameters {
		switch {
		case parameters.Valid && probe.parameters == nil:
			probe.parameters = parameters.Value
		case !parameters.Valid && probe.supportInvalidParams == nil:
			decodable, decodableErr := checker.decodable(schemaPath, parameters.Value)
			if decodableErr != nil {
				return decodableErr
			}
			if decodable {
				probe.supportInvalidParams = parameters.Value
			}
		}
	}
	return nil
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

// equalCatalogJSON compares decoded JSON values so probe selection treats
// whitespace or key-order variants of one value as the same State.
func equalCatalogJSON(left, right json.RawMessage) (bool, error) {
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
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
	hasOperations := false
	for _, probe := range probes {
		if len(probe.operations) > 0 {
			hasOperations = true
			break
		}
	}
	if hasOperations {
		source.WriteString("import (\n\t\"bytes\"\n\t\"encoding/json\"\n\t\"reflect\"\n")
		source.WriteString("\t\"testing\"\n\t\"time\"\n)\n\n")
	} else {
		source.WriteString("import (\n\t\"bytes\"\n\t\"encoding/json\"\n\t\"reflect\"\n\t\"testing\"\n)\n\n")
	}
	source.WriteString("// equalGeneratedCatalogJSON compares normalized catalog output against the\n")
	source.WriteString("// authored example independent of object key order or whitespace.\n")
	source.WriteString("func equalGeneratedCatalogJSON(left, right []byte) bool {\n")
	source.WriteString("\tleftValue, leftErr := decodeGeneratedCatalogJSON(left)\n")
	source.WriteString("\trightValue, rightErr := decodeGeneratedCatalogJSON(right)\n")
	source.WriteString("\tif leftErr != nil || rightErr != nil { return false }\n")
	source.WriteString("\treturn reflect.DeepEqual(leftValue, rightValue)\n")
	source.WriteString("}\n\n")
	source.WriteString("func decodeGeneratedCatalogJSON(raw []byte) (any, error) {\n")
	source.WriteString("\tdecoder := json.NewDecoder(bytes.NewReader(raw))\n")
	source.WriteString("\tdecoder.UseNumber()\n")
	source.WriteString("\tvar value any\n")
	source.WriteString("\tif err := decoder.Decode(&value); err != nil { return nil, err }\n")
	source.WriteString("\treturn value, nil\n")
	source.WriteString("}\n\n")
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
	if len(probe.supportInvalidState) > 0 {
		invalidState, invalidErr := compactCatalogJSON(probe.supportInvalidState)
		if invalidErr != nil {
			return invalidErr
		}
		fmt.Fprintf(
			source,
			"\t\tif _, err := catalog.NormalizeState(entity, Value(%s)); err == nil { t.Error(\"catalog support-invalid State unexpectedly accepted\") }\n",
			strconv.Quote(invalidState),
		)
	}
	for _, operation := range probe.operations {
		if operationErr := writeCatalogOperationProbe(source, operation); operationErr != nil {
			return operationErr
		}
	}
	fmt.Fprintf(
		source,
		"\t\tif equal, err := catalog.EqualState(entity, Value(%s), Value(%s)); err != nil || !equal { t.Errorf(\"catalog equal State = %%v, %%v\", equal, err) }\n",
		strconv.Quote(validState),
		strconv.Quote(validState),
	)
	if len(probe.unequalState) > 0 {
		unequalState, unequalErr := compactCatalogJSON(probe.unequalState)
		if unequalErr != nil {
			return unequalErr
		}
		fmt.Fprintf(
			source,
			"\t\tif equal, err := catalog.EqualState(entity, Value(%s), Value(%s)); err != nil || equal { t.Errorf(\"catalog unequal State = %%v, %%v\", equal, err) }\n",
			strconv.Quote(validState),
			strconv.Quote(unequalState),
		)
	}
	source.WriteString("\t})\n")
	return nil
}

func writeCatalogOperationProbe(source *strings.Builder, probe catalogOperationProbe) error {
	operation := probe.model
	variable := "resolved" + operation.GoName
	parameters, err := compactCatalogJSON(probe.parameters)
	if err != nil {
		return err
	}
	fmt.Fprintf(
		source,
		"\t\t%s, err := catalog.ResolveCommand(entity, OperationName(%s), CommandParameters(%s))\n",
		variable,
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
	if len(probe.supportInvalidParams) > 0 {
		invalid, paramsErr := compactCatalogJSON(probe.supportInvalidParams)
		if paramsErr != nil {
			return paramsErr
		}
		fmt.Fprintf(
			source,
			"\t\tif _, err := catalog.ResolveCommand(entity, OperationName(%s), CommandParameters(%s)); err == nil { t.Error(\"catalog support-invalid %s parameters unexpectedly accepted\") }\n",
			strconv.Quote(operation.Name),
			strconv.Quote(invalid),
			operation.Name,
		)
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
		"\t\tif satisfied, err := catalog.Satisfies(entity, CommandRecord{OperationName: OperationName(%s), Parameters: CommandParameters(%s)}, Value(%s)); err != nil || !satisfied { t.Errorf(\"catalog %s satisfied outcome = %%v, %%v\", satisfied, err) }\n",
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
		"\t\tif satisfied, err := catalog.Satisfies(entity, CommandRecord{OperationName: OperationName(%s), Parameters: CommandParameters(%s)}, Value(%s)); err != nil || satisfied { t.Errorf(\"catalog %s unsatisfied outcome = %%v, %%v\", satisfied, err) }\n",
		strconv.Quote(operation.Name),
		strconv.Quote(unsatisfiedParameters),
		strconv.Quote(unsatisfiedState),
		operation.Name,
	)
	return nil
}
