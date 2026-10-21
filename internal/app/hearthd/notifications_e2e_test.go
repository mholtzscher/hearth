//go:build e2e

package hearthd //nolint:testpackage // Exercises real assembly and shutdown with the existing Run harness.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	notificationsapi "github.com/mholtzscher/hearth/internal/modules/notifications/httpapi"
	notificationssqlite "github.com/mholtzscher/hearth/internal/modules/notifications/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// Production startup must install registration, HTTP and the agent's MCP catalog
// without requiring a synthetic Device. This catches omissions in run.go that a
// manually assembled transport fixture cannot detect.
//
//nolint:gocognit // Linear process startup and persisted teardown checks stay in lifecycle order.
func TestNotificationProductionAssembly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	broker := startLifecycleNATSServer(t)
	address, options := reserveLoopbackListener(t)
	config := Config{HouseholdTimezone: "UTC", HTTPAddr: address, NATSURL: broker.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "core.db"), Agent: requiredAgentConfig(t)}
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() { runErrors <- runWithOptions(runContext, config, slog.New(slog.DiscardHandler), options) }()
	client := newNonPoolingHTTPClient(t)
	waitForCoreHTTPStatus(ctx, t, client, address, "/readyz", runErrors)
	session, err := adapter.Connect(
		ctx,
		adapter.Config{
			AdapterID:       "only-notifications",
			SoftwareName:    "controlled-provider",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	_, err = session.StartNotificationServer(
		ctx,
		func(context.Context, adapter.NotificationRequest) adapter.NotificationOutcome {
			return adapter.NotificationOutcome{Outcome: "accepted"}
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	id, err := session.RegisterNotificationDestination(ctx, "home", "Home")
	if err != nil {
		t.Fatal(err)
	}
	mcpClient, err := mcp.NewClient(&mcp.Implementation{Name: "production-catalog-test", Version: "1"}, nil).
		Connect(ctx,
			&mcp.StreamableClientTransport{Endpoint: "http://" + address + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := mcpClient.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "notification") {
			count++
		}
	}
	if count != 5 {
		t.Fatalf("production notification tools = %d, want 5", count)
	}
	if err = mcpClient.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+address+"/v1/notification-destinations/"+id+"/notifications",
		strings.NewReader(`{"message":"production assembly"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("production send = %d: %s", response.StatusCode, raw)
	}
	a := decodeNotification[notificationsapi.AttemptBody](t, raw)
	if a.Status != "accepted" {
		t.Fatalf("production attempt: %+v", a)
	}
	// Release while Core still serves its Session lifecycle.
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-ctx.Done():
		t.Fatal("production shutdown did not finish")
	}
	database, err := platformdb.Open(ctx, config.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	saved, err := notificationssqlite.NewRepository(database).GetAttempt(ctx, notifications.AttemptID(a.ID))
	if err != nil || saved.Outcome != notifications.Accepted {
		t.Fatalf("production durable attempt: %+v %v", saved, err)
	}
	var devicesCount int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM devices`).
		Scan(&devicesCount); err != nil ||
		devicesCount != 0 {
		t.Fatalf("synthetic Devices = %d: %v", devicesCount, err)
	}
}
