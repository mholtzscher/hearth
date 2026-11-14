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
	EntityTextSensor   EntityType = "text_sensor"
	EntityBinarySensor EntityType = "binary_sensor"
	EntitySwitch       EntityType = "switch"
	EntityLight        EntityType = "light"
	EntityCover        EntityType = "cover"
	EntityLock         EntityType = "lock"
	EntityClimate      EntityType = "climate"
	EntityNumber       EntityType = "number"
	EntitySelect       EntityType = "select"
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

// ColorModeRGB is the capability bit of RGB-bearing light modes.
const ColorModeRGB uint32 = 0x20

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
	// Unit describes sensors and numbers; AccuracyDecimals describes sensors.
	Unit             string
	AccuracyDecimals int32
	// AssumedState describes switches that cannot report their real state.
	AssumedState bool
	// ColorModes lists a light's supported color modes as capability bitmasks.
	ColorModes []uint32
	Effects    []string
	// Cover and lock capabilities constrain the operations the Adapter may expose.
	SupportsPosition bool
	SupportsStop     bool
	RequiresCode     bool
	Climate          *ClimateInfo
	Number           *NumberInfo
	Options          []string
}

// ClimateInfo describes native climate capabilities and their native-unit visual bounds.
type ClimateInfo struct {
	Modes                                                []string
	StandardFanModes                                     []string
	CustomFanModes                                       []string
	TemperatureUnit                                      string
	CurrentTemperature, TwoPointTarget, RequiresTwoPoint bool
	CurrentHumidity, TargetHumidity, Activity            bool
	MinimumTemperature, MaximumTemperature               float64
	TargetTemperatureStep                                float64
	MinimumHumidity, MaximumHumidity                     float64
}

// NumberInfo describes a number's advertised native bounds and increment.
type NumberInfo struct {
	Minimum, Maximum, Step float64
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
	// Number is a sensor or number value, converted from wire float32 to its shortest decimal form.
	Number float64
	// Bool is a binary sensor or switch state, or a light's on/off state.
	Bool bool
	// Brightness is a light's master brightness from 0 to 1.
	Brightness float64
	// Text is a text sensor's reported value, including a valid empty string.
	Text      string
	ColorMode uint32
	RGB       RGB
	// ColorBrightness is the RGB-only amplitude, independent of master Brightness and power.
	ColorBrightness float64
	Effect          string
	Position        float64
	CoverOperation  string
	LockState       string
	Climate         *ClimateState
}

// ClimateState is one native snapshot, with non-finite readings represented as absent.
type ClimateState struct {
	Mode                                        string
	Activity                                    *string
	CurrentTemperature, TargetTemperature       *float64
	TargetTemperatureLow, TargetTemperatureHigh *float64
	CurrentHumidity, TargetHumidity             *float64
	StandardFanMode, CustomFanMode              *string
}

// RGB is an upstream color with each channel in the range 0..1, separate from master brightness.
type RGB struct{ Red, Green, Blue float64 }

// Command is one supported entity command.
type Command interface{ nativeCommand() }

// SwitchCommand turns a switch on or off.
type SwitchCommand struct {
	Key      uint32
	DeviceID uint32
	On       bool
}

// LightCommand changes only the non-nil fields, preserving unrelated light settings.
type LightCommand struct {
	Key             uint32
	DeviceID        uint32
	On              *bool
	Brightness      *float64
	RGB             *RGB
	ColorBrightness *float64
	Effect          *string
}

func (SwitchCommand) nativeCommand() {}
func (LightCommand) nativeCommand()  {}

// CoverCommand sets a native 0..1 position or sends stop, never both.
type CoverCommand struct {
	Key      uint32
	DeviceID uint32
	Position *float64
	Stop     bool
}

// LockCommand changes lock State without opening the lock or supplying a code.
type LockCommand struct {
	Key      uint32
	DeviceID uint32
	Locked   bool
}

func (CoverCommand) nativeCommand() {}
func (LockCommand) nativeCommand()  {}

// ClimateCommand changes one control family without implying a mode or schedule change.
type ClimateCommand struct {
	Key, DeviceID                               uint32
	Mode                                        *string
	TargetTemperature                           *float64
	TargetTemperatureLow, TargetTemperatureHigh *float64
	StandardFanMode, CustomFanMode              *string
	TargetHumidity                              *float64
}

// NumberCommand sets one number in its advertised native unit.
type NumberCommand struct {
	Key, DeviceID uint32
	Value         float64
}

// SelectCommand selects an exact advertised option.
type SelectCommand struct {
	Key, DeviceID uint32
	Value         string
}

func (ClimateCommand) nativeCommand() {}
func (NumberCommand) nativeCommand()  {}
func (SelectCommand) nativeCommand()  {}
