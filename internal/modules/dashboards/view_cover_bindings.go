package dashboards

import "encoding/json"

// coverPositionField is the numeric State field a Cover validates its source
// through. The card reads the whole State, position and motion together.
const coverPositionField = "/position"

// coverBindings returns the cover's one binding. The schema pins the Entity
// type, and the position field checks it against the stored Entity.
func coverBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	source := props.Source
	source.Field, source.ValueKind = coverPositionField, numericValueKind
	return []binding{source}, nil
}
