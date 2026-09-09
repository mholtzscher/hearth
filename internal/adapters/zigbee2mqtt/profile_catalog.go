package zigbee2mqtt

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// This file loads the repository-owned embedded Zigbee2MQTT profile catalog.
// Version 1 accepts only embedded documents: there is no operator or runtime
// catalog input and no generated effective catalog. The loader compiles the
// full catalog before the Adapter makes any external connection; any failure
// returns no partial catalog.

//go:embed profiles
var embeddedProfileFiles embed.FS

// Embedded profile catalog layout. Filenames determine deterministic
// diagnostic order but never planning precedence; profiles carry an explicit
// unique order instead.
const (
	embeddedProfileRoot       = "profiles"
	embeddedProfileSchemaPath = "profiles/profile.schema.json"
	embeddedPlannerSuffix     = ".profile.json"
	embeddedOverrideSuffix    = ".override.json"
	profileSchemaResourceID   = "urn:hearth:schema:zigbee2mqtt-profile:v1"
)

// Catalog size limits. Per-document group, rule, and patch bounds are
// schema-expressible; the document byte limit, the document count, and the
// total rule count are enforced semantically by the compiler.
const (
	profileCatalogMaxDocumentBytes      = 256 * 1024
	profileCatalogMaxDocuments          = 64
	profileCatalogMaxGroupsPerProfile   = 64
	profileCatalogMaxRulesPerGroup      = 64
	profileCatalogMaxDeviceRules        = 64
	profileCatalogMaxPatchesPerOverride = 64
	profileCatalogMaxTotalRules         = 512
)

// LoadEmbeddedProfileCatalog validates and compiles every repository-owned
// profile before the Adapter makes any external connection. It returns a
// fail-fast startup error without a partial catalog when any document is
// invalid. Profiles select from the closed production strategy registry, so
// strategy resolution and parameter compilation run here at startup.
func LoadEmbeddedProfileCatalog() (*ProfileCatalog, error) {
	return loadEmbeddedProfileCatalogWithRegistry(defaultProfileStrategyRegistry())
}

// loadEmbeddedProfileCatalogWithRegistry compiles the embedded profile
// documents against one injected strategy registry so tests can prove the
// compiler without the production strategy set.
func loadEmbeddedProfileCatalogWithRegistry(strategies profileStrategyRegistry) (*ProfileCatalog, error) {
	files, err := readEmbeddedProfileDocuments(embeddedProfileFiles)
	if err != nil {
		return nil, err
	}
	return compileProfileCatalogFiles(files, strategies)
}

// profileCatalogFile is one embedded profile document in lexical path order.
type profileCatalogFile struct {
	path string
	data []byte
}

// readEmbeddedProfileDocuments lists every planner and override document under
// the embedded profiles root in lexical path order. The authoritative JSON
// Schema file itself is metadata and never a catalog document.
func readEmbeddedProfileDocuments(filesystem fs.FS) ([]profileCatalogFile, error) {
	var paths []string
	walkErr := fs.WalkDir(filesystem, embeddedProfileRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, embeddedPlannerSuffix) || strings.HasSuffix(path, embeddedOverrideSuffix) {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: embeddedProfileRoot,
			Err:      fmt.Errorf("zigbee2mqtt list embedded profile documents: %w", walkErr),
		}
	}
	sort.Strings(paths)
	files := make([]profileCatalogFile, 0, len(paths))
	for _, path := range paths {
		data, readErr := fs.ReadFile(filesystem, path)
		if readErr != nil {
			return nil, &ProfileCatalogError{
				Code:     profileCatalogErrorSchemaInvalid,
				Document: path,
				Err:      fmt.Errorf("zigbee2mqtt read embedded profile document: %w", readErr),
			}
		}
		files = append(files, profileCatalogFile{path: path, data: data})
	}
	return files, nil
}

// compileProfileCatalogFiles validates and compiles one file set in lexical
// path order. Files are sorted defensively so in-memory callers get the same
// deterministic diagnostics as the embedded loader.
func compileProfileCatalogFiles(
	files []profileCatalogFile,
	strategies profileStrategyRegistry,
) (*ProfileCatalog, error) {
	ordered := make([]profileCatalogFile, len(files))
	copy(ordered, files)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].path < ordered[j].path })

	if len(ordered) > profileCatalogMaxDocuments {
		overflow := ordered[profileCatalogMaxDocuments]
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorCatalogLimitExceeded,
			Document: overflow.path,
			Err: fmt.Errorf("zigbee2mqtt profile catalog exceeds %d documents with %q",
				profileCatalogMaxDocuments, overflow.path),
		}
	}

	schema, err := compileProfileSchema()
	if err != nil {
		return nil, err
	}

	decoded := make([]decodedProfileDocument, 0, len(ordered))
	for _, file := range ordered {
		document, decodeErr := decodeProfileDocument(file, schema)
		if decodeErr != nil {
			return nil, decodeErr
		}
		decoded = append(decoded, document)
	}
	return compileDecodedProfileCatalog(decoded, strategies)
}

// decodedProfileDocument is one schema-validated document awaiting semantic
// compilation. Exactly one of planner or override is set, matching kind.
type decodedProfileDocument struct {
	path     string
	planner  *plannerProfileDocument
	override *profileOverrideDocument
}

// decodeProfileDocument enforces the file size limit, decodes exactly one JSON
// value so trailing data fails startup, schema-validates the value, and
// decodes it into the typed document matching its discriminated kind.
func decodeProfileDocument(file profileCatalogFile, schema *jsonschema.Schema) (decodedProfileDocument, error) {
	if len(file.data) > profileCatalogMaxDocumentBytes {
		return decodedProfileDocument{}, &ProfileCatalogError{
			Code:     profileCatalogErrorDocumentTooLarge,
			Document: file.path,
			Err:      fmt.Errorf("zigbee2mqtt profile document exceeds %d bytes", profileCatalogMaxDocumentBytes),
		}
	}

	value, decodeErr := decodeSingleProfileJSONValue(file)
	if decodeErr != nil {
		return decodedProfileDocument{}, decodeErr
	}
	if schemaErr := validateProfileDocumentSchema(file.path, value, schema); schemaErr != nil {
		return decodedProfileDocument{}, schemaErr
	}

	var header struct {
		Kind profileDocumentKind `json:"kind"`
		ID   string              `json:"id"`
	}
	if headerErr := remarshalProfileValue(file.path, "", "", value, &header); headerErr != nil {
		return decodedProfileDocument{}, headerErr
	}
	switch header.Kind {
	case profileDocumentPlanner:
		var planner plannerProfileDocument
		if plannerErr := remarshalProfileValue(file.path, header.ID, "", value, &planner); plannerErr != nil {
			return decodedProfileDocument{}, plannerErr
		}
		return decodedProfileDocument{path: file.path, planner: &planner}, nil
	case profileDocumentOverride:
		var override profileOverrideDocument
		if overrideErr := remarshalProfileValue(file.path, header.ID, "", value, &override); overrideErr != nil {
			return decodedProfileDocument{}, overrideErr
		}
		return decodedProfileDocument{path: file.path, override: &override}, nil
	default:
		return decodedProfileDocument{}, &ProfileCatalogError{
			Code:        profileCatalogErrorSchemaInvalid,
			Document:    file.path,
			JSONPointer: "/kind",
			Err:         fmt.Errorf("zigbee2mqtt profile document has unknown kind %q", string(header.Kind)),
		}
	}
}

// decodeSingleProfileJSONValue decodes exactly one JSON value so a document
// with trailing data fails catalog compilation instead of silently ignoring
// the suffix.
func decodeSingleProfileJSONValue(file profileCatalogFile) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(file.data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: file.path,
			Err:      fmt.Errorf("zigbee2mqtt profile document is not one JSON value: %w", err),
		}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: file.path,
			Err:      fmt.Errorf("zigbee2mqtt profile document has trailing JSON after the first value"),
		}
	}
	return value, nil
}

// validateProfileDocumentSchema validates one decoded JSON value against the
// authoritative profile schema and reports the deepest instance pointer.
func validateProfileDocumentSchema(path string, value any, schema *jsonschema.Schema) error {
	if err := schema.Validate(value); err != nil {
		return &ProfileCatalogError{
			Code:        profileCatalogErrorSchemaInvalid,
			Document:    path,
			JSONPointer: profileSchemaValidationPointer(err),
			Err:         fmt.Errorf("zigbee2mqtt profile document fails schema validation: %w", err),
		}
	}
	return nil
}

// remarshalProfileValue converts one schema-validated JSON value into a typed
// profile document. The value already passed schema validation, so a failure
// here is a deterministic schema error rather than caller input.
func remarshalProfileValue(path, profileID, ruleID string, value any, target any) error {
	raw, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return &ProfileCatalogError{
			Code:      profileCatalogErrorSchemaInvalid,
			Document:  path,
			ProfileID: profileID,
			RuleID:    ruleID,
			Err:       fmt.Errorf("zigbee2mqtt profile document is not valid JSON: %w", marshalErr),
		}
	}
	if unmarshalErr := json.Unmarshal(raw, target); unmarshalErr != nil {
		return &ProfileCatalogError{
			Code:      profileCatalogErrorSchemaInvalid,
			Document:  path,
			ProfileID: profileID,
			RuleID:    ruleID,
			Err:       fmt.Errorf("zigbee2mqtt profile document does not match its kind: %w", unmarshalErr),
		}
	}
	return nil
}

// compileProfileSchema compiles the authoritative embedded profile schema.
// The schema file is repository-owned, so a compile failure is a deterministic
// startup error naming the schema document.
func compileProfileSchema() (*jsonschema.Schema, error) {
	raw, readErr := embeddedProfileFiles.ReadFile(embeddedProfileSchemaPath)
	if readErr != nil {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: embeddedProfileSchemaPath,
			Err:      fmt.Errorf("zigbee2mqtt read authoritative profile schema: %w", readErr),
		}
	}
	var schemaValue any
	if syntaxErr := json.Unmarshal(raw, &schemaValue); syntaxErr != nil {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: embeddedProfileSchemaPath,
			Err:      fmt.Errorf("zigbee2mqtt authoritative profile schema is not JSON: %w", syntaxErr),
		}
	}
	compiler := jsonschema.NewCompiler()
	if resourceErr := compiler.AddResource(profileSchemaResourceID, schemaValue); resourceErr != nil {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: embeddedProfileSchemaPath,
			Err:      fmt.Errorf("zigbee2mqtt load authoritative profile schema: %w", resourceErr),
		}
	}
	schema, compileErr := compiler.Compile(profileSchemaResourceID)
	if compileErr != nil {
		return nil, &ProfileCatalogError{
			Code:     profileCatalogErrorSchemaInvalid,
			Document: embeddedProfileSchemaPath,
			Err:      fmt.Errorf("zigbee2mqtt compile authoritative profile schema: %w", compileErr),
		}
	}
	return schema, nil
}

// profileSchemaValidationPointer reports the deepest JSON pointer for one
// schema validation failure so diagnostics name the first invalid value.
func profileSchemaValidationPointer(err error) string {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return ""
	}
	deepest := validation
	for len(deepest.Causes) > 0 && deepest.Causes[0] != nil {
		deepest = deepest.Causes[0]
	}
	return profileJSONPointer(deepest.InstanceLocation)
}

// profileJSONPointer escapes one token path as an RFC 6901 JSON pointer.
func profileJSONPointer(tokens []string) string {
	var pointer strings.Builder
	for _, token := range tokens {
		pointer.WriteByte('/')
		pointer.WriteString(strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1"))
	}
	return pointer.String()
}
