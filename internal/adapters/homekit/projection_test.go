package homekit //nolint:testpackage // Exercises the internal merge before the HAP runtime owns its public boundary.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryFactDoesNotReplaceNewerStateOrReadMetadata(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.November, 19, 0, 0, 5, 0, time.UTC)
	const entityID = "ent_01900000-0000-7000-8000-000000000001"
	current := &EntityState{
		Value: json.RawMessage("true"), ObservationID: "obs_01900000-0000-7000-8000-000000000002", ObservedAt: at,
	}
	inventory := Inventory{Entities: map[string]Entity{
		entityID: {ID: entityID, Enabled: true, Availability: "unavailable", State: current},
	}}
	fact := ObservationFact{
		EntityID: entityID, Value: json.RawMessage("false"),
		ObservationID: "obs_01900000-0000-7000-8000-000000000001", ObservedAt: at.Add(-time.Second),
	}
	assert.False(t, inventory.applyFact(fact))
	assert.Same(t, current, inventory.Entities[entityID].State)

	fact.ObservationID = "obs_01900000-0000-7000-8000-000000000003"
	fact.ObservedAt = at.Add(time.Second)
	require.True(t, inventory.applyFact(fact))
	entity := inventory.Entities[entityID]
	assert.JSONEq(t, "false", string(entity.State.Value))
	assert.False(t, entity.reachable(), "a Fact is not availability evidence")

	fact.Value = json.RawMessage("true")
	assert.False(t, inventory.applyFact(fact), "repeat Observation ID")
	assert.JSONEq(t, "false", string(inventory.Entities[entityID].State.Value))
}
