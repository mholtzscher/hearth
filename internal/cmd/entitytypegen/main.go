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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	typeIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*/v[1-9][0-9]*$`)
	operationPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
)

type manifest struct {
	ManifestVersion int                          `json:"manifest_version"`
	TypeID          string                       `json:"type"`
	StateSchema     string                       `json:"state_schema"`
	SupportSchema   string                       `json:"support_schema"`
	StateValidation []ruleManifest               `json:"state_validation,omitempty"`
	Operations      map[string]operationManifest `json:"operations"`
	Examples        string                       `json:"examples"`
}

type operationManifest struct {
	ParametersSchema    string         `json:"parameters_schema"`
	DeadlineMS          int64          `json:"deadline_ms"`
	ParameterValidation []ruleManifest `json:"parameter_validation,omitempty"`
	SatisfiedWhen       *ruleManifest  `json:"satisfied_when"`
}

type ruleManifest struct {
	Op    string            `json:"op"`
	Left  referenceManifest `json:"left"`
	Right referenceManifest `json:"right"`
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
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
	MaxProperties        *int                  `json:"maxProperties"`
}

type operationModel struct {
	Name                string
	GoName              string
	ParametersFile      string
	ParametersSchema    schemaNode
	SupportSchema       schemaNode
	Required            bool
	DeadlineMS          int64
	ParameterValidation []ruleModel
	SatisfiedWhen       ruleModel
}

type entityTypeModel struct {
	Package         string
	Directory       string
	ModuleRoot      string
	TypeID          string
	StateFile       string
	StateSchema     schemaNode
	SupportFile     string
	SupportSchema   schemaNode
	StateSupport    schemaNode
	StateValidation []ruleModel
	Operations      []operationModel
	Examples        examplesFile
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
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	manifests, err := filepath.Glob(filepath.Join(absoluteRoot, "entitytypes", "*", "entitytype.json"))
	if err != nil {
		return err
	}
	if len(manifests) == 0 {
		return fmt.Errorf("no Entity-type manifests found under %s", root)
	}
	sort.Strings(manifests)
	models := make([]entityTypeModel, 0, len(manifests))
	seenTypeIDs := make(map[string]string, len(manifests))
	seenGoNames := make(map[string]string, len(manifests))
	for _, path := range manifests {
		model, err := loadModel(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		if previous, duplicate := seenTypeIDs[model.TypeID]; duplicate {
			return fmt.Errorf("duplicate Entity type ID %q in %s and %s", model.TypeID, previous, path)
		}
		seenTypeIDs[model.TypeID] = path
		goName := entityTypeGoName(model)
		if previous, duplicate := seenGoNames[goName]; duplicate {
			return fmt.Errorf("Entity-type packages %q and %q both map to generated Go name %q", previous, model.Package, goName)
		}
		seenGoNames[goName] = model.Package
		models = append(models, model)
	}
	modulePath, err := readModulePath(absoluteRoot)
	if err != nil {
		return err
	}
	var outputs []output
	for _, model := range models {
		generated, err := render(model)
		if err != nil {
			return fmt.Errorf("render %s: %w", model.TypeID, err)
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

func loadModel(path string) (entityTypeModel, error) {
	directory := filepath.Dir(path)
	moduleRoot, err := findModuleRoot(directory)
	if err != nil {
		return entityTypeModel{}, err
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
	if !typeIDPattern.MatchString(definition.TypeID) {
		return entityTypeModel{}, fmt.Errorf("invalid Entity type ID %q", definition.TypeID)
	}
	if definition.StateSchema == "" || definition.SupportSchema == "" {
		return entityTypeModel{}, errors.New("state_schema and support_schema are required")
	}
	packageName := filepath.Base(directory)
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return entityTypeModel{}, fmt.Errorf("manifest directory %q is not a Go package name", packageName)
	}
	state, err := loadSchema(directory, definition.StateSchema)
	if err != nil {
		return entityTypeModel{}, fmt.Errorf("state schema: %w", err)
	}
	support, err := loadSchema(directory, definition.SupportSchema)
	if err != nil {
		return entityTypeModel{}, fmt.Errorf("support schema: %w", err)
	}
	if state.ID == "" || support.ID == "" {
		return entityTypeModel{}, errors.New("state and support schemas require $id")
	}
	if support.Type != "object" {
		return entityTypeModel{}, errors.New("support schema must describe an object")
	}
	if err := requireClosedObject(support); err != nil {
		return entityTypeModel{}, fmt.Errorf("support schema: %w", err)
	}
	if !required(support, "state") || !required(support, "operations") {
		return entityTypeModel{}, errors.New("support schema must require state and operations")
	}
	stateSupport, exists := support.Properties["state"]
	if !exists || stateSupport.Type != "object" {
		return entityTypeModel{}, errors.New("support.state must describe an object")
	}
	operationSupport, exists := support.Properties["operations"]
	if !exists || operationSupport.Type != "object" {
		return entityTypeModel{}, errors.New("support.operations must describe an object")
	}
	if err := requireClosedObject(operationSupport); err != nil {
		return entityTypeModel{}, fmt.Errorf("support.operations schema: %w", err)
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
		supportSchema, exists := operationSupport.Properties[name]
		if !exists {
			return entityTypeModel{}, fmt.Errorf("operation %q is absent from support schema", name)
		}
		if supportSchema.Type != "object" {
			return entityTypeModel{}, fmt.Errorf("operation support %q must describe an object", name)
		}
		operation := definition.Operations[name]
		if operation.ParametersSchema == "" {
			return entityTypeModel{}, fmt.Errorf("operation %q has no parameters_schema", name)
		}
		parameters, err := loadSchema(directory, operation.ParametersSchema)
		if err != nil {
			return entityTypeModel{}, fmt.Errorf("operation %q parameters schema: %w", name, err)
		}
		if parameters.ID == "" {
			return entityTypeModel{}, fmt.Errorf("operation %q parameters schema requires $id", name)
		}
		if parameters.Type != "object" {
			return entityTypeModel{}, fmt.Errorf("operation %q parameters schema must describe an object", name)
		}
		if operation.DeadlineMS <= 0 {
			return entityTypeModel{}, fmt.Errorf("operation %q deadline_ms must be positive", name)
		}
		if operation.DeadlineMS > 9_223_372_036_854 {
			return entityTypeModel{}, fmt.Errorf("operation %q deadline_ms overflows time.Duration", name)
		}
		if operation.SatisfiedWhen == nil {
			return entityTypeModel{}, fmt.Errorf("operation %q requires satisfied_when", name)
		}
		parameterValidation, err := compileRules(operation.ParameterValidation, map[string]referenceRoot{
			"parameters":        {Schema: parameters, GoExpression: "parameters"},
			"support":           {Schema: support, GoExpression: "support"},
			"operation_support": {Schema: supportSchema, GoExpression: "operationSupport"},
		}, fmt.Sprintf("operation %q parameter validation", name))
		if err != nil {
			return entityTypeModel{}, err
		}
		satisfied, err := compileRule(*operation.SatisfiedWhen, map[string]referenceRoot{
			"parameters": {Schema: parameters, GoExpression: "parameters"},
			"state":      {Schema: state, GoExpression: "state"},
		})
		if err != nil {
			return entityTypeModel{}, fmt.Errorf("operation %q satisfied_when: %w", name, err)
		}
		goName, err := exportedName(name)
		if err != nil {
			return entityTypeModel{}, err
		}
		if previous, collision := seenGoNames[goName]; collision {
			return entityTypeModel{}, fmt.Errorf("operation names %q and %q both map to Go name %q", previous, name, goName)
		}
		seenGoNames[goName] = name
		operations = append(operations, operationModel{
			Name: name, GoName: goName, ParametersFile: operation.ParametersSchema,
			ParametersSchema: parameters, SupportSchema: supportSchema,
			Required: required(operationSupport, name), DeadlineMS: operation.DeadlineMS,
			ParameterValidation: parameterValidation, SatisfiedWhen: satisfied,
		})
	}
	for name := range operationSupport.Properties {
		if _, exists := definition.Operations[name]; !exists {
			return entityTypeModel{}, fmt.Errorf("support operation %q is absent from manifest", name)
		}
	}
	stateValidation, err := compileRules(definition.StateValidation, map[string]referenceRoot{
		"state":   {Schema: state, GoExpression: "state"},
		"support": {Schema: support, GoExpression: "support"},
	}, "state validation")
	if err != nil {
		return entityTypeModel{}, err
	}
	examples, err := loadExamples(directory, definition.Examples, operations)
	if err != nil {
		return entityTypeModel{}, fmt.Errorf("examples: %w", err)
	}
	return entityTypeModel{
		Package: packageName, Directory: directory, ModuleRoot: moduleRoot, TypeID: definition.TypeID,
		StateFile: definition.StateSchema, StateSchema: state,
		SupportFile: definition.SupportSchema, SupportSchema: support, StateSupport: stateSupport,
		StateValidation: stateValidation, Operations: operations, Examples: examples,
	}, nil
}

func validateManifest(moduleRoot, path string) error {
	const schemaID = "urn:hearth:schema:entity-type-manifest:v1"
	schemaPath := filepath.Join(moduleRoot, "entitytypes", "entitytype-manifest.schema.json")
	rawSchema, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read manifest schema: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawSchema))
	if err != nil {
		return fmt.Errorf("decode manifest schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaID, document); err != nil {
		return fmt.Errorf("add manifest schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaID)
	if err != nil {
		return fmt.Errorf("compile manifest schema: %w", err)
	}
	rawManifest, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawManifest))
	if err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("validate manifest schema: %w", err)
	}
	return nil
}

func loadSchema(directory, relative string) (schemaNode, error) {
	path, err := localPath(directory, relative)
	if err != nil {
		return schemaNode{}, err
	}
	var schema schemaNode
	if err := decodeFile(path, &schema, false); err != nil {
		return schemaNode{}, err
	}
	if schema.Type == "" {
		return schemaNode{}, fmt.Errorf("schema %s has no type", relative)
	}
	return schema, nil
}

func localPath(directory, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("schema path %q must be relative", relative)
	}
	path := filepath.Clean(filepath.Join(directory, relative))
	rel, err := filepath.Rel(directory, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("schema path %q escapes its Entity-type directory", relative)
	}
	return path, nil
}

func decodeStrictFile(path string, target any) error {
	return decodeFile(path, target, true)
}

func decodeFile(path string, target any, strict bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
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

func readModulePath(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
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
	current, err := os.ReadFile(generated.path)
	if err == nil && bytes.Equal(current, generated.content) {
		return nil
	}
	if check {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("generated file %s is missing; run go generate ./entitytypes", generated.path)
		}
		return fmt.Errorf("generated file %s is stale; run go generate ./entitytypes", generated.path)
	}
	if err := os.MkdirAll(filepath.Dir(generated.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(generated.path, generated.content, 0o644); err != nil {
		return err
	}
	return nil
}

func required(schema schemaNode, property string) bool {
	for _, name := range schema.Required {
		if name == property {
			return true
		}
	}
	return false
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
