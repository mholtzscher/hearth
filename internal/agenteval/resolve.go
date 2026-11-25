package agenteval

import (
	"fmt"
	"strings"
)

// Resolve replaces {{entity:...}}, {{device:...}}, {{dashboard:...}}, and
// {{automation:...}} placeholders with this trial's canonical IDs. Entity and
// Device names default to the sim-healthy adapter: {{entity:simulated-light/power}}.
func Resolve(template string, inventory Inventory) (string, error) {
	var missing []string
	resolved := placeholder.ReplaceAllStringFunc(template, func(match string) string {
		parts := placeholder.FindStringSubmatch(match)
		id, ok := lookup(parts[1], parts[2], inventory)
		if !ok {
			missing = append(missing, parts[1]+":"+parts[2])
		}
		return id
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unknown references: %s", strings.Join(missing, ", "))
	}
	return resolved, nil
}

// Placeholder names may omit the adapter: device/key for an Entity and
// device for a Device.
const (
	entityNameSegments = 2
	deviceNameSegments = 1
)

func lookup(kind, name string, inventory Inventory) (string, bool) {
	segments := strings.Split(name, "/")
	switch kind {
	case "entity":
		if len(segments) == entityNameSegments {
			segments = append([]string{DefaultAdapter}, segments...)
		}
		id, ok := inventory.Entities[strings.Join(segments, "/")]
		return id, ok
	case "device":
		if len(segments) == deviceNameSegments {
			segments = append([]string{DefaultAdapter}, segments...)
		}
		id, ok := inventory.Devices[strings.Join(segments, "/")]
		return id, ok
	case "dashboard":
		saved, ok := inventory.Dashboards[name]
		return saved.ID, ok
	case "automation":
		saved, ok := inventory.Automations[name]
		return saved.ID, ok
	case "label":
		id, ok := inventory.Labels[name]
		return id, ok
	}
	return "", false
}

// EntityID resolves a scenario reference, or returns "" when unknown.
func (inventory Inventory) EntityID(ref EntityRef) string {
	return inventory.Entities[ref.InventoryKey()]
}
