//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationhttp "github.com/mholtzscher/hearth/internal/modules/notifications/httpapi"
	notificationmcp "github.com/mholtzscher/hearth/internal/modules/notifications/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestStoredAttemptCorruptionIsInternalAcrossReadTransports(t *testing.T) {
	t.Parallel()
	f, attempt := retainedAttempt(t)
	service, err := notifications.NewService(t.Context(), f.repo,
		senderFunc(func(context.Context, notifications.Attempt) notifications.Evidence {
			t.Error("history read dispatched a send")
			return notifications.Evidence{}
		}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.ExecContext(t.Context(),
		"UPDATE notification_attempts SET outcome = 'rejected', receipt = '', reason_code = ? WHERE id = ?",
		"private_stored_value", attempt.ID,
	); err != nil {
		t.Fatal(err)
	}

	assertStoredAttemptHTTP(t, service, attempt.ID)
	assertStoredAttemptMCP(t, service, attempt.ID)
	var retained string
	if err = f.database.QueryRowContext(t.Context(),
		"SELECT reason_code FROM notification_attempts WHERE id = ?", attempt.ID,
	).Scan(&retained); err != nil || retained != "private_stored_value" {
		t.Fatalf("history read repaired or removed the retained evidence: %q, %v", retained, err)
	}
}

func assertStoredAttemptHTTP(t *testing.T, service *notifications.Service, id notifications.AttemptID) {
	t.Helper()
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Notifications", "1"))
	notificationhttp.Register(huma.NewGroup(api, "/v1"), service)
	for _, path := range []string{"/v1/notifications/" + string(id), "/v1/notifications"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d, want 500: %s", path, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private_stored_value") {
			t.Fatalf("%s exposed retained evidence: %s", path, response.Body.String())
		}
	}
}

func assertStoredAttemptMCP(t *testing.T, service *notifications.Service, id notifications.AttemptID) {
	t.Helper()
	client := retainedNotificationMCP(t, service)
	for _, tool := range []string{"get_notification", "list_notifications"} {
		arguments := map[string]any{}
		if tool == "get_notification" {
			arguments["id"] = string(id)
		}
		result, callErr := client.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: arguments})
		if callErr != nil {
			t.Fatal(callErr)
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !result.IsError || strings.Contains(string(raw), "private_stored_value") {
			t.Fatalf("%s did not return a safe failure: %s", tool, raw)
		}
		structured, marshalErr := json.Marshal(result.StructuredContent)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var fields map[string]any
		if err := json.Unmarshal(structured, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 2 || fields["failure_code"] != "internal_error" || fields["message"] != "internal error" {
			t.Fatalf("%s failure = %s", tool, structured)
		}
	}
}

func retainedNotificationMCP(t *testing.T, service *notifications.Service) *mcp.ClientSession {
	t.Helper()
	server := mcptools.New(mcptools.Config{Name: "notification-storage-test", Version: "1"})
	notificationmcp.Register(server, service)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Raw().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
