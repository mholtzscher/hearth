//go:build integration

package hearthd //nolint:testpackage // Exercises private application assembly through real transports.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	natsgo "github.com/nats-io/nats.go"
	"github.com/pressly/goose/v3"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/agent"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This decorator loses only the first reply, after the real service commits.
// It supplies no catalog, admission, routing or persistence behavior.
type lostFunctionRegistrationReply struct {
	service   *adapters.FunctionService
	committed chan adapters.CatalogID
	release   chan struct{}
	mu        sync.Mutex
	ids       []adapters.CatalogID
}

func (registrar *lostFunctionRegistrationReply) RegisterCatalog(ctx context.Context, catalog adapters.Catalog) error {
	if err := registrar.service.RegisterCatalog(ctx, catalog); err != nil {
		return err
	}
	registrar.mu.Lock()
	registrar.ids = append(registrar.ids, catalog.ID)
	first := len(registrar.ids) == 1
	registrar.mu.Unlock()
	if first {
		registrar.committed <- catalog.ID
	}
	// Also hold any retry's reply so a slow test cannot race the SDK's one-second
	// attempt deadline and observe Start returning before the assertion.
	<-registrar.release
	if first {
		return errors.New("injected lost registration acknowledgement")
	}
	return nil
}

type functionIntegrationFixture struct {
	database  *sql.DB
	service   *adapters.FunctionService
	session   *adapter.Session
	handler   http.Handler
	tools     map[string]tool.InvokableTool
	registrar *lostFunctionRegistrationReply
}

func newFunctionIntegrationFixture(t *testing.T) functionIntegrationFixture {
	t.Helper()
	broker := startLifecycleNATSServer(t)
	connection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	validator := mustNotificationValidator(t)
	database := openPreFunctionUpgradeDatabase(t)
	runtimes := adapterServiceForTest(database)
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtimes, runtimes, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Close(context.Background()) })
	service, err := adapters.NewFunctionService(
		t.Context(),
		adapterssqlite.NewAdapterRepository(database),
		adaptersnats.NewFunctionInvoker(connection, validator),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Drain(context.Background()) })
	registrar := &lostFunctionRegistrationReply{
		service:   service,
		committed: make(chan adapters.CatalogID, 1),
		release:   make(chan struct{}),
	}
	registrations, err := adaptersnats.StartFunctionRegistrationServer(
		connection,
		validator,
		registrar,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Close(context.Background()) })
	devices, automations, notifications := &stubDevices{}, &stubAutomations{}, &stubNotifications{}
	mcpServer := newMCPServer(devices, automations, notifications, runtimes, service, nil)
	bridgeContext, stopBridge := context.WithCancel(t.Context())
	t.Cleanup(stopBridge)
	bridged, err := agent.MCPTools(bridgeContext, mcpServer)
	if err != nil {
		t.Fatal(err)
	}
	tools := make(map[string]tool.InvokableTool)
	for _, candidate := range bridged {
		info, infoErr := candidate.Info(t.Context())
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		invokable, ok := candidate.(tool.InvokableTool)
		if !ok {
			t.Fatalf("tool %s is not invokable", info.Name)
		}
		tools[info.Name] = invokable
	}
	handler, _ := newHTTPHandler(
		nil,
		devices,
		automations,
		notifications,
		runtimes,
		service,
		&stubAgent{},
		&testReadiness{},
		devices,
		automations,
		service,
		mcpServer,
	)
	session, err := adapter.Connect(
		t.Context(),
		adapter.Config{
			AdapterID:       "function-only",
			SoftwareName:    "controlled-functions",
			SoftwareVersion: "1",
			NATSURL:         broker.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return functionIntegrationFixture{
		database:  database,
		service:   service,
		session:   session,
		handler:   handler,
		tools:     tools,
		registrar: registrar,
	}
}

func openPreFunctionUpgradeDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("../../platform/db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(t.Context(), 15); err != nil {
		t.Fatal(err)
	}
	insertAgentConversationAt(
		t,
		database,
		"aconv_before_functions",
		time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	if err = platformdb.Migrate(t.Context(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

func runFunctionBridge(t *testing.T, tools map[string]tool.InvokableTool, name, arguments string) string {
	t.Helper()
	selected, ok := tools[name]
	if !ok {
		t.Fatalf("fixed tool %s absent before catalog registration", name)
	}
	output, err := selected.InvokableRun(t.Context(), arguments)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !json.Valid([]byte(output)) {
		t.Fatalf("%s did not return successful JSON evidence: %s", name, output)
	}
	return output
}
