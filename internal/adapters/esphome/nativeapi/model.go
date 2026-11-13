package nativeapi

import "time"

// ServerInfo is what the device reports in its hello.
type ServerInfo struct {
	Name     string
	APIMajor uint32
	APIMinor uint32
}

// DeviceInfo is the subset of DeviceInfoResponse the Adapter reads. MAC is the device's text form, which current
// firmware sends as uppercase colon-separated hexadecimal.
type DeviceInfo struct {
	MAC            string
	Name           string
	FriendlyName   string
	ESPHomeVersion string
	Model          string
	Manufacturer   string
	ProjectName    string
	ProjectVersion string
	HasDeepSleep   bool
}

// EntityType names the ESPHome entity platforms the Adapter can map. Every other platform is EntityOther.
type EntityType string

const (
	EntitySensor       EntityType = "sensor"
	EntityBinarySensor EntityType = "binary_sensor"
	EntitySwitch       EntityType = "switch"
	EntityLight        EntityType = "light"
	// EntityOther is listed for identity and skip logging only.
	EntityOther EntityType = "other"
)

// EntityCategory mirrors ESPHome's entity category.
type EntityCategory uint8

const (
	CategoryNone EntityCategory = iota
	CategoryConfig
	CategoryDiagnostic
)

// ColorModeBrightness is the capability bit every brightness-capable light color mode includes.
const ColorModeBrightness uint32 = 0x02

// EntityInfo is one ListEntities response. Type-specific fields are zero for other types.
type EntityInfo struct {
	Type EntityType
	// UpstreamType is the ESPHome platform name, such as "button", kept for EntityOther skip logging.
	UpstreamType      string
	Key               uint32
	DeviceID          uint32
	Name              string
	Category          EntityCategory
	DisabledByDefault bool
	DeviceClass       string
	// Unit and AccuracyDecimals describe sensors.
	Unit             string
	AccuracyDecimals int32
	// AssumedState describes switches that cannot report their real state.
	AssumedState bool
	// ColorModes lists a light's supported color modes as capability bitmasks.
	ColorModes []uint32
}

// StateUpdate is one state push. Fields that do not apply to Type are zero.
type StateUpdate struct {
	// ReceivedAt is the local frame receipt time, preserved across buffering and publication.
	ReceivedAt time.Time
	Type       EntityType
	Key        uint32
	DeviceID   uint32
	// Missing reports that the device has no value yet.
	Missing bool
	// Number is a sensor value, converted from the wire float32 to its shortest decimal form.
	Number float64
	// Bool is a binary sensor or switch state, or a light's on/off state.
	Bool bool
	// Brightness is a light's master brightness from 0 to 1.
	Brightness float64
}

// Command is one entity command: SwitchCommand or LightCommand.
type Command interface{ nativeCommand() }

// SwitchCommand turns a switch on or off.
type SwitchCommand struct {
	Key      uint32
	DeviceID uint32
	On       bool
}

// LightCommand sets a light's on/off state and, when Brightness is non-nil, its brightness from 0 to 1.
type LightCommand struct {
	Key        uint32
	DeviceID   uint32
	On         bool
	Brightness *float64
}

func (SwitchCommand) nativeCommand() {}
func (LightCommand) nativeCommand()  {}
