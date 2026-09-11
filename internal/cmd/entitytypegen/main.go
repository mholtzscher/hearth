// Command entitytypegen generates Go Entity-type bindings and typed SDK facades.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"go/token"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	typeIDPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*/v[1-9][0-9]*$`)
	operationPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	packageNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type manifest struct {
	ManifestVersion   int                          `json:"manifest_version"`
	TypeID            string                       `json:"type"`
	StateSchema       string                       `json:"state_schema"`
	SupportSchema     string                       `json:"support_schema"`
	Stateless         bool                         `json:"stateless,omitempty"`
	EventSource       bool                         `json:"event_source,omitempty"`
	StateValidation   []ruleManifest               `json:"state_validation,omitempty"`
	SupportValidation []ruleManifest               `json:"support_validation,omitempty"`
	Operations        map[string]operationManifest `json:"operations"`
	Examples          string                       `json:"examples"`
}

type operationManifest struct {
	ParametersSchema    string         `json:"parameters_schema"`
	DeadlineMS          int64          `json:"deadline_ms"`
	Outcome             string         `json:"outcome"`
	ParameterValidation []ruleManifest `json:"parameter_validation,omitempty"`
	SatisfiedWhen       []ruleManifest `json:"satisfied_when"`
}

type ruleManifest struct {
	Op        string             `json:"op"`
	Left      referenceManifest  `json:"left"`
	Right     *referenceManifest `json:"right"`
	Tolerance *json.Number       `json:"tolerance"`
	Modulus   *json.Number       `json:"modulus"`
}

type referenceManifest struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

type schemaNode struct {
	ID                   string                `json:"$id"`
	Type                 string                `json:"type"`
	Properties           map[string]schemaNode `json:"properties"`
	Required             []string              `json:"required"`
	Items                *schemaNode           `json:"items"`
	Pattern              string                `json:"pattern"`
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
	MaxProperties        *int                  `json:"maxProperties"`
	Minimum              *json.Number          `json:"minimum"`
	Maximum              *json.Number          `json:"maximum"`
	MinLength            *int                  `json:"minLength"`
	MaxLength            *int                  `json:"maxLength"`
	MinItems             *int                  `json:"minItems"`
	MaxItems             *int                  `json:"maxItems"`
	UniqueItems          *bool                 `json:"uniqueItems"`
}

type operationModel struct {
	Name                string
	GoName              string
	ParametersFile      string
	ParametersSchema    schemaNode
	SupportSchema       schemaNode
	Required            bool
	DeadlineMS          int64
	Outcome             string
	ParameterValidation []ruleModel
	SatisfiedWhen       []ruleModel
}

type entityTypeModel struct {
	Package            string
	Directory          string
	ModuleRoot         string
	TypeID             string
	ExamplesFile       string
	StateFile          string
	StateSchema        schemaNode
	SupportFile        string
	SupportSchema      schemaNode
	StateSupport       schemaNode
	Stateless          bool
	EventSource        bool
	EventSupportSchema schemaNode
	StateValidation    []ruleModel
	SupportValidation  []ruleModel
	Operations         []operationModel
	Examples           examplesFile
}

type output struct {
	path    string
	content []byte
}

func main() {
	root := flag.String("root", "", "repository root containing Entity-type manifests")
	check := flag.Bool("check", false, "check generated files without writing them")
	flag.Parse()

	if *root == "" {
		fatal(errors.New("-root is required"))
	}
	if err := generateRoot(*root, *check); err != nil {
		fatal(err)
	}
}

func generateRoot(root string, check bool) error {
	absoluteRoot, absoluteRootErr := filepath.Abs(root)
	if absoluteRootErr != nil {
		return absoluteRootErr
	}
	manifests, globErr := filepath.Glob(filepath.Join(absoluteRoot, "entitytypes", "*", "entitytype.json"))
	if globErr != nil {
		return globErr
	}
	if len(manifests) == 0 {
		return fmt.Errorf("no Entity-type manifests found under %s", root)
	}
	sort.Strings(manifests)
	models := make([]entityTypeModel, 0, len(manifests))
	seenTypeIDs := make(map[string]string, len(manifests))
	seenGoNames := make(map[string]string, len(manifests))
	for _, path := range manifests {
		model, loadErr := loadModel(path)
		if loadErr != nil {
			return fmt.Errorf("load %s: %w", path, loadErr)
		}
		if previous, duplicate := seenTypeIDs[model.TypeID]; duplicate {
			return fmt.Errorf("duplicate Entity type ID %q in %s and %s", model.TypeID, previous, path)
		}
		seenTypeIDs[model.TypeID] = path
		goName := entityTypeGoName(model)
		if previous, duplicate := seenGoNames[goName]; duplicate {
			return fmt.Errorf(
				"entity-type packages %q and %q both map to generated Go name %q",
				previous,
				model.Package,
				goName,
			)
		}
		seenGoNames[goName] = model.Package
		models = append(models, model)
	}
	modulePath, modulePathErr := readModulePath(absoluteRoot)
	if modulePathErr != nil {
		return modulePathErr
	}
	var outputs []output
	for _, model := range models {
		generated, renderErr := render(model, modulePath)
		if renderErr != nil {
			return fmt.Errorf("render %s: %w", model.TypeID, renderErr)
		}
		outputs = append(outputs, generated...)
	}
	catalog, err := renderCatalog(models, modulePath, absoluteRoot)
	if err != nil {
		return fmt.Errorf("render built-in catalog: %w", err)
	}
	catalogTest, err := renderCatalogConformanceTest(models, absoluteRoot)
	if err != nil {
		return fmt.Errorf("render built-in catalog conformance test: %w", err)
	}
	outputs = append(outputs, catalog, catalogTest)
	return applyOutputs(absoluteRoot, outputs, check)
}

//nolint:gocognit,gocyclo,cyclop,funlen // Model loading validates the manifest in document order in one linear pass.
func loadModel(path string) (entityTypeModel, error) {
	directory := filepath.Dir(path)
	moduleRoot, moduleRootErr := findModuleRoot(directory)
	if moduleRootErr != nil {
		return entityTypeModel{}, moduleRootErr
	}
	if err := validateManifest(moduleRoot, path); err != nil {
		return entityTypeModel{}, err
	}

	var definition manifest
	if err := decodeStrictFile(path, &definition); err != nil {
		return entityTypeModel{}, err
	}
	if definition.ManifestVersion != 1 {
		return entityTypeModel{}, fmt.Errorf("unsupported manifest_version %d", definition.ManifestVersion)
	}
	if !typeIDPattern.MatchString(definition.TypeID) || len(definition.TypeID) > 128 {
		return entityTypeModel{}, fmt.Errorf("invalid Entity type ID %q", definition.TypeID)
	}
	if definition.StateSchema == "" || definition.SupportSchema == "" {
		return entityTypeModel{}, errors.New("state_schema and support_schema are required")
	}
	packageName := filepath.Base(directory)
	if !packageNamePattern.MatchString(packageName) || !token.IsIdentifier(packageName) ||
		token.Lookup(packageName).IsKeyword() ||
		packageName == "main" ||
		packageName == "internal" ||
		packageName == "_" {
		return entityTypeModel{}, fmt.Errorf("manifest directory %q is not an importable Go package name", packageName)
	}
	state, stateErr := loadSchema(directory, definition.StateSchema)
	if stateErr != nil {
		return entityTypeModel{}, fmt.Errorf("state schema: %w", stateErr)
	}
	support, supportErr := loadSchema(directory, definition.SupportSchema)
	if supportErr != nil {
		return entityTypeModel{}, fmt.Errorf("support schema: %w", supportErr)
	}
	if state.ID == "" || support.ID == "" {
		return entityTypeModel{}, errors.New("state and support schemas require $id")
	}
	if support.Type != schemaTypeObject {
		return entityTypeModel{}, errors.New("support schema must describe an object")
	}
	if err := requireClosedObject(support); err != nil {
		return entityTypeModel{}, fmt.Errorf("support schema: %w", err)
	}
	if !required(support, referenceRootState) || !required(support, "operations") {
		return entityTypeModel{}, errors.New("support schema must require state and operations")
	}
	allowedSupportProperties := map[string]struct{}{referenceRootState: {}, "operations": {}}
	if definition.EventSource {
		allowedSupportProperties["events"] = struct{}{}
	}
	for _, property := range sortedProperties(support.Properties) {
		if _, allowed := allowedSupportProperties[property]; !allowed {
			return entityTypeModel{}, fmt.Errorf("support schema has unsupported top-level property %q", property)
		}
	}
	stateSupport, stateSupportExists := support.Properties[referenceRootState]
	if !stateSupportExists || stateSupport.Type != schemaTypeObject {
		return entityTypeModel{}, errors.New("support.state must describe an object")
	}
	operationSupport, operationSupportExists := support.Properties["operations"]
	if !operationSupportExists || operationSupport.Type != schemaTypeObject {
		return entityTypeModel{}, errors.New("support.operations must describe an object")
	}
	if err := requireClosedObject(operationSupport); err != nil {
		return entityTypeModel{}, fmt.Errorf("support.operations schema: %w", err)
	}
	var eventSupportSchema schemaNode
	if definition.EventSource {
		if !definition.Stateless {
			return entityTypeModel{}, errors.New("event_source requires stateless: true")
		}
		if len(definition.Operations) != 0 {
			return entityTypeModel{}, errors.New("event_source requires an empty operations map")
		}
		if stateShapeErr := requireEventSourceStateShape(state, stateSupport); stateShapeErr != nil {
			return entityTypeModel{}, stateShapeErr
		}
		events, eventsErr := requireEntityEventSupportSchema(support)
		if eventsErr != nil {
			return entityTypeModel{}, eventsErr
		}
		eventSupportSchema = events
	}
	if len(definition.Operations) != len(operationSupport.Properties) {
		return entityTypeModel{}, errors.New("manifest operations must exactly match support.operations properties")
	}

	names := make([]string, 0, len(definition.Operations))
	for name := range definition.Operations {
		names = append(names, name)
	}
	sort.Strings(names)
	operations := make([]operationModel, 0, len(names))
	seenGoNames := make(map[string]string, len(names))
	for _, name := range names {
		if !operationPattern.MatchString(name) {
			return entityTypeModel{}, fmt.Errorf("invalid operation name %q", name)
		}
		supportSchema, supportExists := operationSupport.Properties[name]
		if !supportExists {
			return entityTypeModel{}, fmt.Errorf("operation %q is absent from support schema", name)
		}
		if supportSchema.Type != schemaTypeObject {
			return entityTypeModel{}, fmt.Errorf("operation support %q must describe an object", name)
		}
		operation := definition.Operations[name]
		if operation.ParametersSchema == "" {
			return entityTypeModel{}, fmt.Errorf("operation %q has no parameters_schema", name)
		}
		parameters, parametersErr := loadSchema(directory, operation.ParametersSchema)
		if parametersErr != nil {
			return entityTypeModel{}, fmt.Errorf("operation %q parameters schema: %w", name, parametersErr)
		}
		if parameters.ID == "" {
			return entityTypeModel{}, fmt.Errorf("operation %q parameters schema requires $id", name)
		}
		if parameters.Type != schemaTypeObject {
			return entityTypeModel{}, fmt.Errorf("operation %q parameters schema must describe an object", name)
		}
		if operation.DeadlineMS <= 0 {
			return entityTypeModel{}, fmt.Errorf("operation %q deadline_ms must be positive", name)
		}
		if operation.DeadlineMS > maximumOperationDeadline {
			return entityTypeModel{}, fmt.Errorf("operation %q deadline_ms overflows time.Duration", name)
		}
		if operation.Outcome != outcomeObserved && operation.Outcome != outcomeDispatched {
			return entityTypeModel{}, fmt.Errorf(
				"operation %q has invalid outcome %q",
				name,
				operation.Outcome,
			)
		}
		if operation.Outcome == outcomeDispatched {
			if len(operation.SatisfiedWhen) != 0 {
				return entityTypeModel{}, fmt.Errorf(
					"operation %q is dispatched and must declare empty satisfied_when",
					name,
				)
			}
		} else if len(operation.SatisfiedWhen) == 0 {
			return entityTypeModel{}, fmt.Errorf("operation %q requires satisfied_when", name)
		}
		parameterValidation, validationErr := compileRules(operation.ParameterValidation, map[string]referenceRoot{
			referenceRootParameters: {Schema: parameters, GoExpression: referenceRootParameters},
			referenceRootSupport:    {Schema: support, GoExpression: referenceRootSupport},
			"operation_support":     {Schema: supportSchema, GoExpression: "operationSupport"},
		}, fmt.Sprintf("operation %q parameter validation", name))
		if validationErr != nil {
			return entityTypeModel{}, validationErr
		}
		satisfied, satisfiedErr := compileRules(operation.SatisfiedWhen, map[string]referenceRoot{
			referenceRootParameters: {Schema: parameters, GoExpression: referenceRootParameters},
			referenceRootState:      {Schema: state, GoExpression: referenceRootState},
		}, fmt.Sprintf("operation %q satisfied_when", name))
		if satisfiedErr != nil {
			return entityTypeModel{}, satisfiedErr
		}
		goName, nameErr := exportedName(name)
		if nameErr != nil {
			return entityTypeModel{}, nameErr
		}
		if previous, collision := seenGoNames[goName]; collision {
			return entityTypeModel{}, fmt.Errorf(
				"operation names %q and %q both map to Go name %q",
				previous,
				name,
				goName,
			)
		}
		seenGoNames[goName] = name
		operations = append(operations, operationModel{
			Name: name, GoName: goName, ParametersFile: operation.ParametersSchema,
			ParametersSchema: parameters, SupportSchema: supportSchema,
			Required: required(operationSupport, name), DeadlineMS: operation.DeadlineMS,
			Outcome:             operation.Outcome,
			ParameterValidation: parameterValidation, SatisfiedWhen: satisfied,
		})
	}
	for name := range operationSupport.Properties {
		if _, manifestExists := definition.Operations[name]; !manifestExists {
			return entityTypeModel{}, fmt.Errorf("support operation %q is absent from manifest", name)
		}
	}
	if err := requireUniqueSchemaIDs(state, support, operations); err != nil {
		return entityTypeModel{}, err
	}
	stateValidation, stateValidationErr := compileRules(definition.StateValidation, map[string]referenceRoot{
		referenceRootState:   {Schema: state, GoExpression: referenceRootState},
		referenceRootSupport: {Schema: support, GoExpression: referenceRootSupport},
	}, "state validation")
	if stateValidationErr != nil {
		return entityTypeModel{}, stateValidationErr
	}
	supportValidation, supportValidationErr := compileRules(definition.SupportValidation, map[string]referenceRoot{
		referenceRootSupport: {Schema: support, GoExpression: referenceRootSupport},
	}, "support validation")
	if supportValidationErr != nil {
		return entityTypeModel{}, supportValidationErr
	}
	examplesPath, examplesPathErr := normalizeExamplesPath(directory, definition.Examples)
	if examplesPathErr != nil {
		return entityTypeModel{}, fmt.Errorf("examples: %w", examplesPathErr)
	}
	examples, examplesErr := loadExamples(directory, examplesPath, operations)
	if examplesErr != nil {
		return entityTypeModel{}, fmt.Errorf("examples: %w", examplesErr)
	}
	if definition.EventSource {
		if eventExamplesErr := requireEventSourceExamples(examples); eventExamplesErr != nil {
			return entityTypeModel{}, fmt.Errorf("examples: %w", eventExamplesErr)
		}
	}
	if validationErr := checkInvalidSupports(
		directory,
		definition.SupportSchema,
		supportValidation,
		examples,
	); validationErr != nil {
		return entityTypeModel{}, validationErr
	}
	return entityTypeModel{
		Package: packageName, Directory: directory, ModuleRoot: moduleRoot, TypeID: definition.TypeID,
		ExamplesFile: examplesPath,
		StateFile:    definition.StateSchema, StateSchema: state,
		SupportFile: definition.SupportSchema, SupportSchema: support, StateSupport: stateSupport,
		Stateless:          definition.Stateless,
		EventSource:        definition.EventSource,
		EventSupportSchema: eventSupportSchema,
		StateValidation:    stateValidation, SupportValidation: supportValidation,
		Operations: operations, Examples: examples,
	}, nil
}

func validateManifest(moduleRoot, path string) error {
	const schemaID = "urn:hearth:schema:entity-type-manifest:v1"
	schemaPath := filepath.Join(moduleRoot, "entitytypes", "entitytype-manifest.schema.json")
	rawSchema, readErr := os.ReadFile(schemaPath)
	if readErr != nil {
		return fmt.Errorf("read manifest schema: %w", readErr)
	}
	document, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(rawSchema))
	if decodeErr != nil {
		return fmt.Errorf("decode manifest schema: %w", decodeErr)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return fmt.Errorf("add manifest schema: %w", err)
	}
	compiled, compileErr := compiler.Compile(schemaID)
	if compileErr != nil {
		return fmt.Errorf("compile manifest schema: %w", compileErr)
	}
	rawManifest, readErr := os.ReadFile(path)
	if readErr != nil {
		return readErr
	}
	value, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(rawManifest))
	if decodeErr != nil {
		return fmt.Errorf("decode manifest: %w", decodeErr)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("validate manifest schema: %w", err)
	}
	return nil
}

func loadSchema(directory, relative string) (schemaNode, error) {
	path, pathErr := localPath(directory, relative)
	if pathErr != nil {
		return schemaNode{}, pathErr
	}
	var schema schemaNode
	if err := decodeFile(path, &schema, false); err != nil {
		return schemaNode{}, err
	}
	if schema.Type == "" {
		return schemaNode{}, fmt.Errorf("schema %s has no type", relative)
	}
	if err := requireInt64Bindings(schema); err != nil {
		return schemaNode{}, err
	}
	return schema, nil
}

//nolint:gocognit // Recursive schema validation mirrors the supported JSON Schema node kinds.
func requireInt64Bindings(schema schemaNode) error {
	switch schema.Type {
	case string(kindInteger):
		if schema.Minimum == nil || schema.Maximum == nil {
			return errors.New("integer schema must set minimum and maximum within int64")
		}
		minimum, ok := new(big.Rat).SetString(schema.Minimum.String())
		if !ok {
			return fmt.Errorf("invalid integer minimum %q", schema.Minimum)
		}
		maximum, ok := new(big.Rat).SetString(schema.Maximum.String())
		if !ok {
			return fmt.Errorf("invalid integer maximum %q", schema.Maximum)
		}
		minimumInt64 := new(big.Rat).SetInt64(-1 << int64MagnitudeBits)
		maximumInt64 := new(big.Rat).SetInt64(1<<63 - 1)
		if minimum.Cmp(minimumInt64) < 0 || maximum.Cmp(maximumInt64) > 0 {
			return errors.New("integer schema minimum and maximum must fit int64")
		}
	case schemaTypeObject:
		for _, property := range sortedProperties(schema.Properties) {
			if err := requireInt64Bindings(schema.Properties[property]); err != nil {
				return fmt.Errorf("property %q: %w", property, err)
			}
		}
	case schemaTypeArray:
		if schema.Items != nil {
			if err := requireInt64Bindings(*schema.Items); err != nil {
				return fmt.Errorf("items: %w", err)
			}
		}
	}
	return nil
}

func requireUniqueSchemaIDs(state, support schemaNode, operations []operationModel) error {
	seen := make(map[string]string, len(operations)+builtinSchemaCount)
	add := func(id, location string) error {
		if previous, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate schema ID %q in %s and %s", id, previous, location)
		}
		seen[id] = location
		return nil
	}
	if err := add(state.ID, "state schema"); err != nil {
		return err
	}
	if err := add(support.ID, "support schema"); err != nil {
		return err
	}
	for _, operation := range operations {
		if err := add(
			operation.ParametersSchema.ID,
			fmt.Sprintf("operation %q parameters schema", operation.Name),
		); err != nil {
			return err
		}
	}
	return nil
}

func localPath(directory, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("path %q must be relative", relative)
	}
	path := filepath.Clean(filepath.Join(directory, relative))
	rel, err := filepath.Rel(directory, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes its Entity-type directory", relative)
	}
	return path, nil
}

func decodeStrictFile(path string, target any) error {
	return decodeFile(path, target, true)
}

func decodeFile(path string, target any, strict bool) error {
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return readErr
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// checkInvalidSupports keeps support-validation coverage structural:
// manifests with support_validation rules must author schema-decodable
// invalid supports (rejected by the generated catalog conformance test),
// and manifests without those rules must not carry any.
func checkInvalidSupports(
	directory, supportSchema string,
	supportValidation []ruleModel,
	examples examplesFile,
) error {
	if len(supportValidation) > 0 && len(examples.InvalidSupports) == 0 {
		return errors.New("support validation requires invalid_supports examples")
	}
	if len(supportValidation) == 0 && len(examples.InvalidSupports) > 0 {
		return errors.New("invalid_supports requires support validation rules")
	}
	if len(examples.InvalidSupports) == 0 {
		return nil
	}
	checker := newCatalogSchemaChecker()
	supportSchemaPath := filepath.Join(directory, supportSchema)
	for index, raw := range examples.InvalidSupports {
		if len(raw) == 0 {
			return fmt.Errorf("invalid_supports example %d has no value", index+1)
		}
		decodable, err := checker.decodable(supportSchemaPath, raw)
		if err != nil {
			return fmt.Errorf("invalid_supports example %d: %w", index+1, err)
		}
		if !decodable {
			return fmt.Errorf("invalid_supports example %d must schema-decode as support", index+1)
		}
	}
	return nil
}

func readModulePath(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", errors.New("go.mod has no module directive")
}

func findModuleRoot(start string) (string, error) {
	current := start
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("could not find go.mod")
		}
		current = parent
	}
}

func generatedHeader(source *strings.Builder) {
	source.WriteString("// Code generated by entitytypegen; DO NOT EDIT.\n\n")
}

func formatGenerated(source string) ([]byte, error) {
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, fmt.Errorf("format generated Go: %w\n%s", err, source)
	}
	return formatted, nil
}

func applyOutput(generated output, check bool) error {
	current, readErr := os.ReadFile(generated.path)
	if readErr == nil && bytes.Equal(current, generated.content) {
		return nil
	}
	if check {
		if errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("generated file %s is missing; run go generate ./entitytypes", generated.path)
		}
		return fmt.Errorf("generated file %s is stale; run go generate ./entitytypes", generated.path)
	}
	//nolint:gosec // Generated source directories use conventional repository permissions.
	if err := os.MkdirAll(filepath.Dir(generated.path), 0o755); err != nil {
		return err
	}
	//nolint:gosec // Generated source files must be readable by repository users.
	if err := os.WriteFile(generated.path, generated.content, 0o644); err != nil {
		return err
	}
	return nil
}

func required(schema schemaNode, property string) bool {
	return slices.Contains(schema.Required, property)
}

func sortedProperties(properties map[string]schemaNode) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func exportedName(value string) (string, error) {
	var result strings.Builder
	upperNext := true
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			upperNext = true
			continue
		}
		if result.Len() == 0 && unicode.IsDigit(character) {
			return "", fmt.Errorf("%q cannot map to an exported Go name", value)
		}
		if upperNext {
			character = unicode.ToUpper(character)
			upperNext = false
		}
		result.WriteRune(character)
	}
	if result.Len() == 0 {
		return "", fmt.Errorf("%q cannot map to an exported Go name", value)
	}
	return result.String(), nil
}

func lowerFirst(value string) string {
	characters := []rune(value)
	characters[0] = unicode.ToLower(characters[0])
	return string(characters)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "entitytypegen:", err)
	os.Exit(1)
}
