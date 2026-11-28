package agent

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

const composeViewToolName = "compose_view"
const legacyViewToolName = "present_view"
const maxComposeArguments = 32 * 1024
const composeModeField = "mode"
const composeTypeField = "type"

const composeViewDescription = "Present a live dashboard draft without saving or sending Commands. " +
	"For create, put mode, title, and layout at the top level. Use a nested layout of cards, Stacks, or Grids and bind exact Entity or Device IDs from discovery. " +
	"Use mode edit only with the current user-selected draft, to change its title, forecast periods, or a chart window. " +
	"Other draft edits belong in the dashboard editor. Pass native objects and arrays, not JSON strings. " +
	"Follow the keys for each card type. Omit unused optional fields; never send null. " +
	"For AirPurifier, entity_id is its main power Entity. Use fan_mode for named modes and fan_level for numbered speeds. " +
	"Use those exact keys, not power or mode. " +
	"Grid requires columns. A card bound to one Entity names it entity_id; TimeSeries lists its Entities in series, and ReadingList, StateTimeline, and ToggleList in rows. An Entity is always a bare ID string. " +
	"For a numeric question, TimeSeries shows change over time, Ranking compares current readings, WeekGrid shows the usual week, and TimeInRange shows time below, between, and above thresholds in the unit you see for that reading. " +
	"TimeSeries compare typical answers whether today is unusual, and PeriodTotals shows on-time of a yes/no field per hour or day. " +
	"Each card type's description says when to use it and how to bind it. " +
	"For a weather station, prefer OutsideNow, Rain, Wind, Pressure, and Forecast over separate Metrics. " +
	"A weather slot that takes a hearth.numericsensor/v1 Entity requires its discovered unit: mm/h for a rain rate, mm for rain totals, deg for wind direction. " +
	"Ask which source to use if a requested binding is ambiguous, including relative versus absolute pressure. " +
	"For quick switches, use a Grid of up to 4 columns of ToggleTiles or tile layouts. " +
	"A view has at most 32 nodes, 8 levels, and 16 distinct Entities and Devices."

//go:embed compose_view.schema.json
var composeViewSchema []byte

// ViewAuthoring is the dashboard capability the built-in Agent uses to create
// artifacts; neither operation changes a saved dashboard.
type ViewAuthoring interface {
	ComposeView(context.Context, dashboards.ComposeViewInput) (dashboards.Artifact, error)
	EditView(context.Context, dashboards.Definition, []dashboards.ViewChange) (dashboards.Artifact, error)
}

type composeViewContract struct {
	authoring ViewAuthoring
	info      *schema.ToolInfo
	input     *jsonschema.Schema
}

func newComposeViewContract(authoring ViewAuthoring) (*composeViewContract, error) {
	const schemaID = "https://hearth.local/agent/compose_view"
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(composeViewSchema))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err = compiler.AddResource(schemaID, document); err != nil {
		return nil, err
	}
	input, err := compiler.Compile(schemaID)
	if err != nil {
		return nil, err
	}
	var parameters einojsonschema.Schema
	if err = json.Unmarshal(composeViewSchema, &parameters); err != nil {
		return nil, err
	}
	return &composeViewContract{authoring: authoring, input: input, info: &schema.ToolInfo{
		Name:        composeViewToolName,
		Desc:        composeViewDescription,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&parameters),
	}}, nil
}

type composeViewTool struct {
	*composeViewContract

	draft *ViewContext
}

func (t composeViewTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t composeViewTool) InvokableRun(ctx context.Context, arguments string, _ ...tool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", toolCallError{err: err}
	}
	if len(arguments) > maxComposeArguments {
		return rejectComposeArguments(ctx, composeDiagnostic{Code: "argument_limit"})
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(arguments))
	if err != nil {
		return rejectComposeArguments(ctx, composeDiagnostic{Code: "invalid_json"})
	}
	object, _ := value.(map[string]any)
	if object != nil {
		// Bound recursion before schema evaluation, even on unexpected
		// children: incompatible union branches also inspect the input.
		var nodes int
		if path, tooLarge := composeLayoutLimit(object["layout"], "/layout", 1, &nodes); tooLarge {
			return rejectComposeArguments(ctx, composeDiagnostic{Code: "layout_limit", Path: path})
		}
	}
	if invalid := t.input.Validate(value); invalid != nil {
		validation, ok := errors.AsType[*jsonschema.ValidationError](invalid)
		if !ok {
			return "", toolCallError{err: invalid}
		}
		return rejectComposeArguments(ctx, diagnoseComposeArguments(t.input, value, validation)...)
	}
	// Decode exactly the instance the validator saw, not an object whose
	// repeated keys can merge into Go structs during unmarshaling.
	raw, err := json.Marshal(normalizeComposeIntegers(object))
	if err != nil {
		return "", toolCallError{err: err}
	}
	if object[composeModeField] == "edit" {
		return t.edit(ctx, raw)
	}
	return t.create(ctx, raw)
}

// maxExactInteger is the largest magnitude a float64 holds as an exact integer.
const maxExactInteger = 1 << 53

func normalizeComposeIntegers(value any) any {
	switch item := value.(type) {
	case json.Number:
		// Columns and periods are bounded whole numbers, including spellings
		// such as 8.0; decode those as Go ints. Any other number, such as a
		// TimeInRange threshold of 20.5, keeps its fractional value.
		if n, err := item.Float64(); err == nil {
			if n == math.Trunc(n) && math.Abs(n) < maxExactInteger {
				return int(n)
			}
			return n
		}
	case map[string]any:
		for key, child := range item {
			item[key] = normalizeComposeIntegers(child)
		}
	case []any:
		for i, child := range item {
			item[i] = normalizeComposeIntegers(child)
		}
	}
	return value
}

func (t composeViewTool) create(ctx context.Context, arguments []byte) (string, error) {
	if t.draft != nil {
		return composeFailure(ctx, composeViewFailure{
			Code:    "draft_selected",
			Message: "To create a separate view, send the request without the selected dashboard draft.",
		})
	}
	var input dashboards.ComposeViewInput
	if err := json.Unmarshal(arguments, &input); err != nil {
		return rejectComposeArguments(ctx, composeDiagnostic{Code: "invalid_layout", Path: "/layout"})
	}
	artifact, err := t.authoring.ComposeView(ctx, input)
	return composeResult(ctx, artifact, err)
}

func (t composeViewTool) edit(ctx context.Context, arguments []byte) (string, error) {
	if t.draft == nil {
		return composeFailure(ctx, composeViewFailure{
			Code: "draft_required", Message: "Select the dashboard draft and send the edit request again.",
		})
	}
	var input struct {
		Changes []dashboards.ViewChange `json:"changes"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return rejectComposeArguments(ctx, composeDiagnostic{Code: "invalid_changes", Path: "/changes"})
	}
	artifact, err := t.authoring.EditView(ctx, t.draft.Definition, input.Changes)
	return composeResult(ctx, artifact, err)
}

func composeResult(ctx context.Context, artifact dashboards.Artifact, err error) (string, error) {
	var value []byte
	switch {
	case err == nil:
		if contextErr := ctx.Err(); contextErr != nil {
			return "", toolCallError{err: contextErr}
		}
		value, err = json.Marshal(artifact)
	case errors.Is(err, dashboards.ErrInvalidDefinition):
		failure := composeViewFailure{Code: "invalid_definition", Message: "The requested view is not valid."}
		if rejected, ok := errors.AsType[dashboards.DefinitionError](err); ok {
			failure.Message = rejected.Reason
		}
		if argument, ok := errors.AsType[dashboards.ViewArgumentError](err); ok {
			failure.Diagnostics = []composeDiagnostic{{Code: "invalid_value", Path: argument.Path}}
		}
		return composeFailure(ctx, failure)
	}
	if err != nil {
		return "", toolCallError{err: err}
	}
	return string(value), nil
}

type composeDiagnostic struct {
	Code string `json:"code"`
	Path string `json:"path"`
	// Allowed lists schema-declared property names or variant tags at Path
	// for unknown_property and invalid_variant. It never contains input.
	Allowed []string `json:"allowed,omitempty"`
}

// maxComposeDiagnostics bounds one rejection's diagnostics.
const maxComposeDiagnostics = 8

type composeViewFailure struct {
	Code        string              `json:"failure_code"`
	Message     string              `json:"message"`
	Diagnostics []composeDiagnostic `json:"diagnostics,omitempty"`
}

func rejectComposeArguments(ctx context.Context, diagnostics ...composeDiagnostic) (string, error) {
	return composeFailure(ctx, composeViewFailure{
		Code: "invalid_arguments", Message: "Invalid compose_view arguments.", Diagnostics: diagnostics,
	})
}

func composeFailure(ctx context.Context, failure composeViewFailure) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", toolCallError{err: err}
	}
	output, err := json.Marshal(failure)
	if err != nil {
		return "", toolCallError{err: err}
	}
	if status, ok := ctx.Value(toolResultStatusKey{}).(*toolResultStatus); ok {
		status.modeledFailure = true
	}
	return string(output), nil
}

func composeLayoutLimit(value any, path string, depth int, count *int) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	(*count)++
	if depth > dashboards.MaxViewDepth || *count > dashboards.MaxViewNodes {
		return path, true
	}
	children, _ := object["children"].([]any)
	for i, node := range children {
		if childPath, over := composeLayoutLimit(node, fmt.Sprintf("%s/children/%d", path, i), depth+1, count); over {
			return childPath, true
		}
	}
	return "", false
}

// diagnoseComposeArguments returns up to maxComposeDiagnostics leaf problems,
// sorted by path then code, with duplicates removed. A declared discriminator
// (type or op) selects one variant's causes; other branches contribute all
// their causes. Unknown keys and instance values never form diagnostics; an
// unknown key or tag instead lists what the schema allows at that path.
func diagnoseComposeArguments(
	root *jsonschema.Schema, value any, issue *jsonschema.ValidationError,
) []composeDiagnostic {
	var found []composeDiagnostic
	collectComposeDiagnostics(root, value, issue, &found)
	if len(found) == 0 {
		return []composeDiagnostic{{Code: "constraint_violation"}}
	}
	slices.SortStableFunc(found, func(a, b composeDiagnostic) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.Code, b.Code))
	})
	found = slices.CompactFunc(found, func(a, b composeDiagnostic) bool {
		return a.Path == b.Path && a.Code == b.Code
	})
	return found[:min(len(found), maxComposeDiagnostics)]
}

func collectComposeDiagnostics(
	root *jsonschema.Schema, value any, issue *jsonschema.ValidationError, found *[]composeDiagnostic,
) {
	path, location, instance := composeSchemaLocation(root, value, issue.InstanceLocation)
	add := func(diagnostic composeDiagnostic) { *found = append(*found, diagnostic) }
	switch problem := issue.ErrorKind.(type) {
	case *kind.OneOf:
		if _, object := instance.(map[string]any); !object {
			add(composeDiagnostic{Code: "type_mismatch", Path: path})
			return
		}
		index, discriminator := composeVariant(location, instance)
		if index < 0 || index >= len(issue.Causes) {
			add(composeDiagnostic{
				Code:    "invalid_variant",
				Path:    path + discriminator,
				Allowed: composeVariantTags(location, discriminator),
			})
			return
		}
		collectComposeDiagnostics(root, value, issue.Causes[index], found)
		return
	case *kind.Required:
		location = composeSelectedVariant(location, instance)
		for _, missing := range problem.Missing {
			missingPath := path
			if location != nil && location.Properties[missing] != nil {
				missingPath += "/" + missing
			}
			add(composeDiagnostic{Code: "missing_property", Path: missingPath})
		}
		return
	case *kind.AdditionalProperties:
		location = composeSelectedVariant(location, instance)
		var allowed []string
		if location != nil {
			allowed = slices.Sorted(maps.Keys(location.Properties))
		}
		add(composeDiagnostic{Code: "unknown_property", Path: path, Allowed: allowed})
		return
	case *kind.Type:
		add(composeDiagnostic{Code: "type_mismatch", Path: path})
		return
	}
	if len(issue.Causes) == 0 {
		add(composeDiagnostic{Code: "constraint_violation", Path: path})
		return
	}
	for _, cause := range issue.Causes {
		collectComposeDiagnostics(root, value, cause, found)
	}
}

// composeSelectedVariant narrows a union to the variant its discriminator names.
func composeSelectedVariant(location *jsonschema.Schema, instance any) *jsonschema.Schema {
	if index, _ := composeVariant(location, instance); index >= 0 {
		return location.OneOf[index]
	}
	return location
}

// composeVariantTags lists a union's declared discriminator values.
func composeVariantTags(location *jsonschema.Schema, discriminator string) []string {
	key := strings.TrimPrefix(discriminator, "/")
	if location == nil || key == "" {
		return nil
	}
	tags := make([]string, 0, len(location.OneOf))
	for _, variant := range location.OneOf {
		if tag := variant.Properties[key]; tag != nil && tag.Const != nil {
			tags = append(tags, fmt.Sprint(*tag.Const))
		}
	}
	return tags
}

func composeSchemaLocation(root *jsonschema.Schema, value any, tokens []string) (string, *jsonschema.Schema, any) {
	path := ""
	for _, token := range tokens {
		root = composeSchemaTarget(root)
		if index, _ := composeVariant(root, value); index >= 0 {
			root = root.OneOf[index]
		}
		var child *jsonschema.Schema
		switch instance := value.(type) {
		case map[string]any:
			child, value = root.Properties[token], instance[token]
		case []any:
			index, err := strconv.Atoi(token)
			if err == nil && index >= 0 && index < len(instance) {
				child, value = root.Items2020, instance[index]
			}
		}
		if child == nil {
			return path, nil, nil
		}
		path += "/" + token
		root = child
	}
	return path, composeSchemaTarget(root), value
}

func composeSchemaTarget(s *jsonschema.Schema) *jsonschema.Schema {
	for s.Ref != nil {
		s = s.Ref
	}
	return s
}

func composeVariant(s *jsonschema.Schema, instance any) (int, string) {
	object, ok := instance.(map[string]any)
	if s == nil || !ok || len(s.OneOf) == 0 {
		return -1, ""
	}
	for _, key := range []string{composeTypeField, "op"} {
		if s.OneOf[0].Properties[key] == nil {
			continue
		}
		value, _ := object[key].(string)
		for i, variant := range s.OneOf {
			tag := variant.Properties[key]
			if tag != nil && tag.Const != nil && *tag.Const == value {
				return i, "/" + key
			}
		}
		return -1, "/" + key
	}
	return -1, ""
}
