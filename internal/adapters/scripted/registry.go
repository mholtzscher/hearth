// Package scripted implements a config-driven simulator runtime: an agent
// declares Devices and Entities in YAML, and the runtime registers them,
// publishes scripted Observations and Entity Events on intervals, and answers
// Commands according to configured behavior. It speaks to Core only through
// the SDK Session and validates every configured value against the
// authoritative per-type JSON Schemas.
package scripted

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/mholtzscher/hearth/entitytypes"
	contractbinarysensorv1 "github.com/mholtzscher/hearth/entitytypes/binarysensorv1"
	contractbrightnessv1 "github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	contractcolorhsv1 "github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	contractcolormodev1 "github.com/mholtzscher/hearth/entitytypes/colormodev1"
	contractcolortempv1 "github.com/mholtzscher/hearth/entitytypes/colortempv1"
	contractcolorxyv1 "github.com/mholtzscher/hearth/entitytypes/colorxyv1"
	contractenumactionv1 "github.com/mholtzscher/hearth/entitytypes/enumactionv1"
	contractenumeventv1 "github.com/mholtzscher/hearth/entitytypes/enumeventv1"
	contractenumsettingv1 "github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
	contractnumericsensorv1 "github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	contractpressurev1 "github.com/mholtzscher/hearth/entitytypes/pressurev1"
	contractrelativehumidityv1 "github.com/mholtzscher/hearth/entitytypes/relativehumidityv1"
	contractspeedv1 "github.com/mholtzscher/hearth/entitytypes/speedv1"
	contracttemperaturev1 "github.com/mholtzscher/hearth/entitytypes/temperaturev1"
)

// TypeCodecs carries the schema-backed codecs for one built-in Entity type,
// operating on raw JSON so the runtime needs no handwritten per-type behavior.
// Cross-field rules beyond the schemas stay enforced by Core on publication.
type TypeCodecs struct {
	TypeID      string
	EventSource bool
	State       *entitytypes.JSONCodec[json.RawMessage]
	Support     *entitytypes.JSONCodec[json.RawMessage]
}

// NormalizeSupport validates support JSON and returns its normalized form.
func (codecs *TypeCodecs) NormalizeSupport(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := codecs.Support.Decode(raw)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// NormalizeState validates state JSON and returns its normalized form.
func (codecs *TypeCodecs) NormalizeState(raw json.RawMessage) (json.RawMessage, error) {
	_, normalized, err := codecs.State.Decode(raw)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

// schemaSource wires one generated Entity-type package into the registry. It
// references generated schema IDs, file maps, and file readers only, so adding
// a type is one table row with no behavior to hand-write. Moving this table
// into entitytypegen output is tracked future work.
type schemaSource struct {
	typeID      string
	eventSource bool
	stateID     string
	supportID   string
	files       map[string]string
	readFile    func(string) ([]byte, error)
}

var (
	//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative Entity-type codecs.
	registryOnce sync.Once
	//nolint:gochecknoglobals // Lazy, concurrency-safe cache of authoritative Entity-type codecs.
	registry    map[string]*TypeCodecs
	errRegistry error
)

// KnownTypes returns the sorted built-in Entity type IDs the harness supports.
func KnownTypes() []string {
	byID, err := loadRegistry()
	if err != nil {
		return nil
	}
	types := make([]string, 0, len(byID))
	for id := range byID {
		types = append(types, id)
	}
	sort.Strings(types)
	return types
}

// Lookup returns the codecs for a built-in Entity type ID, or an error naming
// the unknown type and the supported set.
func Lookup(typeID string) (*TypeCodecs, error) {
	byID, err := loadRegistry()
	if err != nil {
		return nil, err
	}
	codecs, ok := byID[typeID]
	if !ok {
		return nil, fmt.Errorf("unknown Entity type %q (supported: %v)", typeID, KnownTypes())
	}
	return codecs, nil
}

func loadRegistry() (map[string]*TypeCodecs, error) {
	registryOnce.Do(func() {
		registry, errRegistry = buildRegistry()
	})
	return registry, errRegistry
}

func buildRegistry() (map[string]*TypeCodecs, error) {
	sources := []schemaSource{
		{contractbinarysensorv1.TypeID, false,
			contractbinarysensorv1.StateSchemaID, contractbinarysensorv1.SupportSchemaID,
			contractbinarysensorv1.SchemaFiles(), contractbinarysensorv1.FS.ReadFile},
		{contractbrightnessv1.TypeID, false,
			contractbrightnessv1.StateSchemaID, contractbrightnessv1.SupportSchemaID,
			contractbrightnessv1.SchemaFiles(), contractbrightnessv1.FS.ReadFile},
		{contractcolorhsv1.TypeID, false,
			contractcolorhsv1.StateSchemaID, contractcolorhsv1.SupportSchemaID,
			contractcolorhsv1.SchemaFiles(), contractcolorhsv1.FS.ReadFile},
		{contractcolormodev1.TypeID, false,
			contractcolormodev1.StateSchemaID, contractcolormodev1.SupportSchemaID,
			contractcolormodev1.SchemaFiles(), contractcolormodev1.FS.ReadFile},
		{contractcolortempv1.TypeID, false,
			contractcolortempv1.StateSchemaID, contractcolortempv1.SupportSchemaID,
			contractcolortempv1.SchemaFiles(), contractcolortempv1.FS.ReadFile},
		{contractcolorxyv1.TypeID, false,
			contractcolorxyv1.StateSchemaID, contractcolorxyv1.SupportSchemaID,
			contractcolorxyv1.SchemaFiles(), contractcolorxyv1.FS.ReadFile},
		{contractenumactionv1.TypeID, false,
			contractenumactionv1.StateSchemaID, contractenumactionv1.SupportSchemaID,
			contractenumactionv1.SchemaFiles(), contractenumactionv1.FS.ReadFile},
		{contractenumeventv1.TypeID, true,
			contractenumeventv1.StateSchemaID, contractenumeventv1.SupportSchemaID,
			contractenumeventv1.SchemaFiles(), contractenumeventv1.FS.ReadFile},
		{contractenumsettingv1.TypeID, false,
			contractenumsettingv1.StateSchemaID, contractenumsettingv1.SupportSchemaID,
			contractenumsettingv1.SchemaFiles(), contractenumsettingv1.FS.ReadFile},
		{contractnumericsensorv1.TypeID, false,
			contractnumericsensorv1.StateSchemaID, contractnumericsensorv1.SupportSchemaID,
			contractnumericsensorv1.SchemaFiles(), contractnumericsensorv1.FS.ReadFile},
		{contractnumericsettingv1.TypeID, false,
			contractnumericsettingv1.StateSchemaID, contractnumericsettingv1.SupportSchemaID,
			contractnumericsettingv1.SchemaFiles(), contractnumericsettingv1.FS.ReadFile},
		{contractpowerv1.TypeID, false,
			contractpowerv1.StateSchemaID, contractpowerv1.SupportSchemaID,
			contractpowerv1.SchemaFiles(), contractpowerv1.FS.ReadFile},
		{contractpressurev1.TypeID, false,
			contractpressurev1.StateSchemaID, contractpressurev1.SupportSchemaID,
			contractpressurev1.SchemaFiles(), contractpressurev1.FS.ReadFile},
		{contractrelativehumidityv1.TypeID, false,
			contractrelativehumidityv1.StateSchemaID, contractrelativehumidityv1.SupportSchemaID,
			contractrelativehumidityv1.SchemaFiles(), contractrelativehumidityv1.FS.ReadFile},
		{contractspeedv1.TypeID, false,
			contractspeedv1.StateSchemaID, contractspeedv1.SupportSchemaID,
			contractspeedv1.SchemaFiles(), contractspeedv1.FS.ReadFile},
		{contracttemperaturev1.TypeID, false,
			contracttemperaturev1.StateSchemaID, contracttemperaturev1.SupportSchemaID,
			contracttemperaturev1.SchemaFiles(), contracttemperaturev1.FS.ReadFile},
	}
	byID := make(map[string]*TypeCodecs, len(sources))
	for _, source := range sources {
		codecs, err := compileSource(source)
		if err != nil {
			return nil, err
		}
		if _, duplicate := byID[codecs.TypeID]; duplicate {
			return nil, fmt.Errorf("duplicate Entity type %q in simulator registry", codecs.TypeID)
		}
		byID[codecs.TypeID] = codecs
	}
	return byID, nil
}

func compileSource(source schemaSource) (*TypeCodecs, error) {
	stateFile, ok := source.files[source.stateID]
	if !ok {
		return nil, fmt.Errorf("entity type %q has no state schema file", source.typeID)
	}
	supportFile, ok := source.files[source.supportID]
	if !ok {
		return nil, fmt.Errorf("entity type %q has no support schema file", source.typeID)
	}
	stateSchema, err := source.readFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("read state schema for %q: %w", source.typeID, err)
	}
	supportSchema, err := source.readFile(supportFile)
	if err != nil {
		return nil, fmt.Errorf("read support schema for %q: %w", source.typeID, err)
	}
	state, err := entitytypes.CompileJSONCodec[json.RawMessage](
		source.stateID, json.RawMessage(stateSchema), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("compile state codec for %q: %w", source.typeID, err)
	}
	support, err := entitytypes.CompileJSONCodec[json.RawMessage](
		source.supportID, json.RawMessage(supportSchema), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("compile support codec for %q: %w", source.typeID, err)
	}
	return &TypeCodecs{
		TypeID:      source.typeID,
		EventSource: source.eventSource,
		State:       state,
		Support:     support,
	}, nil
}
