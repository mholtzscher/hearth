package mcpapi_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationsmcp "github.com/mholtzscher/hearth/internal/modules/notifications/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type inputRecorder struct {
	mutex                    sync.Mutex
	sends                    []notifications.SendInput
	gets                     []notifications.AttemptID
	lists                    []notifications.AttemptFilter
	sendErr, getErr, listErr error
	attempt                  notifications.Attempt
	destination              notifications.Destination
	pageLimit                int
	pageCursor               string
}

func (r *inputRecorder) SendPublic(_ context.Context, in notifications.SendInput) (notifications.Attempt, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.sends = append(r.sends, in)
	a := r.attempt
	a.ID, a.DestinationID, a.Message = in.AttemptID, in.DestinationID, in.Message
	if a.Outcome == "" {
		a.Outcome = notifications.Accepted
	}
	return a, r.sendErr
}
func (r *inputRecorder) GetAttempt(_ context.Context, id notifications.AttemptID) (notifications.Attempt, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.gets = append(r.gets, id)
	return r.attempt, r.getErr
}

func (r *inputRecorder) ListAttempts(
	_ context.Context,
	in notifications.AttemptFilter,
) (notifications.AttemptPage, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.lists = append(r.lists, in)
	var items []notifications.Attempt
	if r.attempt.ID != "" {
		items = append(items, r.attempt)
	}
	return notifications.AttemptPage{Items: items, NextCursor: r.pageCursor}, r.listErr
}

func (r *inputRecorder) GetDestination(
	context.Context,
	notifications.DestinationID,
) (notifications.Destination, error) {
	return r.destination, r.getErr
}

func (r *inputRecorder) ListDestinations(
	_ context.Context,
	limit int,
	_ string,
) (notifications.DestinationPage, error) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.pageLimit = limit
	var items []notifications.Destination
	if r.destination.ID != "" {
		items = append(items, r.destination)
	}
	return notifications.DestinationPage{Items: items, NextCursor: r.pageCursor}, r.listErr
}

func inputMCP(t *testing.T, service notificationsmcp.Notifications) *mcp.ClientSession {
	t.Helper()
	server := mcptools.New(mcptools.Config{Name: "notification-test", Version: "1"})
	notificationsmcp.Register(server, service)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	session, err := server.Raw().Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func call(
	t *testing.T,
	client *mcp.ClientSession,
	name string,
	args map[string]any,
) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return result, fields
}

func (r *inputRecorder) assertCalls(t *testing.T, sends, gets, lists int) {
	t.Helper()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.sends) != sends || len(r.gets) != gets || len(r.lists) != lists {
		t.Fatalf("service calls = %d/%d/%d, want %d/%d/%d", len(r.sends), len(r.gets), len(r.lists), sends, gets, lists)
	}
}

func (r *inputRecorder) assertSend(t *testing.T, destination notifications.DestinationID, message string) {
	t.Helper()
	r.mutex.Lock()
	defer r.mutex.Unlock()
	in := r.sends[0]
	if in.DestinationID != destination || notifications.ValidateID(string(in.AttemptID), "ntf_") != nil ||
		in.Message.Text != message ||
		in.Message.Title != "" {
		t.Fatalf("send conversion: %+v", in)
	}
}
