package dashboards

import "encoding/json"

// thermostatModeField is the required text State field a Thermostat validates
// its source through. The card reads the whole State.
const thermostatModeField = "/mode"

func thermostatBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Source binding `json:"source"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	source := props.Source
	source.Field, source.ValueKind = thermostatModeField, textValueKind
	return []binding{source}, nil
}
