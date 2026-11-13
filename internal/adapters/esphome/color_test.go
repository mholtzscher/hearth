package esphome //nolint:testpackage // Exercises the adapter's color conversion and command evidence.

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
)

func TestRGBWhiteCanSatisfyColorCommand(t *testing.T) {
	t.Parallel()
	// White has no recoverable hue, so the Entity keeps native RGB rather than converting to HS.
	state, err := rgbState(nativeapi.RGB{Red: 1, Green: 1, Blue: 1}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !stateMatches(routeLightColor, value, []byte(`{"active":true,"red":255,"green":255,"blue":255}`)) {
		t.Fatalf("white command cannot be satisfied by RGB State %s", value)
	}
}

func TestRGBChannelsRoundTripIncludingGreyAndWhite(t *testing.T) {
	t.Parallel()
	for _, parameters := range []string{
		`{"red":255,"green":255,"blue":255}`, `{"red":128,"green":128,"blue":128}`,
		`{"red":0,"green":0,"blue":0}`, `{"red":255,"green":0,"blue":0}`,
		`{"red":1,"green":254,"blue":17}`,
	} {
		target, command, err := prepareSet(
			boundRoute{Kind: routeLightColor},
			[]byte(parameters),
		)
		if err != nil {
			t.Fatal(err)
		}
		light := command.(nativeapi.LightCommand)
		// ESPHome normalizes channels, discarding amplitude unless color brightness is supplied separately.
		normalized := firmwareNormalizedRGB(*light.RGB)
		amplitude := 1.0
		if light.ColorBrightness != nil {
			amplitude = *light.ColorBrightness
		}
		state, err := rgbState(normalized, amplitude, true)
		if err != nil {
			t.Fatal(err)
		}
		value, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if !stateMatches(routeLightColor, value, target) {
			t.Errorf("%s -> %s does not satisfy", parameters, value)
		}
	}
}

func TestRGBRejectsInvalidChannelsAndParameters(t *testing.T) {
	t.Parallel()
	for _, channel := range []float64{-0.1, 1.1, math.NaN(), math.Inf(1)} {
		if _, err := rgbState(nativeapi.RGB{Red: channel}, 1, true); !errors.Is(err, errUnprojectable) {
			t.Errorf("red=%v error=%v", channel, err)
		}
		if _, err := rgbState(nativeapi.RGB{Red: 1}, channel, true); !errors.Is(err, errUnprojectable) {
			t.Errorf("invalid color brightness=%v error=%v", channel, err)
		}
	}
	for _, parameters := range []string{
		`{"red":256,"green":0,"blue":0}`, `{"red":-1,"green":0,"blue":0}`,
		`{"red":0.5,"green":0,"blue":0}`, `{"red":0,"green":0}`, `{"red":0,"green":0,"blue":0,"on":true}`,
	} {
		if _, _, err := prepareSet(
			boundRoute{Kind: routeLightColor},
			[]byte(parameters),
		); err == nil {
			t.Errorf("invalid color parameters accepted: %s", parameters)
		}
	}
}

func TestRGBEvidenceMatchesCoreToleranceAndActivity(t *testing.T) {
	t.Parallel()
	target := []byte(`{"active":true,"red":255,"green":128,"blue":0}`)
	for _, test := range []struct {
		value string
		want  bool
	}{
		{`{"active":true,"red":254,"green":129,"blue":1}`, true},
		{`{"active":true,"red":255,"green":130,"blue":0}`, false},
		{`{"active":false,"red":255,"green":128,"blue":0}`, false},
	} {
		if got := stateMatches(routeLightColor, []byte(test.value), target); got != test.want {
			t.Errorf("%s = %t", test.value, got)
		}
	}
}

func TestEffectCommandsUseAdvertisedChoicesAndPreserveOtherFields(t *testing.T) {
	t.Parallel()
	route := boundRoute{
		Kind:   routeLightEffect,
		Effect: effectSupport([]string{"None", "Slow Pulse", "Fast Pulse"}),
	}
	for _, choice := range []string{"None", "Slow Pulse", "Fast Pulse"} {
		parameters, _ := json.Marshal(map[string]string{"value": choice})
		target, command, err := prepareSet(route, parameters)
		if err != nil {
			t.Fatal(err)
		}
		light, ok := command.(nativeapi.LightCommand)
		if !ok || light.Effect == nil || *light.Effect != choice || light.On != nil || light.Brightness != nil ||
			light.RGB != nil || light.ColorBrightness != nil {
			t.Fatalf("effect command=%+v", command)
		}
		want, _ := json.Marshal(choice)
		if string(target) != string(want) {
			t.Fatalf("target=%s", target)
		}
	}
	if _, _, err := prepareSet(route, []byte(`{"value":"Unsupported effect"}`)); err == nil {
		t.Fatal("unadvertised effect accepted")
	}
}

func TestColorSetPreservesOtherLightSettings(t *testing.T) {
	t.Parallel()
	_, command, err := prepareSet(boundRoute{Kind: routeLightColor},
		[]byte(`{"red":0,"green":255,"blue":0}`))
	if err != nil {
		t.Fatal(err)
	}
	light, ok := command.(nativeapi.LightCommand)
	if !ok || light.On != nil || light.Brightness != nil || light.Effect != nil ||
		light.RGB == nil || *light.RGB != (nativeapi.RGB{Green: 1}) || light.ColorBrightness == nil || *light.ColorBrightness != 1 {
		t.Fatalf("color command = %+v", command)
	}
}

// Firmware 2024.11.1 LightColorValues::normalize_color is independent of the adapter's projection.
func firmwareNormalizedRGB(rgb nativeapi.RGB) nativeapi.RGB {
	maximum := max(rgb.Red, rgb.Green, rgb.Blue)
	if maximum == 0 {
		return nativeapi.RGB{Red: 1, Green: 1, Blue: 1}
	}
	return nativeapi.RGB{Red: rgb.Red / maximum, Green: rgb.Green / maximum, Blue: rgb.Blue / maximum}
}
