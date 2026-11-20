package homekit

import "time"

type AccessoryKind string

const (
	KindLightbulb      AccessoryKind = "lightbulb"
	KindSwitch         AccessoryKind = "switch"
	KindOutlet         AccessoryKind = "outlet"
	KindLock           AccessoryKind = "lock"
	KindWindowCovering AccessoryKind = "window_covering"
	KindThermostat     AccessoryKind = "thermostat"
	KindAirPurifier    AccessoryKind = "air_purifier"
	KindSensor         AccessoryKind = "sensor"
)

type Role string

type RoleAssignment struct {
	EntityID string `json:"entity_id"`
	Role     Role   `json:"role"`
	Inverted bool   `json:"inverted,omitempty"`
}

type Exposure struct {
	DeviceID  string            `json:"device_id"`
	Kind      AccessoryKind     `json:"kind"`
	Name      string            `json:"name,omitempty"`
	Entities  []RoleAssignment  `json:"entities"`
	AID       uint64            `json:"aid"`
	Slots     map[string]uint16 `json:"slots"`
	ExposedAt time.Time         `json:"exposed_at"`
}
