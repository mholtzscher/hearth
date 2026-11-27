package entitytypes

import (
	"cmp"
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// Schema diagnostic codes.
const (
	DiagnosticMissingProperty = "missing_property"
	DiagnosticUnknownProperty = "unknown_property"
	DiagnosticTypeMismatch    = "type_mismatch"
	DiagnosticConstraint      = "constraint_violation"
)

// SchemaDiagnostic locates one schema violation. Path names at most one
// top-level property the schema declares, and Allowed lists the schema's
// top-level property names for unknown_property, so neither echoes submitted
// keys or values. Command parameter schemas are flat objects.
type SchemaDiagnostic struct {
	Code    string   `json:"code"`
	Path    string   `json:"path"`
	Allowed []string `json:"allowed,omitempty"`
}

// SchemaViolationError is a value that failed its authoritative schema. Its error
// text is the validator's; callers that publish a rejection use Diagnostics.
type SchemaViolationError struct {
	Diagnostics []SchemaDiagnostic
	cause       error
}

func (violation *SchemaViolationError) Error() string { return violation.cause.Error() }

func (violation *SchemaViolationError) Unwrap() error { return violation.cause }

// newSchemaViolation adds safe diagnostics to a schema validation failure.
func newSchemaViolation(root *jsonschema.Schema, err error) error {
	validation, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return err
	}
	var found []SchemaDiagnostic
	collectSchemaDiagnostics(root, validation, &found)
	if len(found) == 0 {
		found = []SchemaDiagnostic{{Code: DiagnosticConstraint}}
	}
	slices.SortStableFunc(found, func(a, b SchemaDiagnostic) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.Code, b.Code))
	})
	found = slices.CompactFunc(found, func(a, b SchemaDiagnostic) bool {
		return a.Path == b.Path && a.Code == b.Code
	})
	return &SchemaViolationError{Diagnostics: found, cause: err}
}

func collectSchemaDiagnostics(root *jsonschema.Schema, issue *jsonschema.ValidationError, found *[]SchemaDiagnostic) {
	path, location := "", root
	if len(issue.InstanceLocation) > 0 {
		location = nil
		if token := issue.InstanceLocation[0]; root.Properties[token] != nil {
			path = "/" + token
		}
	}
	add := func(code, at string, allowed []string) {
		*found = append(*found, SchemaDiagnostic{Code: code, Path: at, Allowed: allowed})
	}
	switch problem := issue.ErrorKind.(type) {
	case *kind.Required:
		for _, missing := range problem.Missing {
			at := path
			if location != nil && location.Properties[missing] != nil {
				at = "/" + missing
			}
			add(DiagnosticMissingProperty, at, nil)
		}
		return
	case *kind.AdditionalProperties:
		var allowed []string
		if location != nil {
			allowed = slices.Sorted(maps.Keys(location.Properties))
		}
		add(DiagnosticUnknownProperty, path, allowed)
		return
	case *kind.Type:
		add(DiagnosticTypeMismatch, path, nil)
		return
	}
	if len(issue.Causes) == 0 {
		add(DiagnosticConstraint, path, nil)
		return
	}
	for _, cause := range issue.Causes {
		collectSchemaDiagnostics(root, cause, found)
	}
}
