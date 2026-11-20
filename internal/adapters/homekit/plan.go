package homekit

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/mholtzscher/hearth/entitytypes/binarysensorv1"
	"github.com/mholtzscher/hearth/entitytypes/brightnessv1"
	"github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	"github.com/mholtzscher/hearth/entitytypes/colortempv1"
	"github.com/mholtzscher/hearth/entitytypes/coverv1"
	"github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
	"github.com/mholtzscher/hearth/entitytypes/lockv1"
	"github.com/mholtzscher/hearth/entitytypes/numericsensorv1"
	"github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/entitytypes/relativehumidityv1"
	"github.com/mholtzscher/hearth/entitytypes/temperaturev1"
	"github.com/mholtzscher/hearth/entitytypes/thermostatv1"
)

const (
	roleOn               Role = "on"
	roleColorTemperature Role = "color_temperature"
	rolePosition         Role = "position"
	roleActive           Role = "active"
	roleChildLock        Role = "child_lock"
	roleMode             Role = "mode"
	roleSpeed            Role = "speed"
	rolePM25             Role = "pm25"
	roleFilter           Role = "filter_life"
	roleClass            Role = "binary_sensor"
)

// Proposal describes assignments that can be inferred without using display names.
type Proposal struct {
	Kind       AccessoryKind
	Assigned   []RoleAssignment
	Unresolved []UnresolvedRole
	Unmapped   []string
}

// UnresolvedRole explains why a proposed assignment needs operator input.
type UnresolvedRole struct {
	Role       Role
	Reason     string
	Candidates []string
}

// PlanError carries a stable reason for an exposure that cannot be installed.
type PlanError struct {
	Reason string
}

func (err *PlanError) Error() string { return err.Reason }

// Propose derives role assignments for a selected kind and reports ambiguities.
func Propose(device Device, entities []Entity, kind AccessoryKind) Proposal {
	if kind == "" {
		kind = defaultAccessoryKind(device.Kind)
	}
	entities = deviceEntities(device.ID, entities)
	proposal := Proposal{Kind: kind}
	rules := roleRules(kind)
	byRole, claims := collectCandidates(rules, entities)
	for _, rule := range rules {
		if rule.entityType == binarysensorv1.TypeID {
			continue
		}
		proposal.addProposedRole(rule, byRole[rule.role], claims)
	}
	proposal.classifyUnassigned(entities, claims)
	if kind == KindSensor && len(proposal.Assigned) == 0 && len(proposal.Unresolved) == 0 {
		proposal.Unresolved = append(proposal.Unresolved, UnresolvedRole{Reason: "missing_required"})
	}
	return proposal
}

// ValidateExposure checks selected roles against the current Core inventory.
func ValidateExposure(exposure Exposure, inventory Inventory) error {
	device, ok := inventory.Devices[exposure.DeviceID]
	if !ok {
		return &PlanError{Reason: "device_not_found"}
	}
	if !compatibleKind(device, inventory, exposure.Kind) {
		return &PlanError{Reason: "kind_incompatible"}
	}
	rules := roleRules(exposure.Kind)
	count := make(map[Role]int)
	used := make(map[string]bool)
	for _, assignment := range exposure.Entities {
		entity, exists := inventory.Entities[assignment.EntityID]
		if !exists || entity.DeviceID != device.ID {
			return &PlanError{Reason: "entity_not_on_device"}
		}
		index := slices.IndexFunc(rules, func(rule roleRule) bool { return assignment.Role == rule.role })
		if index < 0 || !rules[index].matches(entity) || (assignment.Inverted && entity.Type != binarysensorv1.TypeID) {
			return &PlanError{Reason: "entity_type_mismatch"}
		}
		if used[entity.ID] || (!rules[index].repeatable && count[assignment.Role] != 0) {
			return &PlanError{Reason: "duplicate_role"}
		}
		used[entity.ID] = true
		count[assignment.Role]++
	}
	for _, rule := range rules {
		if rule.required && count[rule.role] == 0 {
			return &PlanError{Reason: "roles_unresolved"}
		}
	}
	if len(used) == 0 {
		return &PlanError{Reason: "roles_unresolved"}
	}
	return nil
}

func defaultAccessoryKind(deviceKind string) AccessoryKind {
	switch deviceKind {
	case "light":
		return KindLightbulb
	case "relay":
		return KindSwitch
	case "lock":
		return KindLock
	case "cover":
		return KindWindowCovering
	case "thermostat":
		return KindThermostat
	case "air-purifier":
		return KindAirPurifier
	case "sensor":
		return KindSensor
	default:
		return ""
	}
}

func compatibleKind(device Device, inventory Inventory, kind AccessoryKind) bool {
	switch kind {
	case KindSwitch, KindOutlet:
		return hasDeviceEntityType(inventory, device.ID, powerv1.TypeID)
	case KindLightbulb, KindLock, KindWindowCovering, KindThermostat, KindAirPurifier, KindSensor:
		return kind == defaultAccessoryKind(device.Kind)
	}
	return false
}

func hasDeviceEntityType(inventory Inventory, device, typeID string) bool {
	for _, entity := range inventory.Entities {
		if entity.DeviceID == device && entity.Type == typeID {
			return true
		}
	}
	return false
}

func deviceEntities(deviceID string, entities []Entity) []Entity {
	selected := make([]Entity, 0, len(entities))
	for _, entity := range entities {
		if entity.DeviceID == deviceID {
			selected = append(selected, entity)
		}
	}
	slices.SortFunc(selected, func(a, b Entity) int { return strings.Compare(a.ID, b.ID) })
	return selected
}

func collectCandidates(rules []roleRule, entities []Entity) (map[Role][]string, map[string]int) {
	byRole := make(map[Role][]string, len(rules))
	claims := make(map[string]int)
	for _, rule := range rules {
		for _, entity := range entities {
			if rule.matches(entity) {
				byRole[rule.role] = append(byRole[rule.role], entity.ID)
				claims[entity.ID]++
			}
		}
	}
	return byRole, claims
}

func (proposal *Proposal) addProposedRole(rule roleRule, candidates []string, claims map[string]int) {
	switch {
	case len(candidates) == 0:
		if rule.required {
			proposal.Unresolved = append(
				proposal.Unresolved,
				UnresolvedRole{Role: rule.role, Reason: "missing_required"},
			)
		}
	case (!rule.repeatable && len(candidates) > 1) || slices.ContainsFunc(candidates, func(id string) bool {
		return claims[id] != 1
	}):
		proposal.Unresolved = append(proposal.Unresolved, UnresolvedRole{
			Role: rule.role, Reason: "ambiguous", Candidates: candidates,
		})
	default:
		for _, id := range candidates {
			proposal.Assigned = append(proposal.Assigned, RoleAssignment{Role: rule.role, EntityID: id})
		}
	}
}

func (proposal *Proposal) classifyUnassigned(entities []Entity, claims map[string]int) {
	for _, entity := range entities {
		switch {
		case proposal.Kind == KindSensor && entity.Type == binarysensorv1.TypeID:
			proposal.Unresolved = append(proposal.Unresolved, UnresolvedRole{
				Role: roleClass, Reason: "class_required", Candidates: []string{entity.ID},
			})
		case claims[entity.ID] == 0:
			proposal.Unmapped = append(proposal.Unmapped, entity.ID)
		}
	}
}

type roleRule struct {
	role       Role
	entityType string
	required   bool
	repeatable bool
	unit       string
	quantity   string
}

func (rule roleRule) matches(entity Entity) bool {
	if rule.entityType != entity.Type {
		return false
	}
	if rule.unit == "" && rule.quantity == "" {
		return true
	}
	var support struct {
		State struct {
			Unit         string `json:"unit"`
			QuantityKind string `json:"quantity_kind"`
		} `json:"state"`
	}
	return json.Unmarshal(entity.Support, &support) == nil &&
		(rule.unit == "" || rule.unit == support.State.Unit) &&
		(rule.quantity == "" || rule.quantity == support.State.QuantityKind)
}

func roleRules(kind AccessoryKind) []roleRule {
	switch kind {
	case KindLightbulb:
		return []roleRule{
			{role: roleOn, entityType: powerv1.TypeID, required: true},
			{role: "brightness", entityType: brightnessv1.TypeID},
			{role: roleColorTemperature, entityType: colortempv1.TypeID},
			{role: "color", entityType: colorhsv1.TypeID},
		}
	case KindSwitch, KindOutlet:
		return []roleRule{{role: roleOn, entityType: powerv1.TypeID, required: true, repeatable: true}}
	case KindLock:
		return []roleRule{{role: "lock", entityType: lockv1.TypeID, required: true}}
	case KindWindowCovering:
		return []roleRule{{role: rolePosition, entityType: coverv1.TypeID, required: true}}
	case KindThermostat:
		return []roleRule{{role: "thermostat", entityType: thermostatv1.TypeID, required: true}}
	case KindAirPurifier:
		return []roleRule{
			{role: roleActive, entityType: powerv1.TypeID, required: true},
			{role: roleMode, entityType: enumsettingv1.TypeID},
			{role: roleSpeed, entityType: enumsettingv1.TypeID},
			{role: roleChildLock, entityType: powerv1.TypeID},
			{role: rolePM25, entityType: numericsensorv1.TypeID, unit: "µg/m³", quantity: "particulate_matter"},
			{role: roleFilter, entityType: numericsensorv1.TypeID, unit: "%"},
		}
	case KindSensor:
		return []roleRule{
			{role: "temperature", entityType: temperaturev1.TypeID, repeatable: true},
			{role: "humidity", entityType: relativehumidityv1.TypeID, repeatable: true},
			{role: "contact", entityType: binarysensorv1.TypeID, repeatable: true},
			{role: "motion", entityType: binarysensorv1.TypeID, repeatable: true},
			{role: "occupancy", entityType: binarysensorv1.TypeID, repeatable: true},
			{role: "leak", entityType: binarysensorv1.TypeID, repeatable: true},
		}
	default:
		return nil
	}
}
