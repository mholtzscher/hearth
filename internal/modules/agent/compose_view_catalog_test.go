package agent //nolint:testpackage // Exercises the private authoring contract with the real presenter.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type portableViewFixture struct {
	Title string `json:"title"`
	Spec  struct {
		Root     string                  `json:"root"`
		Elements map[string]portableNode `json:"elements"`
	} `json:"spec"`
}

type portableNode struct {
	Type     string         `json:"type"`
	Props    map[string]any `json:"props"`
	Children []string       `json:"children,omitempty"`
}

type portableNodeShape struct {
	Properties struct {
		Type struct {
			Const string `json:"const"`
		} `json:"type"`
		Props portablePropShape `json:"props"`
	} `json:"properties"`
}

type portablePropShape struct {
	Required   []string            `json:"required"`
	Properties map[string]any      `json:"properties"`
	AnyOf      []portablePropShape `json:"anyOf"`
}

func portableNodeShapes(t *testing.T) map[string]portablePropShape {
	t.Helper()
	var canonical struct {
		Properties struct {
			Spec struct {
				Properties struct {
					Elements struct {
						AdditionalProperties struct {
							OneOf []portableNodeShape `json:"oneOf"`
						} `json:"additionalProperties"`
					} `json:"elements"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(viewsv1.Schema, &canonical))
	result := make(map[string]portablePropShape)
	for _, variant := range canonical.Properties.Spec.Properties.Elements.AdditionalProperties.OneOf {
		result[variant.Properties.Type.Const] = variant.Properties.Props
	}
	require.NotEmpty(t, result)
	return result
}

func TestComposeViewCatalogParity(t *testing.T) {
	t.Parallel()
	shapes := portableNodeShapes(t)
	var published struct {
		Defs struct {
			Node struct {
				OneOf []portableNodeShape `json:"oneOf"`
			} `json:"node"`
		} `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(composeViewSchema, &published))
	var authoringKinds []string
	for _, variant := range published.Defs.Node.OneOf {
		authoringKinds = append(authoringKinds, variant.Properties.Type.Const)
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(shapes)), authoringKinds)

	var examples []struct {
		Name       string          `json:"name"`
		Valid      bool            `json:"valid"`
		Definition json.RawMessage `json:"definition"`
	}
	require.NoError(t, json.Unmarshal(viewsv1.Examples, &examples))
	nodes := map[string]portableNode{}
	for _, example := range examples {
		if !example.Valid {
			continue
		}
		var definition portableViewFixture
		require.NoError(t, json.Unmarshal(example.Definition, &definition))
		t.Run(example.Name, func(t *testing.T) {
			t.Parallel()
			assertComposeMatchesPortable(t, definition)
		})
		for _, node := range definition.Spec.Elements {
			if previous := nodes[node.Type]; len(node.Props) > len(previous.Props) {
				nodes[node.Type] = node
			}
		}
	}
	nodes["Heading"] = portableNode{Type: "Heading", Props: map[string]any{"text": "Weather and controls"}}

	// Independently authored portable examples are the option oracle. Where a
	// shared example omits a label, add a label case; also exercise required-only
	// leaf inputs, including default periods and confirmation behavior.
	for _, name := range slices.Sorted(maps.Keys(shapes)) {
		shape := shapes[name]
		node, found := nodes[name]
		require.True(t, found, "portable kind %q needs an authoring fixture", name)
		if len(node.Children) != 0 {
			continue // Cover containers in shared fixtures and TestComposeViewNestedLayoutsPreserveOrderAndOptions.
		}
		if len(shape.AnyOf) > 0 {
			shape = shape.AnyOf[0] // Forecast arms share the same keys.
		}
		full := maps.Clone(node.Props)
		if _, hasLabel := shape.Properties["label"]; hasLabel {
			full["label"] = "Custom label"
		}
		for key := range shape.Properties {
			require.Contains(t, full, key, "%q needs a fixture for property %q", name, key)
		}
		minimal := map[string]any{}
		for _, key := range shape.Required {
			minimal[key] = full[key]
		}
		t.Run(name+" minimal", func(t *testing.T) {
			t.Parallel()
			assertComposeMatchesPortable(t, portableFixture(portableNode{Type: name, Props: minimal}))
		})
		t.Run(name+" with options", func(t *testing.T) {
			t.Parallel()
			assertComposeMatchesPortable(t, portableFixture(portableNode{Type: name, Props: full}))
		})
	}
}

func portableFixture(node portableNode) portableViewFixture {
	view := portableViewFixture{Title: "Portable example"}
	view.Spec.Root = "original-element-key"
	view.Spec.Elements = map[string]portableNode{view.Spec.Root: node}
	return view
}

func assertComposeMatchesPortable(t *testing.T, want portableViewFixture) {
	t.Helper()
	expected := portableTree(want, want.Spec.Root)
	input, err := json.Marshal(map[string]any{
		"mode": "create", "title": want.Title, "layout": authoringTree(expected),
	})
	require.NoError(t, err)
	inventory := catalogInventory(expected)
	contract, err := newComposeViewContract(dashboards.NewService(nil, inventory, inventory))
	require.NoError(t, err)
	result, err := (composeViewTool{composeViewContract: contract}).InvokableRun(t.Context(), string(input))
	require.NoError(t, err)
	artifact, err := dashboards.ParseArtifact([]byte(result))
	require.NoError(t, err, "tool result: %s", result)
	var got portableViewFixture
	require.NoError(t, json.Unmarshal(artifact.Definition, &got))
	require.Equal(t, want.Title, got.Title)
	require.Equal(t, expected, portableTree(got, got.Spec.Root))
}

// The comparison ignores only generated keys and an explicit empty root field
// versus its omitted spelling. It retains all labels, units' source IDs, card
// props, and ordered layout children.
type portableTreeNode struct {
	Type     string
	Props    map[string]any
	Children []portableTreeNode
}

func portableTree(view portableViewFixture, key string) portableTreeNode {
	node := view.Spec.Elements[key]
	tree := portableTreeNode{Type: node.Type, Props: withoutEmptyFields(node.Props).(map[string]any)}
	for _, child := range node.Children {
		tree.Children = append(tree.Children, portableTree(view, child))
	}
	return tree
}

func withoutEmptyFields(value any) any {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, member := range item {
			if key != "field" || member != "" {
				result[key] = withoutEmptyFields(member)
			}
		}
		return result
	case []any:
		result := make([]any, 0, len(item))
		for _, member := range item {
			result = append(result, withoutEmptyFields(member))
		}
		return result
	default:
		return value
	}
}

// Translate the independent portable fixtures in the opposite direction from
// the production compiler. Leave every ordinary option verbatim rather than
// inventing expected compiler output in the tests.
func authoringTree(node portableTreeNode) map[string]any {
	args := withoutPortableSourceTypes(node.Props).(map[string]any)
	args["type"] = node.Type
	if len(node.Children) > 0 {
		children := make([]map[string]any, 0, len(node.Children))
		for _, child := range node.Children {
			children = append(children, authoringTree(child))
		}
		args["children"] = children
	}
	switch node.Type {
	case "Device":
		args["device_id"] = args["device"].(map[string]any)["device_id"]
		delete(args, "device")
	case "ToggleTile", "Pressure", "Forecast", "Cover", "Thermostat", "MediaPlayer":
		flattenSource(args)
	case "ToggleList":
		for _, row := range args["rows"].([]any) {
			flattenSource(row.(map[string]any))
		}
	case "TimeSeries":
		for _, row := range args["series"].([]any) {
			entry := row.(map[string]any)
			source := entry["source"].(map[string]any)
			args["window"] = source["window"]
			if field, ok := source["field"]; ok {
				args["field"] = field
			}
			flattenSource(entry)
		}
	case "OutsideNow", "Rain", "Wind", "AirPurifier":
		flattenRoles(args)
		if totals, ok := args["totals"].(map[string]any); ok {
			flattenRoles(totals)
		}
	}
	return args
}

func flattenSource(node map[string]any) {
	node["entity_id"] = node["source"].(map[string]any)["entity_id"]
	delete(node, "source")
}

func flattenRoles(props map[string]any) {
	for key, value := range props {
		if object, ok := value.(map[string]any); ok {
			if id, bound := object["entity_id"]; bound {
				props[key] = id
			}
		}
	}
}

func withoutPortableSourceTypes(value any) any {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, member := range item {
			if key != "kind" && key != "entity_type" {
				result[key] = withoutPortableSourceTypes(member)
			}
		}
		return result
	case []any:
		result := make([]any, 0, len(item))
		for _, member := range item {
			result = append(result, withoutPortableSourceTypes(member))
		}
		return result
	default:
		return value
	}
}

type composeInventory map[devices.EntityID]devices.Entity

func (r composeInventory) GetEntity(_ context.Context, id devices.EntityID) (devices.EntityWithState, error) {
	entity, ok := r[id]
	if !ok {
		return devices.EntityWithState{}, devices.ErrEntityNotFound
	}
	return devices.EntityWithState{Entity: entity}, nil
}

func (composeInventory) GetDevice(_ context.Context, req devices.GetDeviceParams) (devices.DeviceAggregate, error) {
	if req.ID != "dev_01900000-0000-7000-8000-000000000001" {
		return devices.DeviceAggregate{}, devices.ErrDeviceNotFound
	}
	return devices.DeviceAggregate{Device: devices.Device{ID: req.ID}}, nil
}

func catalogInventory(tree portableTreeNode) composeInventory {
	reader := make(composeInventory)
	var visitProps func(any)
	visitProps = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			if rawID, ok := item["entity_id"].(string); ok {
				id := devices.EntityID(rawID)
				typ := devices.EntityTypeID(item["entity_type"].(string))
				reader[id] = devices.Entity{
					ID: id, TypeID: typ, DeviceID: "dev_01900000-0000-7000-8000-000000000001",
					Support: catalogSupport(id, typ),
				}
			}
			for _, child := range item {
				visitProps(child)
			}
		case []any:
			for _, child := range item {
				visitProps(child)
			}
		}
	}
	var visitNodes func(portableTreeNode)
	visitNodes = func(node portableTreeNode) {
		visitProps(node.Props)
		for _, child := range node.Children {
			visitNodes(child)
		}
	}
	visitNodes(tree)
	return reader
}

func catalogSupport(id devices.EntityID, typ devices.EntityTypeID) devices.EntitySupport {
	suffix := strings.TrimPrefix(string(id), "ent_01900000-0000-7000-8000-000000000")
	// Only types whose fixtures need nonempty Support are special-cased here.
	//nolint:exhaustive // Other types use the empty Support fixture.
	switch typ {
	case devices.EntityTypeNumericsensorV1:
		unit := "%RH"
		switch suffix {
		case "104":
			unit = "deg"
		case "105":
			unit = "mm/h"
		case "106", "107", "108", "109", "110":
			unit = "mm"
		case "302":
			unit = "µg/m³"
		case "303":
			unit = "%"
		}
		return devices.EntitySupport(
			fmt.Sprintf(`{"state":{"minimum":0,"maximum":10000,"unit":%q},"operations":{}}`, unit),
		)
	case devices.EntityTypeEnumsettingV1:
		if suffix == "305" {
			return devices.EntitySupport(`{"state":{"choices":["1","2","3","4"]},"operations":{"set":{}}}`)
		}
		return devices.EntitySupport(`{"state":{"choices":["manual","auto","sleep"]},"operations":{"set":{}}}`)
	case devices.EntityTypeColortempV1:
		return devices.EntitySupport(`{"state":{"minimum":153,"maximum":500},"operations":{"set":{"step":1}}}`)
	case devices.EntityTypeNumericsettingV1:
		return devices.EntitySupport(`{"state":{"minimum":0,"maximum":100,"choices":[]},"operations":{"set":{}}}`)
	default:
		return devices.EntitySupport(`{"state":{},"operations":{}}`)
	}
}
