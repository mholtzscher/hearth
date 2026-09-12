package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// startOutageNATSServer starts one JetStream server on an explicit loopback
// port and store directory, so the test can restart the same broker and the
// running Core reconnects to the durable JetStream state it already provisions.
func startOutageNATSServer(t *testing.T, port int, storeDir string) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: storeDir, NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func outageServerPort(t *testing.T, server *natsserver.Server) int {
	t.Helper()
	address, ok := server.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("NATS server address = %#v", server.Addr())
	}
	return address.Port
}

// TestCoreDoesNotPublishFactsForCommandsCommittedDuringAFactOutage protects the
// no-catch-up contract for a running Core: a durable Command transition
// committed while the NATS connection the fact dispatcher publishes through is
// unavailable enters history but emits no Device Fact after the connection
// recovers, while the next live transition emits exactly one. The epoch fence,
// the dedicated no-buffer connection and the dispatcher are all the assembly's
// own, so a missing track, gate or epoch reset fails here.
func TestCoreDoesNotPublishFactsForCommandsCommittedDuringAFactOutage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	_, powerEntityID, _ := seedDeviceFactsRegistration(ctx, t, databasePath)
	storeDir := t.TempDir()
	server := startOutageNATSServer(t, -1, storeDir)
	port := outageServerPort(t, server)

	// A fast reconnect keeps the external subscriber subscribed across the
	// broker restart, so a fact the dispatcher wrongly published during or just
	// after the outage would still be observed.
	subscriber := newDeviceFactSubscriber(
		t, server.ClientURL(), natsgo.MaxReconnects(-1), natsgo.ReconnectWait(5*time.Millisecond),
	)
	httpAddress, stopCore, runErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), databasePath)
	defer stopCore()
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	waitForCoreReady(ctx, t, httpAddress, runErrors)
	// Disabling the Entity makes every Command it receives an immediate durable
	// terminal transition, so a Command can commit during the outage without any
	// NATS round trip of its own.
	disableEntityOverHTTP(ctx, t, httpAddress, powerEntityID)
	commandDatabase, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = commandDatabase.Close() }()

	// The outage: the broker Core and the dedicated fact connection share
	// disappears while Core keeps running.
	server.Shutdown()
	server.WaitForShutdown()
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		return !coreReady(ctx, httpAddress), nil
	})

	// One durable Command transition committed during the outage.
	outageCommandID := postDisabledEntityCommand(ctx, t, httpAddress, powerEntityID)
	waitForCommittedCommandStatus(
		ctx, t, commandDatabase, outageCommandID, devices.CommandStatusEntityDisabled,
	)

	// Recovery: the same broker returns with its JetStream store, and both Core
	// connections and the subscriber reconnect to it.
	restored := startOutageNATSServer(t, port, storeDir)
	defer func() {
		restored.Shutdown()
		restored.WaitForShutdown()
	}()
	waitForMatrixCondition(t, 30*time.Second, func() (bool, error) {
		return subscriber.connection.IsConnected(), nil
	})
	waitForCoreReady(ctx, t, httpAddress, runErrors)

	// The transition committed during the outage is durable history only: no
	// fact is published for it after recovery.
	subscriber.assertNone(t)

	// A transition committed inside the recovered live window still publishes
	// exactly one fact, so suppression is freshness, not a broken fact path.
	liveCommandID := postDisabledEntityCommand(ctx, t, httpAddress, powerEntityID)
	if liveCommandID == outageCommandID {
		t.Fatalf("the live Command reused the outage Command identity %q", outageCommandID)
	}
	waitForCommittedCommandStatus(
		ctx, t, commandDatabase, liveCommandID, devices.CommandStatusEntityDisabled,
	)
	live := subscriber.next(t)
	if live.route.Family != natswire.DeviceFactFamilyCommand ||
		live.route.Variant != string(devices.CommandStatusEntityDisabled) {
		t.Fatalf("live fact route = %#v", live.route)
	}
	if live.envelope.CausationID != liveCommandID {
		t.Fatalf("live fact causation = %q, want %q", live.envelope.CausationID, liveCommandID)
	}
	subscriber.assertNone(t)

	stopDeviceFactsCore(t, stopCore, runErrors)
}

// coreReady reports whether /readyz currently accepts traffic.
func coreReady(ctx context.Context, httpAddress string) bool {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://"+httpAddress+"/readyz", nil,
	)
	if err != nil {
		return false
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

// disableEntityOverHTTP disables one Entity through the Core API.
func disableEntityOverHTTP(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	entityID devices.EntityID,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPatch,
		"http://"+httpAddress+"/v1/entities/"+string(entityID),
		strings.NewReader(`{"enabled":false}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable Entity = %d: %s", response.StatusCode, body)
	}
}

// postDisabledEntityCommand posts one Command to a disabled Entity and returns
// the Command identity the API reports for its committed terminal transition.
func postDisabledEntityCommand(
	ctx context.Context,
	t *testing.T,
	httpAddress string,
	entityID devices.EntityID,
) string {
	t.Helper()
	status, body, err := postFactsCommand(ctx, httpAddress, string(entityID), true)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusConflict {
		t.Fatalf("disabled Entity command = %d: %s", status, body)
	}
	var problem struct {
		Code      string `json:"code"`
		CommandID string `json:"command_id"`
	}
	if unmarshalErr := json.Unmarshal(body, &problem); unmarshalErr != nil {
		t.Fatalf("command problem body %s: %v", body, unmarshalErr)
	}
	if problem.Code != "entity_disabled" || problem.CommandID == "" {
		t.Fatalf("command problem body = %s", body)
	}
	return problem.CommandID
}

// waitForCommittedCommandStatus waits until one Command is durably stored with
// the supplied status, which proves the transition committed.
func waitForCommittedCommandStatus(
	ctx context.Context,
	t *testing.T,
	database *sql.DB,
	commandID string,
	status devices.CommandStatus,
) {
	t.Helper()
	waitForMatrixCondition(t, 15*time.Second, func() (bool, error) {
		var stored string
		queryErr := database.QueryRowContext(
			ctx, `SELECT status FROM commands WHERE id = ?`, commandID,
		).Scan(&stored)
		if queryErr == sql.ErrNoRows {
			return false, nil
		}
		if queryErr != nil {
			return false, queryErr
		}
		return stored == string(status), nil
	})
}
