package dashboards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/entitytypes"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const MaxDefinitionBytes = 16 * 1024

//nolint:gochecknoglobals // The embedded immutable schema is compiled once.
var codecOnce sync.Once

//nolint:gochecknoglobals // The compiled schema is immutable and concurrency-safe.
var viewCodec *entitytypes.JSONCodec[Definition]

var errCodec error

func codec() (*entitytypes.JSONCodec[Definition], error) {
	codecOnce.Do(func() {
		viewCodec, errCodec = entitytypes.CompileJSONCodec[Definition](
			"https://hearth.local/views/v1",
			viewsv1.Schema,
			nil,
		)
	})
	return viewCodec, errCodec
}

// ParseDefinition strictly validates the portable contract and graph.
func ParseDefinition(raw json.RawMessage) (Definition, error) {
	if len(raw) > MaxDefinitionBytes {
		return Definition{}, fmt.Errorf("%w: definition too large", ErrInvalidDefinition)
	}
	c, err := codec()
	if err != nil {
		return Definition{}, err
	}
	d, normalized, err := c.Decode(raw)
	if err != nil {
		return Definition{}, fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	if len(normalized) > MaxDefinitionBytes {
		return Definition{}, fmt.Errorf("%w: definition too large", ErrInvalidDefinition)
	}
	if err = validateGraph(d.Spec); err != nil {
		return Definition{}, err
	}
	return d, nil
}

func ValidateDefinition(d Definition) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	_, err = ParseDefinition(raw)
	return err
}

type viewGraph struct {
	Root     string              `json:"root"`
	Elements map[string]viewNode `json:"elements"`
}
type viewNode struct {
	Type     string          `json:"type"`
	Props    json.RawMessage `json:"props"`
	Children []string        `json:"children"`
}
type binding struct {
	EntityID   string `json:"entity_id"`
	EntityType string `json:"entity_type"`
	Window     string `json:"window"`
}

//nolint:gocognit // Graph traversal and binding bounds are one admission decision.
func validateGraph(raw json.RawMessage) error {
	var g viewGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		return fmt.Errorf("%w: graph", ErrInvalidDefinition)
	}
	if len(g.Elements) < 1 || len(g.Elements) > 32 {
		return fmt.Errorf("%w: element count", ErrInvalidDefinition)
	}
	seen := map[string]bool{}
	var visit func(string, int) error
	visit = func(id string, depth int) error {
		node, ok := g.Elements[id]
		if !ok || seen[id] || depth > 8 {
			return fmt.Errorf("%w: invalid graph", ErrInvalidDefinition)
		}
		seen[id] = true
		for _, child := range node.Children {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(g.Root, 1); err != nil {
		return err
	}
	if len(seen) != len(g.Elements) {
		return fmt.Errorf("%w: unreachable element", ErrInvalidDefinition)
	}
	refs := map[string]bool{}
	for _, node := range g.Elements {
		bindings, err := nodeBindings(node)
		if err != nil {
			return err
		}
		chart := map[string]bool{}
		window := ""
		for _, b := range bindings {
			if _, idErr := devices.ParseEntityID(b.EntityID); idErr != nil {
				return fmt.Errorf("%w: invalid entity id", ErrInvalidDefinition)
			}
			refs[b.EntityID] = true
			if node.Type == "TimeSeries" {
				if chart[b.EntityID] || (window != "" && window != b.Window) {
					return fmt.Errorf("%w: incompatible series", ErrInvalidDefinition)
				}
				chart[b.EntityID] = true
				window = b.Window
			}
		}
	}
	const maxEntities = 16
	if len(refs) > maxEntities {
		return fmt.Errorf("%w: too many entities", ErrInvalidDefinition)
	}
	return nil
}

func nodeBindings(node viewNode) ([]binding, error) {
	switch node.Type {
	case "Metric":
		var p struct {
			Source binding `json:"source"`
		}
		if err := json.Unmarshal(node.Props, &p); err != nil {
			return nil, err
		}
		return []binding{p.Source}, nil
	case "TimeSeries":
		var p struct {
			Series []struct {
				Source binding `json:"source"`
			} `json:"series"`
		}
		if err := json.Unmarshal(node.Props, &p); err != nil {
			return nil, err
		}
		result := make([]binding, 0, len(p.Series))
		for _, s := range p.Series {
			result = append(result, s.Source)
		}
		return result, nil
	default:
		return nil, nil
	}
}

// ReadDefinition preserves unsupported versions and invalid graphs for recovery.
// It accepts only a bounded JSON object with the known envelope fields.
func ReadDefinition(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxDefinitionBytes || !json.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid retained definition", ErrInvalidDefinition)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("%w: invalid retained envelope", ErrInvalidDefinition)
	}
	for _, k := range []string{"schema_version", "catalog_version", "title", "spec"} {
		if len(obj[k]) == 0 || bytes.Equal(obj[k], []byte("null")) {
			return nil, fmt.Errorf("%w: invalid retained envelope", ErrInvalidDefinition)
		}
	}
	return append(json.RawMessage(nil), raw...), nil
}
