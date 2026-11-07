package dashboards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/entitytypes"
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
		return Definition{}, invalidDefinition("a definition is at most 16 KiB")
	}
	c, err := codec()
	if err != nil {
		return Definition{}, err
	}
	d, normalized, err := c.Decode(raw)
	if err != nil {
		// Schema diagnostics can quote submitted values; the schema is published.
		return Definition{}, invalidDefinition("does not match the view schema")
	}
	if len(normalized) > MaxDefinitionBytes {
		return Definition{}, invalidDefinition("a definition is at most 16 KiB")
	}
	if err = validateGraph(d.Spec); err != nil {
		return Definition{}, err
	}
	if _, err = collectBindings(d.Spec); err != nil {
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

func validateGraph(raw json.RawMessage) error {
	var g viewGraph
	if err := json.Unmarshal(raw, &g); err != nil {
		return invalidDefinition("spec is not an element graph")
	}
	if len(g.Elements) < 1 || len(g.Elements) > 32 {
		return invalidDefinition("a view has 1 to 32 elements")
	}
	seen := map[string]bool{}
	var visit func(string, int) error
	visit = func(id string, depth int) error {
		node, ok := g.Elements[id]
		if !ok || seen[id] || depth > 8 {
			return invalidDefinition(
				"root and every child must name an existing element, each used once, at most 8 levels deep",
			)
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
		return invalidDefinition("every element must be reachable from root")
	}
	return nil
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
	var version int
	var catalog, title string
	if json.Unmarshal(obj["schema_version"], &version) != nil ||
		json.Unmarshal(obj["catalog_version"], &catalog) != nil ||
		json.Unmarshal(obj["title"], &title) != nil {
		return nil, fmt.Errorf("%w: invalid retained envelope types", ErrInvalidDefinition)
	}
	return append(json.RawMessage(nil), raw...), nil
}
