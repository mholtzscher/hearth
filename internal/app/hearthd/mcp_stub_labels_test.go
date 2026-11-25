package hearthd //nolint:testpackage // Completes the Devices boundary double the catalog tests share.

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The MCP tools reach the Label catalog through the same Devices boundary. The
// catalog wiring tests list tools without calling them, so any call is a bug.

func (*stubDevices) CreateLabel(context.Context, devices.CreateLabelParams) (devices.Label, error) {
	panic("unexpected CreateLabel call")
}

func (*stubDevices) RenameLabel(context.Context, devices.LabelID, string) (devices.Label, error) {
	panic("unexpected RenameLabel call")
}

func (*stubDevices) DeleteLabel(context.Context, devices.LabelID) error {
	panic("unexpected DeleteLabel call")
}

func (*stubDevices) ListLabels(context.Context) ([]devices.Label, error) {
	panic("unexpected ListLabels call")
}
