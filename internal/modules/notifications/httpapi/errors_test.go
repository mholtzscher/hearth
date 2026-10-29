package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationsapi "github.com/mholtzscher/hearth/internal/modules/notifications/httpapi"
)

func TestNotificationHTTPDiagnosticsOwnUnexpectedFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                 string
		cause                error
		canceled, diagnostic bool
		status               int
	}{
		{"unexpected", errors.New("private_error_text"), false, true, 500},
		{"wrapped persistence", fmt.Errorf("private_error_text: %w", notifications.ErrPersistence), false, true, 500},
		{"invalid input", notifications.ErrInvalidInput, false, false, 422},
		{"caller canceled", context.Canceled, true, false, 500},
		{"persistence failure after caller cancellation", errors.Join(notifications.ErrPersistence, context.Canceled), true, true, 500},
		{"dependency canceled", context.Canceled, false, true, 500},
		{"fault after caller cancellation", errors.New("private_error_text"), true, true, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.canceled {
				cancel()
			}
			var logs bytes.Buffer
			capture := &notificationContextHandler{Handler: slog.NewJSONHandler(&logs, nil), expected: ctx}
			router := echo.New()
			api := humaecho.New(router, huma.DefaultConfig("Notifications", "1"))
			notificationsapi.Register(api, &inputRecorder{getErr: test.cause}, slog.New(capture))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet,
				"/notifications/ntf_01890f47-7a6b-7c4d-8e9f-0123456789ab", nil))
			if response.Code != test.status || (logs.Len() > 0) != test.diagnostic {
				t.Fatalf("status=%d logs=%s", response.Code, logs.String())
			}
			if test.diagnostic && !capture.matched {
				t.Fatal("diagnostic lost operation context")
			}
			if strings.Contains(logs.String()+response.Body.String(), "private_error_text") {
				t.Fatal("diagnostic or response exposed private cause")
			}
		})
	}
}

// Each test issues one synchronous request through this unshared handler.
type notificationContextHandler struct {
	slog.Handler

	expected context.Context
	matched  bool
}

func (handler *notificationContextHandler) Handle(ctx context.Context, record slog.Record) error {
	handler.matched = ctx == handler.expected
	return handler.Handler.Handle(ctx, record)
}
