package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// stubNotifications supplies the required Notifications API dependency in tests
// of other modules. Every operation panics on an unexpected call.
type stubNotifications struct{}

func (*stubNotifications) GetDestination(
	context.Context,
	notifications.DestinationID,
) (notifications.Destination, error) {
	panic("unexpected GetDestination call")
}

func (*stubNotifications) ListDestinations(context.Context, int, string) (notifications.DestinationPage, error) {
	panic("unexpected ListDestinations call")
}

func (*stubNotifications) SendPublic(context.Context, notifications.SendInput) (notifications.Attempt, error) {
	panic("unexpected SendPublic call")
}

func (*stubNotifications) GetAttempt(context.Context, notifications.AttemptID) (notifications.Attempt, error) {
	panic("unexpected GetAttempt call")
}

func (*stubNotifications) ListAttempts(
	context.Context,
	notifications.AttemptFilter,
) (notifications.AttemptPage, error) {
	panic("unexpected ListAttempts call")
}
