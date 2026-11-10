// Package household holds household-wide settings that are fixed for the life
// of the process, such as how people see measurements.
package household

import "fmt"

// UnitSystem is the household's display preference for measurements.
// Canonical State never changes; Core projects read responses for display.
type UnitSystem string

const (
	// UnitSystemMetric shows SI-style units such as °C and hPa.
	UnitSystemMetric UnitSystem = "metric"
	// UnitSystemUS shows US customary units such as °F, mi/h and in Hg.
	UnitSystemUS UnitSystem = "us"
)

// ParseUnitSystem accepts "metric", "us", or empty, which selects metric.
func ParseUnitSystem(value string) (UnitSystem, error) {
	switch UnitSystem(value) {
	case "", UnitSystemMetric:
		return UnitSystemMetric, nil
	case UnitSystemUS:
		return UnitSystemUS, nil
	default:
		return "", fmt.Errorf(`household_unit_system must be "metric" or "us"`)
	}
}

// Settings are household-wide values fixed for the life of the process.
type Settings struct {
	// Timezone is the configured IANA timezone name.
	Timezone string
	// UnitSystem is the display preference; the zero value reads as metric.
	UnitSystem UnitSystem
}
