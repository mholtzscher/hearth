package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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
	"Grid requires columns. OutsideNow uses wind_direction; Wind uses direction. " +
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
		return rejectComposeArguments(ctx, "argument_limit", "")
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(arguments))
	if err != nil {
		return rejectComposeArguments(ctx, "invalid_json", "")
	}
	object, _ := value.(map[string]any)
	if object != nil {
		// Bound recursion before schema evaluation, even on unexpected
		// children: incompatible union branches also inspect the input.
		var nodes int
		if path, tooLarge := composeLayoutLimit(object["layout"], "/layout", 1, &nodes); tooLarge {
			return rejectComposeArguments(ctx, "layout_limit", path)
		}
	}
	if invalid := t.input.Validate(value); invalid != nil {
		validation, ok := errors.AsType[*jsonschema.ValidationError](invalid)
		if !ok {
			return "", toolCallError{err: invalid}
		}
		diagnostic := diagnoseComposeArguments(t.input, value, validation)
		return rejectComposeArguments(ctx, diagnostic.Code, diagnostic.Path)
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

func normalizeComposeIntegers(value any) any {
	switch item := value.(type) {
	case json.Number:
		// The closed schema admits only bounded whole numbers (columns and
		// periods), including spellings such as 8.0. Decode these as Go ints.
		if n, err := item.Float64(); err == nil {
			return int(n)
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
		return rejectComposeArguments(ctx, "invalid_layout", "/layout")
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
		return rejectComposeArguments(ctx, "invalid_changes", "/changes")
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
}

type composeViewFailure struct {
	Code        string              `json:"failure_code"`
	Message     string              `json:"message"`
	Diagnostics []composeDiagnostic `json:"diagnostics,omitempty"`
}

func rejectComposeArguments(ctx context.Context, code, path string) (string, error) {
	return composeFailure(ctx, composeViewFailure{
		Code: "invalid_arguments", Message: "Invalid compose_view arguments.",
		Diagnostics: []composeDiagnostic{{Code: code, Path: path}},
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

// diagnoseComposeArguments selects the variant named by a declared
// discriminator. Unknown keys and instance values never form diagnostics.
func diagnoseComposeArguments(root *jsonschema.Schema, value any, issue *jsonschema.ValidationError) composeDiagnostic {
	for {
		path, location, instance := composeSchemaLocation(root, value, issue.InstanceLocation)
		switch problem := issue.ErrorKind.(type) {
		case *kind.OneOf:
			if _, object := instance.(map[string]any); !object {
				return composeDiagnostic{Code: "type_mismatch", Path: path}
			}
			index, discriminator := composeVariant(location, instance)
			if index < 0 || index >= len(issue.Causes) {
				return composeDiagnostic{Code: "invalid_variant", Path: path + discriminator}
			}
			issue = issue.Causes[index]
			continue
		case *kind.Required:
			if index, _ := composeVariant(location, instance); index >= 0 {
				location = location.OneOf[index]
			}
			if location != nil && len(problem.Missing) > 0 && location.Properties[problem.Missing[0]] != nil {
				path += "/" + problem.Missing[0]
			}
			return composeDiagnostic{Code: "missing_property", Path: path}
		case *kind.AdditionalProperties:
			return composeDiagnostic{Code: "unknown_property", Path: path}
		case *kind.Type:
			return composeDiagnostic{Code: "type_mismatch", Path: path}
		}
		if len(issue.Causes) == 0 {
			return composeDiagnostic{Code: "constraint_violation", Path: path}
		}
		issue = issue.Causes[0]
	}
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
