package dashboards

import (
	"encoding/json"
	"slices"
	"strconv"
)

// choiceRule names an enum-setting choice shape a component requires.
type choiceRule string

const (
	anyChoices   choiceRule = ""
	modeChoices  choiceRule = "modes"  // 2 to 5 choices
	levelChoices choiceRule = "levels" // exactly "1" to "N", N from 2 to 10
)

const (
	unitParticulateMatter = "µg/m³"
	unitPercent           = "%"
	minModeChoices        = 2
	maxModeChoices        = 5
	minLevelChoices       = 2
	maxLevelChoices       = 10
	airPurifierSameDevice = "AirPurifier Entities must belong to the power Entity's Device"
)

//nolint:gochecknoglobals // Immutable rejection reasons keyed by choice rule.
var choiceReasons = map[choiceRule]string{
	anyChoices:   "",
	modeChoices:  "AirPurifier modes need an enum setting with 2 to 5 choices",
	levelChoices: "AirPurifier fan levels need an enum setting whose choices are 1 to N",
}

// airPurifierBindings returns one binding per bound role. It rejects a role
// that repeats another role's Entity before any Entity is read.
func airPurifierBindings(raw json.RawMessage) ([]binding, error) {
	var props struct {
		Power      binding  `json:"power"`
		PM25       binding  `json:"pm25"`
		FilterLife *binding `json:"filter_life"`
		Mode       *binding `json:"mode"`
		FanLevel   *binding `json:"fan_level"`
		Display    *binding `json:"display"`
		ChildLock  *binding `json:"child_lock"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return nil, err
	}
	power := props.Power
	power.ValueKind = textValueKind
	onPowerDevice := func(b binding) binding {
		b.SameDeviceAs, b.SameDeviceReason = power.EntityID, airPurifierSameDevice
		return b
	}
	pm25 := onPowerDevice(props.PM25)
	pm25.ValueKind, pm25.RequiredUnit = numericValueKind, unitParticulateMatter
	pm25.UnitReason = "AirPurifier PM2.5 needs a numeric sensor in µg/m³"
	result := []binding{power, pm25}
	optional := []struct {
		source *binding
		kind   string
		unit   string
		reason string
		rule   choiceRule
	}{
		{
			props.FilterLife,
			numericValueKind,
			unitPercent,
			"AirPurifier filter life needs a numeric sensor in %",
			anyChoices,
		},
		{props.Mode, textValueKind, "", "", modeChoices},
		{props.FanLevel, textValueKind, "", "", levelChoices},
		{props.Display, textValueKind, "", "", anyChoices},
		{props.ChildLock, textValueKind, "", "", anyChoices},
	}
	for _, role := range optional {
		if role.source == nil {
			continue
		}
		b := onPowerDevice(*role.source)
		b.ValueKind, b.RequiredUnit, b.UnitReason, b.Choices = role.kind, role.unit, role.reason, role.rule
		result = append(result, b)
	}
	seen := make(map[string]bool, len(result))
	for _, b := range result {
		if seen[b.EntityID] {
			return nil, invalidDefinition("each AirPurifier role must name a different Entity")
		}
		seen[b.EntityID] = true
	}
	return result, nil
}

// validateChoices applies a binding's choice rule to the enum setting's
// support.state.choices.
func validateChoices(b binding, support json.RawMessage) error {
	if b.Choices == anyChoices {
		return nil
	}
	var decoded struct {
		State struct {
			Choices []string `json:"choices"`
		} `json:"state"`
	}
	if err := json.Unmarshal(support, &decoded); err != nil || !choicesFit(b.Choices, decoded.State.Choices) {
		return invalidDefinition(choiceReasons[b.Choices])
	}
	return nil
}

func choicesFit(rule choiceRule, choices []string) bool {
	switch rule {
	case modeChoices:
		return len(choices) >= minModeChoices && len(choices) <= maxModeChoices
	case levelChoices:
		return len(choices) >= minLevelChoices && len(choices) <= maxLevelChoices && isLevels(choices)
	case anyChoices:
		return true
	}
	return false
}

// isLevels reports whether choices are exactly "1" to "N" in order.
func isLevels(choices []string) bool {
	return slices.Equal(choices, levelNames(len(choices)))
}

func levelNames(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = strconv.Itoa(i + 1)
	}
	return names
}
