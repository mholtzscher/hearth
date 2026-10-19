package hearthd //nolint:testpackage // Exercises private application assembly through real transports.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	natsgo "github.com/nats-io/nats.go"
	"github.com/pressly/goose/v3"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/api"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/agent"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const functionExactObject = `{"sequence":9007199254740993}`

func exactIntegrationFunction(handler adapter.FunctionHandler) adapter.Function {
	schema := json.RawMessage(
		`{"type":"object","properties":{"sequence":{"type":"integer","const":9007199254740993}},"required":["sequence"],"additionalProperties":false}`,
	)
	return adapter.Function{
		Descriptor: adapter.FunctionDescriptor{Key: "inspect", Description: "Return the supplied sequence",
			InputSchema: schema, OutputSchema: schema, TimeoutMS: 60000},
		Handler: handler,
	}
}

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

// A closed completion signal is reusable by readiness, assertions and cleanup;
// err is written before close and read only after observing completion.
type functionCoreRun struct {
	address string
	stop    context.CancelFunc
	done    chan struct{}
	err     error
}

func startFunctionCore(t *testing.T, config Config) *functionCoreRun {
	t.Helper()
	address, options := reserveLoopbackListener(t)
	config.HTTPAddr = address
	ctx, stop := context.WithCancel(t.Context())
	run := &functionCoreRun{address: address, stop: stop, done: make(chan struct{})}
	go func() {
		run.err = runWithOptions(ctx, config, slog.New(slog.DiscardHandler), options)
		close(run.done)
	}()
	t.Cleanup(func() { stop(); run.join(t) })
	client := newNonPoolingHTTPClient(t)
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		select {
		case <-run.done:
			return false, errors.Join(errors.New("Core exited before readiness"), run.err)
		default:
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+"/readyz", nil)
		if err != nil {
			return false, err
		}
		response, err := client.Do(request)
		if err != nil {
			return false, nil
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode == http.StatusOK, nil
	})
	return run
}

func (run *functionCoreRun) join(t *testing.T) {
	t.Helper()
	select {
	case <-run.done:
		if run.err != nil {
			t.Errorf("Core shutdown: %v", run.err)
		}
	case <-time.After(20 * time.Second):
		t.Error("Core did not join admitted function work")
	}
}

func functionNetworkRequest(
	ctx context.Context,
	client *http.Client,
	address, method, path, body string,
) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	return response.StatusCode, raw, err
}

func successfulFunctionCoreRequest(
	t *testing.T,
	client *http.Client,
	run *functionCoreRun,
	method, path, body string,
) []byte {
	t.Helper()
	status, raw, err := functionNetworkRequest(t.Context(), client, run.address, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: %d %s %v", method, path, status, raw, err)
	}
	return raw
}

func assertExactFunctionInvocation(t *testing.T, invocation adaptersapi.InvocationBody) {
	t.Helper()
	if invocation.ID == "" || invocation.Status != "succeeded" || invocation.CompletedAt == nil ||
		string(invocation.Arguments) != functionExactObject || string(invocation.Result) != functionExactObject {
		t.Fatalf("exact retained successful invocation: %+v", invocation)
	}
}

func seedInterruptedFunctionInvocation(t *testing.T, path string) adapters.InvocationID {
	t.Helper()
	database, err := platformdb.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	repository := adapterssqlite.NewAdapterRepository(database)
	function, err := repository.GetFunction(t.Context(), "restart-functions", "inspect")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := adapters.PrepareInvocation(
		function,
		adapters.InvokeInput{
			AdapterID:       function.AdapterID,
			FunctionKey:     function.Descriptor.Key,
			CatalogRevision: function.CatalogRevision,
			Arguments:       json.RawMessage(functionExactObject),
		},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := repository.Admit(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	return invocation.ID
}
