//go:build integration

package agent //nolint:testpackage // Tests construct the service without a chat model.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/agent/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// newListRouteFixture mounts the real agent routes over real SQLite with a
// model-free service: the list and create paths touch only the database.
func newListRouteFixture(t *testing.T) (*echo.Echo, *Service, context.Context) {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	service := &Service{database: database, queries: dbsqlc.New(database)}
	router := echo.New()
	openapi := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	RegisterHTTP(huma.NewGroup(openapi, "/v1"), service, nil)
	return router, service, context.Background()
}

func TestFutureAutomationHistoryMatchesPublishedSchema(t *testing.T) {
	t.Parallel()
	router, service, ctx := newListRouteFixture(t)
	conversation, err := service.CreateConversation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"kind":"hearth.automation","artifact_id":"aart_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema_version":2,"mode":{"future":true},"expected_revision":"future","definition_json":{},"automation_id":42}`
	if err = service.persistMessage(
		ctx,
		conversation.ID,
		&schema.Message{Role: schema.Tool, ToolName: "present_automation", ToolCallID: "future", Content: raw},
	); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/v1/agent/conversations/"+conversation.ID+"/messages", nil),
	)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"mode":{"future":true}`) {
		t.Fatalf("history: %d %s", response.Code, response.Body.String())
	}
	openapi := httptest.NewRecorder()
	router.ServeHTTP(openapi, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	document, err := jsonschema.UnmarshalJSON(openapi.Body)
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const location = "https://hearth.invalid/agent-history"
	if err = compiler.AddResource(location, document); err != nil {
		t.Fatal(err)
	}
	contract, err := compiler.Compile(location + "#/components/schemas/HistoryBody")
	if err != nil {
		t.Fatal(err)
	}
	responseBytes := response.Body.Bytes()
	body, err := jsonschema.UnmarshalJSON(strings.NewReader(string(responseBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.Validate(body); err != nil {
		t.Fatalf("history violates published schema: %v", err)
	}
	invalidV1, err := jsonschema.UnmarshalJSON(
		strings.NewReader(strings.Replace(string(responseBytes), `"schema_version":2`, `"schema_version":1`, 1)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.Validate(invalidV1); err == nil {
		t.Fatal("v1 artifact accepted invalid variant fields")
	}
}

func TestListConversationsRoute(t *testing.T) {
	t.Parallel()
	router, service, ctx := newListRouteFixture(t)

	conversation, createErr := service.CreateConversation(ctx)
	if createErr != nil {
		t.Fatal(createErr)
	}
	raw, marshalErr := json.Marshal(schema.UserMessage("what devices do you see?"))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if _, execErr := service.database.ExecContext(ctx,
		`INSERT INTO agent_messages(conversation_id, role, message_json, created_at)
		  VALUES (?, ?, ?, ?)`,
		conversation.ID, "user", string(raw), "2026-09-10T12:00:00Z",
	); execErr != nil {
		t.Fatal(execErr)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/agent/conversations", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Conversations []ConversationSummaryBody `json:"conversations"`
	}
	if unmarshalErr := json.Unmarshal(recorder.Body.Bytes(), &body); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if len(body.Conversations) != 1 {
		t.Fatalf("conversations = %d, want 1: %s", len(body.Conversations), recorder.Body.String())
	}
	entry := body.Conversations[0]
	if entry.ID != conversation.ID {
		t.Fatalf("id = %s, want %s", entry.ID, conversation.ID)
	}
	if entry.MessageCount != 1 {
		t.Fatalf("message_count = %d, want 1", entry.MessageCount)
	}
	if entry.Preview != "what devices do you see?" {
		t.Fatalf("preview = %q, want first user message", entry.Preview)
	}
	if entry.LastMessageAt == nil {
		t.Fatal("last_message_at is nil, want newest message time")
	}
}

func TestListConversationsRouteEmpty(t *testing.T) {
	t.Parallel()
	router, _, _ := newListRouteFixture(t)

	request := httptest.NewRequest(http.MethodGet, "/v1/agent/conversations", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var body struct {
		Conversations []ConversationSummaryBody `json:"conversations"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Conversations == nil || len(body.Conversations) != 0 {
		t.Fatalf("conversations = %v, want empty array", body.Conversations)
	}
}
