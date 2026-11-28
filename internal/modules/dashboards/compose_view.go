package dashboards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// ComposeViewInput describes one new view. It does not carry portable graph keys
// or binding types; Hearth assigns them from the tree and the selected sources.
type ComposeViewInput struct {
	Title  string      `json:"title"`
	Layout ComposeNode `json:"layout"`
}

// ComposeNode is a layout container or a card. Its JSON discriminator selects
// one concrete props type before the compiler sees it.
type ComposeNode struct {
	kind  string
	props composeProps
}

type composeProps interface {
	compile(*cardCompiler) cardProps
}

func (n *ComposeNode) UnmarshalJSON(raw []byte) error {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return err
	}
	props := newComposeProps(header.Type)
	if props == nil {
		return invalidDefinition("unknown layout or card type")
	}
	if err := json.Unmarshal(raw, props); err != nil {
		return err
	}
	*n = ComposeNode{kind: header.Type, props: props}
	return nil
}

type stackProps struct {
	Gap      string        `json:"gap"`
	Children []ComposeNode `json:"children"`
}

func (p *stackProps) compile(_ *cardCompiler) cardProps {
	gap := p.Gap
	if gap == "" {
		gap = "medium"
	}
	return cardProps{"gap": gap}
}

type gridProps struct {
	Columns  int           `json:"columns"`
	Children []ComposeNode `json:"children"`
}

func (p *gridProps) compile(_ *cardCompiler) cardProps {
	return cardProps{"columns": p.Columns}
}

// ViewArgumentError identifies a known authoring field without echoing a
// submitted key, label, or ID. Its cause retains the service error class.
type ViewArgumentError struct {
	Path string
	Err  error
}

func (e ViewArgumentError) Error() string { return e.Err.Error() }
func (e ViewArgumentError) Unwrap() error { return e.Err }

func invalidViewArgument(path, reason string) error {
	return ViewArgumentError{Path: path, Err: invalidDefinition(reason)}
}

func (s *Service) ComposeView(ctx context.Context, input ComposeViewInput) (Artifact, error) {
	compiler := viewCompiler{
		ctx: ctx, reader: s.entities, units: s.units,
		nodes: make(map[string]viewNode), entityTypes: make(map[devices.EntityID]devices.EntityTypeID),
		supports:  make(map[devices.EntityID]json.RawMessage),
		resources: make(map[string]struct{}),
	}
	root, err := compiler.add(input.Layout, "/layout", 1)
	if err != nil {
		return Artifact{}, err
	}
	spec, err := json.Marshal(viewGraph{Root: root, Elements: compiler.nodes})
	if err != nil {
		return Artifact{}, err
	}
	return s.PresentView(ctx, Definition{
		SchemaVersion: 1, CatalogVersion: "hearth.dashboard/v1", Title: input.Title, Spec: spec,
	})
}

type viewCompiler struct {
	ctx         context.Context
	reader      EntityReader
	units       DisplayUnits
	supports    map[devices.EntityID]json.RawMessage
	nodes       map[string]viewNode
	entityTypes map[devices.EntityID]devices.EntityTypeID
	resources   map[string]struct{}
}

func (c *viewCompiler) add(n ComposeNode, path string, depth int) (string, error) {
	if depth > MaxViewDepth || len(c.nodes) >= MaxViewNodes {
		return "", invalidViewArgument(path, "a view has at most 32 elements and 8 levels")
	}
	if n.props == nil {
		return "", invalidViewArgument(path, "a layout or card is required")
	}
	key := "node-" + strconv.Itoa(len(c.nodes)+1)
	c.nodes[key] = viewNode{} // Reserve the key before visiting children.
	card := cardCompiler{view: c, path: path}
	props := n.props.compile(&card)
	if card.err != nil {
		return "", card.err
	}
	raw, err := json.Marshal(props)
	if err != nil {
		return "", err
	}
	childNodes, container := layoutChildren(n.props)
	var children []string
	if container {
		if len(childNodes) < 1 || len(childNodes) > 16 {
			return "", invalidViewArgument(path+"/children", "a layout has 1 to 16 children")
		}
		children = make([]string, 0, len(childNodes))
		for i, child := range childNodes {
			childKey, childErr := c.add(child, fmt.Sprintf("%s/children/%d", path, i), depth+1)
			if childErr != nil {
				return "", childErr
			}
			children = append(children, childKey)
		}
	}
	c.nodes[key] = viewNode{Type: n.kind, Props: raw, Children: children}
	return key, nil
}

func layoutChildren(p composeProps) ([]ComposeNode, bool) {
	switch p := p.(type) {
	case *stackProps:
		return p.Children, true
	case *gridProps:
		return p.Children, true
	default:
		return nil, false
	}
}

func (c *viewCompiler) recordResource(id, path string) error {
	c.resources[id] = struct{}{}
	if len(c.resources) > maxViewResources {
		return invalidViewArgument(path, "a view references at most 16 distinct Entities and Devices")
	}
	return nil
}

func (c *viewCompiler) entityType(id devices.EntityID, path string) (devices.EntityTypeID, error) {
	if known, ok := c.entityTypes[id]; ok {
		return known, nil
	}
	if _, err := devices.ParseEntityID(string(id)); err != nil {
		return "", invalidViewArgument(path, "entity_id must be a canonical Entity ID")
	}
	if err := c.recordResource(string(id), path); err != nil {
		return "", err
	}
	if c.reader == nil {
		return "", errors.New("entity reader unavailable")
	}
	entity, err := c.reader.GetEntity(c.ctx, id)
	if errors.Is(err, devices.ErrEntityNotFound) {
		return "", invalidViewArgument(path, "an entity_id names no Entity")
	}
	if err != nil {
		return "", err
	}
	c.entityTypes[id] = entity.Entity.TypeID
	c.supports[id] = json.RawMessage(entity.Entity.Support)
	return entity.Entity.TypeID, nil
}
