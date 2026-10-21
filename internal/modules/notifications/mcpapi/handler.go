package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type Notifications interface {
	GetDestination(context.Context, notifications.DestinationID) (notifications.Destination, error)
	ListDestinations(context.Context, int, string) (notifications.DestinationPage, error)
	SendPublic(context.Context, notifications.SendInput) (notifications.Attempt, error)
	GetAttempt(context.Context, notifications.AttemptID) (notifications.Attempt, error)
	ListAttempts(context.Context, notifications.AttemptFilter) (notifications.AttemptPage, error)
}

type handler struct{ service Notifications }
