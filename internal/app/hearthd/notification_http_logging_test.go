package hearthd //nolint:testpackage // Tests exercise package-private HTTP assembly.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func TestNotificationHTTPUsesInjectedLogger(t *testing.T) {
	t.Parallel()
	logger, records := withRecording(slog.LevelError)
	handler, _ := newHTTPHandler(
		logger,
		&stubDevices{},
		&stubAutomations{},
		&failedNotificationRead{},
		&stubDevices{},
		&stubFunctions{},
		&stubAgent{},
		nil,
		&stubDevices{},
		&stubAutomations{},
		&stubFunctions{},
		newMCPServer(
			&stubDevices{},
			&stubAutomations{},
			&stubNotifications{},
			&stubDevices{},
			&stubFunctions{},
			logger,
		),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/v1/notifications/ntf_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d: %s", response.Code, response.Body.String())
	}
	logged := records.snapshot()
	if len(logged) != 1 {
		t.Fatalf("want one injected diagnostic, got %d", len(logged))
	}
	requireRecordAttr(t, logged[0], "event", "notification.http_internal_failure")
	requireRecordAttr(t, logged[0], "operation", "get-notification")
}

type failedNotificationRead struct{ stubNotifications }

func (*failedNotificationRead) GetAttempt(context.Context, notifications.AttemptID) (notifications.Attempt, error) {
	return notifications.Attempt{}, errors.New("private dependency failure")
}
