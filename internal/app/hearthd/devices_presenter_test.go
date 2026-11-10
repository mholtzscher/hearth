package hearthd //nolint:testpackage // Tests reuse application assembly fixtures.

import (
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

func testDevicesPresenter() *devices.Presenter {
	return newDevicesPresenter(household.Settings{UnitSystem: household.UnitSystemMetric})
}
