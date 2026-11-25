package agent //nolint:testpackage // Checks the private authoring schema.

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

const entityRef = "#/$defs/entity"

// TestComposeViewSchemaRules keeps one spelling per authoring key: a card's
// main Entity is entity_id, an Entity is a bare ID string, and a meaning has
// one name across cards. A new card or slot that breaks a rule fails here.
func TestComposeViewSchemaRules(t *testing.T) {
	t.Parallel()
	var schema map[string]any
	require.NoError(t, json.Unmarshal(composeViewSchema, &schema))
	defs := schema["$defs"].(map[string]any)
	require.NotContains(t, defs, "source", "an Entity is a bare ID string, not a source object")

	removed := []string{"source", "temperature", "speed", "rate", "wind", "direction"}
	walkSchemaObjects(schema, "", func(path string, properties map[string]any) {
		for _, name := range removed {
			require.NotContains(t, properties, name, "%s declares removed key %q", path, name)
		}
		keys := slices.Sorted(maps.Keys(properties))
		wrapper := len(keys) > 0 && !slices.ContainsFunc(keys, func(key string) bool {
			return key != "entity_id" && key != "field"
		})
		require.False(t, wrapper, "%s wraps an Entity reference in an object", path)
	})

	for _, raw := range defs["node"].(map[string]any)["oneOf"].([]any) {
		variant := raw.(map[string]any)
		properties := variant["properties"].(map[string]any)
		cardType := properties["type"].(map[string]any)["const"]
		hasEntity := slices.ContainsFunc(slices.Collect(maps.Values(properties)), func(value any) bool {
			property, ok := value.(map[string]any)
			return ok && property["$ref"] == entityRef
		})
		if !hasEntity {
			continue
		}
		require.Contains(t, variant["required"], "entity_id", "%s must name its main Entity entity_id", cardType)
		require.Equal(t, entityRef, properties["entity_id"].(map[string]any)["$ref"], "%s entity_id", cardType)
	}
}

// TestComposeViewGuidanceUsesSchemaKeys keeps the tool description and system
// prompt from teaching keys the schema no longer accepts.
func TestComposeViewGuidanceUsesSchemaKeys(t *testing.T) {
	t.Parallel()
	stale := []string{
		"OutsideNow uses wind_direction", "Wind uses direction", "wind (speed)",
		"Wind needs speed", "gust, direction,", "numeric source and window",
		// Generalized to cards with no main Entity, such as TimeSeries.
		"Every card names its main Entity",
	}
	for name, guidance := range map[string]string{
		"tool description": composeViewDescription, "system prompt": defaultSystemPrompt,
	} {
		for _, phrase := range stale {
			require.NotContains(t, guidance, phrase, "%s teaches a removed key", name)
		}
	}
	require.Contains(t, defaultSystemPrompt, "wind_speed, wind_direction")
	for _, guidance := range []string{composeViewDescription, defaultSystemPrompt} {
		require.Contains(
			t,
			guidance,
			"A card bound to one Entity names it entity_id; TimeSeries lists its Entities in series",
		)
	}
}

// walkSchemaObjects calls visit with every properties map in the schema.
func walkSchemaObjects(value any, path string, visit func(string, map[string]any)) {
	switch item := value.(type) {
	case map[string]any:
		if properties, ok := item["properties"].(map[string]any); ok {
			visit(path, properties)
		}
		for key, member := range item {
			walkSchemaObjects(member, path+"/"+key, visit)
		}
	case []any:
		for _, member := range item {
			walkSchemaObjects(member, path+"/[]", visit)
		}
	}
}
