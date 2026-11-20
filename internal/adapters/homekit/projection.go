package homekit

// applyFact advances the State of a known Entity and leaves all HTTP-owned
// metadata intact. The runtime serializes changes and filters by exposure.
func (inventory *Inventory) applyFact(fact ObservationFact) bool {
	entity, exists := inventory.Entities[fact.EntityID]
	if !exists {
		return false
	}
	if state := entity.State; state != nil &&
		(fact.ObservationID == state.ObservationID || fact.ObservedAt.Before(state.ObservedAt)) {
		return false
	}
	state := &EntityState{
		Value: fact.Value, ObservationID: fact.ObservationID, ObservedAt: fact.ObservedAt,
	}
	if !validEntityState(state) {
		return false
	}
	entity.State = state
	inventory.Entities[fact.EntityID] = entity
	return true
}

func (entity Entity) reachable() bool {
	return entity.Enabled && entity.State != nil &&
		(entity.Availability == "available" || entity.Availability == "unknown")
}
