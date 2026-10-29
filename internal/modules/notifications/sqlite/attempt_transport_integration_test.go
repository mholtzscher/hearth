//go:build integration

package sqlite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

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
	var logs bytes.Buffer
	notificationhttp.Register(huma.NewGroup(api, "/v1"), service, slog.New(slog.NewJSONHandler(&logs, nil)))
	for _, test := range []struct{ path, operation, schemaPath string }{
		{"/v1/notifications/" + string(id), "get-notification", "/v1/notifications/{id}"},
		{"/v1/notifications", "list-notifications", "/v1/notifications"},
	} {
		logs.Reset()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d, want 500: %s", test.path, response.Code, response.Body.String())
		}
		assertNotificationProblem(t, api, test.schemaPath, response)
		var record map[string]any
		if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
			t.Fatalf("want exactly one diagnostic: %s, %v", logs.String(), err)
		}
		if record["event"] != "notification.http_internal_failure" || record["level"] != "ERROR" ||
			record["operation"] != test.operation || record["error_code"] != "internal_error" ||
			record["error_type"] != "*sqlite.storedAttemptError" || strings.Contains(logs.String(), "private_stored_value") {
			t.Fatalf("unsafe or incorrect diagnostic: %s", logs.String())
		}
	}
}

func assertNotificationProblem(t *testing.T, api huma.API, path string, response *httptest.ResponseRecorder) {
	t.Helper()
	var problem map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"$schema": "https://example.com/schemas/ErrorModel.json",
		"title":   "Internal Server Error", "status": float64(500), "detail": "internal error",
	}
	if !reflect.DeepEqual(problem, want) || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("incorrect full problem response: headers=%v body=%s", response.Header(), response.Body.String())
	}
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/openapi", document); err != nil {
		t.Fatal(err)
	}
	pointer := strings.ReplaceAll(path, "/", "~1")
	schema, err := compiler.Compile("https://hearth.invalid/openapi#/paths/" + pointer +
		"/get/responses/default/content/application~1problem+json/schema")
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(problem); err != nil {
		t.Fatalf("problem disagrees with published schema: %v", err)
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
