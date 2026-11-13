package esphome

import (
	"math"

	contractcolorrgbv1 "github.com/mholtzscher/hearth/entitytypes/colorrgbv1"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	sdkcolorrgbv1 "github.com/mholtzscher/hearth/sdk/adapter/colorrgbv1"
	sdkenumsettingv1 "github.com/mholtzscher/hearth/sdk/adapter/enumsettingv1"
)

const rgbChannelMaximum = 255

// rgbState reconstructs RGB amplitude from ESPHome's normalized channels and color brightness.
func rgbState(rgb nativeapi.RGB, colorBrightness float64, active bool) (contractcolorrgbv1.State, error) {
	for _, channel := range []float64{rgb.Red, rgb.Green, rgb.Blue, colorBrightness} {
		if math.IsNaN(channel) || math.IsInf(channel, 0) || channel < 0 || channel > 1 {
			return contractcolorrgbv1.State{}, errUnprojectable
		}
	}
	return contractcolorrgbv1.State{Active: active,
		Red:   int64(math.Round(rgb.Red * colorBrightness * rgbChannelMaximum)),
		Green: int64(math.Round(rgb.Green * colorBrightness * rgbChannelMaximum)),
		Blue:  int64(math.Round(rgb.Blue * colorBrightness * rgbChannelMaximum))}, nil
}

func colorSupport() sdkcolorrgbv1.Support {
	return sdkcolorrgbv1.Support{Operations: sdkcolorrgbv1.OperationSupport{Set: sdkcolorrgbv1.SetSupport{}}}
}

func effectSupport(choices []string) sdkenumsettingv1.Support {
	return sdkenumsettingv1.Support{State: sdkenumsettingv1.StateSupport{Choices: choices},
		Operations: sdkenumsettingv1.OperationSupport{Set: sdkenumsettingv1.SetSupport{}}}
}
