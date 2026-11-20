//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core and the HomeKit app over public transports.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	homekitbridge "github.com/mholtzscher/hearth/internal/adapters/homekit"
	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	homekitapp "github.com/mholtzscher/hearth/internal/app/homekit"
	adaptersapi "github.com/mholtzscher/hearth/internal/modules/adapters/httpapi"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const homekitJourneyAdapter = "homekit-journey"

type homekitJourneyAccessory struct {
	DeviceID string `json:"device_id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	AID      uint64 `json:"aid"`
	Entities []struct {
		EntityID string `json:"entity_id"`
		Role     string `json:"role"`
	} `json:"entities"`
}

type homekitJourneyList struct {
	Paired         bool                      `json:"paired"`
	AccessoryCount int                       `json:"accessory_count"`
	AccessoryLimit int                       `json:"accessory_limit"`
	Accessories    []homekitJourneyAccessory `json:"accessories"`
	Candidates     []struct {
		DeviceID string `json:"device_id"`
	} `json:"candidates"`
}

// This uses only a synthetic SDK light. HAP runs normally with loopback mDNS
// and a private store; no controller is paired and no household resource is used.
func TestHomeKitFunctionsThroughProductionCore(t *testing.T) {
	t.Parallel()
	broker := startLifecycleNATSServer(t)
	core := startFunctionCore(t, Config{
		HouseholdTimezone: "UTC", NATSURL: broker.ClientURL(),
		SQLitePath: dbtest.MigratedPath(t), Agent: requiredAgentConfig(t),
	})
	client := newNonPoolingHTTPClient(t)

	session, err := adapter.Connect(t.Context(), adapter.Config{
		AdapterID: "homekit-synthetic-light", SoftwareName: "synthetic-homekit-light",
		SoftwareVersion: "1", NATSURL: broker.ClientURL(), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	light, err := scripted.New(session, []scripted.DeviceSpec{{
		BindingKey: "light", Name: "Synthetic light", Kind: "light",
		Entities: []scripted.EntitySpec{{
			Key: "power", Name: "Power", Type: "hearth.power/v1",
			Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}}},
			Initial: false,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := session.Register(t.Context(), light.Registrations()[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = light.Attach([]adapter.Binding{binding}); err != nil {
		t.Fatal(err)
	}
	if err = light.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	powerID := string(bindingEntityID(t, binding, "power"))
	assertPagedHomeKitInventory(t, session, core, powerID)

	_, portText, err := net.SplitHostPort(freeSimulatorControlAddr(t))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	name := "Hearth E2E " + portText
	natsURL := broker.ClientURL()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	dataDir := filepath.Join(t.TempDir(), "bridge")
	finished := make(chan struct{})
	var runErr error
	go func() {
		runErr = homekitapp.Run(ctx, homekitapp.Config{
			AdapterID: homekitJourneyAdapter, NATSURL: &natsURL, CoreURL: "http://" + core.address,
			DataDir: dataDir, BridgeName: &name, ListenPort: &port, Interfaces: []string{"lo"},
		}, logger)
		close(finished)
	}()
	stopped := false
	completed := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-finished:
			completed = true
			if runErr != nil {
				t.Errorf("HomeKit app exited with an error: %v", runErr)
			}
		case <-time.After(20 * time.Second):
			t.Error("HomeKit app did not stop")
		}
	}
	t.Cleanup(stop)

	revision := homekitJourneyCatalog(t, client, core, finished, &runErr)
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		raw := homekitJourneyRequest(t, client, core, http.MethodGet, "/v1/adapters/"+homekitJourneyAdapter, "")
		var status adaptersapi.AdapterBody
		homekitJourneyDecode(t, raw, &status)
		return status.Health.Status == "healthy", nil
	})
	info := assertHomeKitInventoryJourney(t, client, core, revision, binding, powerID)
	assertHomeKitResetAdmission(t, client, core, revision)
	reset := homekitJourneyInvoke(t, client, core, revision, "reset_pairing",
		`{"confirm":"reset_all_homekit_pairings"}`, "succeeded", "")
	var replacement homekitJourneyPairing
	homekitJourneyDecode(t, reset.Result, &replacement)
	if replacement.Paired || replacement.SetupURI == "" || replacement.SetupURI == info.SetupURI {
		t.Fatal("reset_pairing did not rotate the unpaired setup identity")
	}
	assertHomeKitExposureSurvivedReset(t, client, core, revision, binding.DeviceID)
	assertHomeKitListThroughMCP(t, core, revision, binding.DeviceID)
	assertHomeKitReconcilesAfterFactConsumerLoss(t, client, core, broker.ClientURL(), revision, powerID)
	stop()
	if completed && (hasHomeKitCredential(logs.String(), info) || hasHomeKitCredential(logs.String(), replacement)) {
		t.Error("HomeKit process logs contain a pairing credential")
	}
}

func assertHomeKitReconcilesAfterFactConsumerLoss(
	t *testing.T, client *http.Client, core *functionCoreRun, natsURL, revision, powerID string,
) {
	t.Helper()
	conn, err := natsgo.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(t.Context(), devicesnats.DeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	consumers := stream.ListConsumers(t.Context())
	var homekitConsumer string
	for info := range consumers.Info() {
		if info.Config.FilterSubject == "hearth.v1.core.fact.entity.*.observation.*" {
			homekitConsumer = info.Name
		}
	}
	if err = consumers.Err(); err != nil || homekitConsumer == "" {
		t.Fatalf("find HomeKit's Observation consumer: %v", err)
	}
	before := homekitJourneyInvoke(t, client, core, revision, "list_accessories", `{}`, "succeeded", "")
	var previous homekitJourneyList
	homekitJourneyDecode(t, before.Result, &previous)
	if len(previous.Accessories) != 1 || previous.Accessories[0].Status != "reachable" {
		t.Fatal("synthetic light must be reachable before disabling it")
	}
	disableHomeKitFixtureEntity(t, core, client, powerID)
	if err = stream.DeleteConsumer(t.Context(), homekitConsumer); err != nil {
		t.Fatal(err)
	}
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		raw := homekitJourneyRequest(t, client, core, http.MethodPost,
			"/v1/adapters/"+homekitJourneyAdapter+"/functions/list_accessories/invocations",
			`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":{}}`)
		var invocation adaptersapi.InvocationBody
		homekitJourneyDecode(t, raw, &invocation)
		if invocation.Status != "succeeded" {
			return false, fmt.Errorf("list_accessories after Fact consumer loss: %s", invocation.Status)
		}
		var list homekitJourneyList
		homekitJourneyDecode(t, invocation.Result, &list)
		return len(list.Accessories) == 1 && list.Accessories[0].Status == "unreachable", nil
	})
}

func disableHomeKitFixtureEntity(t *testing.T, core *functionCoreRun, client *http.Client, id string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, "http://"+core.address+"/v1/entities/"+id,
		strings.NewReader(`{"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/merge-patch+json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable synthetic Entity: HTTP %d", resp.StatusCode)
	}
}

func assertPagedHomeKitInventory(t *testing.T, session *adapter.Session, core *functionCoreRun, powerID string) {
	t.Helper()
	// These synthetic pressure sensors fill HTTP pages without adding a mapped role.
	for device := range 4 {
		reg, err := scripted.New(session, []scripted.DeviceSpec{{
			BindingKey: fmt.Sprintf("page-%d", device), Name: "Inventory fixture", Kind: "sensor",
			Entities: homekitPaginationEntities(),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = session.Register(t.Context(), reg.Registrations()[0]); err != nil {
			t.Fatal(err)
		}
	}
	client, err := homekitbridge.NewCoreClient("http://" + core.address)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := client.ReadInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Entities) != 201 || inventory.Entities[powerID].Type != "hearth.power/v1" {
		t.Fatalf("bridge did not read all 201 production Core Entities: got %d", len(inventory.Entities))
	}
}

func homekitPaginationEntities() []scripted.EntitySpec {
	entries := make([]scripted.EntitySpec, 0, 50)
	for id := range cap(entries) {
		entries = append(entries, scripted.EntitySpec{
			Key: fmt.Sprintf("pressure-%d", id), Name: "Pressure", Type: "hearth.pressure/v1", Initial: 1013,
			Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{}},
		})
	}
	return entries
}

type homekitJourneyPairing struct {
	Paired    bool   `json:"paired"`
	SetupCode string `json:"setup_code"`
	SetupURI  string `json:"setup_uri"`
}

func hasHomeKitCredential(logs string, pairing homekitJourneyPairing) bool {
	return strings.Contains(logs, pairing.SetupCode) ||
		strings.Contains(logs, strings.ReplaceAll(pairing.SetupCode, "-", "")) ||
		strings.Contains(logs, pairing.SetupURI)
}

func assertHomeKitExposureSurvivedReset(
	t *testing.T, client *http.Client, core *functionCoreRun, revision, deviceID string,
) {
	t.Helper()
	after := homekitJourneyInvoke(t, client, core, revision, "list_accessories", `{}`, "succeeded", "")
	var list homekitJourneyList
	homekitJourneyDecode(t, after.Result, &list)
	if list.AccessoryCount != 1 || len(list.Accessories) != 1 || list.Accessories[0].DeviceID != deviceID ||
		list.Accessories[0].AID != 2 {
		t.Fatal("reset_pairing did not preserve the exposure and its AID")
	}
}

func assertHomeKitListThroughMCP(t *testing.T, core *functionCoreRun, revision, deviceID string) {
	t.Helper()
	client := connectAutomationFunctionsMCP(t, core)
	raw := automationFunctionsMCP(t, client, "invoke_adapter_function",
		fmt.Sprintf(`{"adapter_id":%q,"function_key":"list_accessories","catalog_revision":%q,"arguments":{}}`,
			homekitJourneyAdapter, revision))
	var outcome struct {
		InvocationID string             `json:"invocation_id"`
		Status       string             `json:"status"`
		Result       homekitJourneyList `json:"result"`
	}
	homekitJourneyDecode(t, raw, &outcome)
	if outcome.Status != "succeeded" || outcome.InvocationID == "" || len(outcome.Result.Accessories) != 1 ||
		outcome.Result.Accessories[0].DeviceID != deviceID {
		t.Fatal("MCP Function invocation did not return the exposed Device")
	}
}

func assertHomeKitInventoryJourney(
	t *testing.T, client *http.Client, core *functionCoreRun, revision string, binding adapter.Binding, powerID string,
) homekitJourneyPairing {
	t.Helper()
	initial := homekitJourneyInvoke(t, client, core, revision, "list_accessories", `{}`, "succeeded", "")
	var list homekitJourneyList
	homekitJourneyDecode(t, initial.Result, &list)
	if list.Paired || list.AccessoryCount != 0 || list.AccessoryLimit != 149 || len(list.Accessories) != 0 ||
		!list.hasCandidate(binding.DeviceID) {
		t.Fatal("initial list did not contain the unexposed synthetic light")
	}

	pairing := homekitJourneyInvoke(t, client, core, revision, "pairing_info", `{}`, "succeeded", "")
	var info homekitJourneyPairing
	homekitJourneyDecode(t, pairing.Result, &info)
	if info.Paired || !regexp.MustCompile(`^\d{3}-\d{2}-\d{3}$`).MatchString(info.SetupCode) ||
		!strings.HasPrefix(info.SetupURI, "X-HM://") {
		t.Fatal("unpaired pairing_info did not return a valid setup code and URI")
	}

	args := fmt.Sprintf(`{"device_id":%q}`, binding.DeviceID)
	incompatible := fmt.Sprintf(`{"device_id":%q,"kind":"lock"}`, binding.DeviceID)
	homekitJourneyInvoke(t, client, core, revision, "expose_device", incompatible, "not_executed", "kind_incompatible")
	first := homekitJourneyInvoke(t, client, core, revision, "expose_device", args, "succeeded", "")
	var accessory homekitJourneyAccessory
	homekitJourneyDecode(t, first.Result, &accessory)
	if accessory.DeviceID != binding.DeviceID || accessory.Kind != "lightbulb" || accessory.AID < 2 ||
		len(
			accessory.Entities,
		) != 1 || accessory.Entities[0].Role != "on" || accessory.Entities[0].EntityID != powerID {
		t.Fatal("expose_device did not return the expected synthetic light allocation")
	}
	firstAID := accessory.AID
	listing := homekitJourneyInvoke(t, client, core, revision, "list_accessories", `{}`, "succeeded", "")
	homekitJourneyDecode(t, listing.Result, &list)
	if list.AccessoryCount != 1 || len(list.Accessories) != 1 || list.Accessories[0].AID != firstAID {
		t.Fatal("list_accessories did not retain the exposed accessory")
	}
	removed := homekitJourneyInvoke(t, client, core, revision, "unexpose_device", args, "succeeded", "")
	var removal struct {
		DeviceID string `json:"device_id"`
		Status   string `json:"status"`
	}
	homekitJourneyDecode(t, removed.Result, &removal)
	if removal.DeviceID != binding.DeviceID || removal.Status != "unexposed" {
		t.Fatal("unexpose_device did not remove the synthetic light")
	}
	afterRemoval := homekitJourneyInvoke(t, client, core, revision, "list_accessories", `{}`, "succeeded", "")
	homekitJourneyDecode(t, afterRemoval.Result, &list)
	if list.AccessoryCount != 0 || len(list.Accessories) != 0 || !list.hasCandidate(binding.DeviceID) {
		t.Fatal("unexposed light did not return to the candidate list")
	}
	again := homekitJourneyInvoke(t, client, core, revision, "expose_device", args, "succeeded", "")
	homekitJourneyDecode(t, again.Result, &accessory)
	if accessory.DeviceID != binding.DeviceID || accessory.AID != firstAID {
		t.Fatal("re-exposing the light did not restore its AID")
	}
	return info
}

func (list homekitJourneyList) hasCandidate(id string) bool {
	for _, candidate := range list.Candidates {
		if candidate.DeviceID == id {
			return true
		}
	}
	return false
}

func assertHomeKitResetAdmission(t *testing.T, client *http.Client, core *functionCoreRun, revision string) {
	t.Helper()
	for _, invalid := range []string{`{}`, `{"confirm":"wrong"}`} {
		status, _, requestErr := functionNetworkRequest(t.Context(), client, core.address, http.MethodPost,
			"/v1/adapters/"+homekitJourneyAdapter+"/functions/reset_pairing/invocations",
			`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":`+invalid+`}`)
		if requestErr != nil || status != http.StatusUnprocessableEntity {
			t.Fatalf("invalid reset_pairing confirmation: HTTP %d, error %v", status, requestErr)
		}
	}
	// Read history without printing it: pairing_info intentionally retains secrets.
	history := homekitJourneyRequest(t, client, core, http.MethodGet,
		"/v1/adapters/"+homekitJourneyAdapter+"/function-invocations?function_key=reset_pairing&limit=50", "")
	var resets adaptersapi.InvocationCollectionBody
	homekitJourneyDecode(t, history, &resets)
	if len(resets.Items) != 0 {
		t.Fatal("invalid reset_pairing confirmation was admitted to invocation history")
	}
}

func homekitJourneyCatalog(
	t *testing.T, client *http.Client, core *functionCoreRun, finished <-chan struct{}, runErr *error,
) string {
	t.Helper()
	var revision string
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		select {
		case <-finished:
			if *runErr == nil {
				return false, errors.New("HomeKit exited before catalog registration")
			}
			return false, fmt.Errorf("HomeKit exited before catalog registration: %w", *runErr)
		default:
		}
		status, raw, err := functionNetworkRequest(t.Context(), client, core.address, http.MethodGet,
			"/v1/adapters/"+homekitJourneyAdapter+"/functions?limit=50", "")
		if err != nil {
			return false, err
		}
		if status != http.StatusOK {
			return false, nil
		}
		var page adaptersapi.FunctionCollectionBody
		if err = json.Unmarshal(raw, &page); err != nil {
			return false, err
		}
		revision = completeHomeKitCatalog(page)
		return revision != "", nil
	})
	return revision
}

func completeHomeKitCatalog(page adaptersapi.FunctionCollectionBody) string {
	expected := map[string]bool{
		"list_accessories": true, "expose_device": true, "unexpose_device": true,
		"pairing_info": true, "reset_pairing": true,
	}
	if len(page.Items) != len(expected) {
		return ""
	}
	revision := page.Items[0].CatalogRevision
	for _, item := range page.Items {
		if !expected[item.FunctionKey] || !item.Callable || item.CatalogRevision != revision {
			return ""
		}
		delete(expected, item.FunctionKey)
	}
	return revision
}

func homekitJourneyInvoke(
	t *testing.T, client *http.Client, core *functionCoreRun, revision, key, args, status, reason string,
) adaptersapi.InvocationBody {
	t.Helper()
	raw := homekitJourneyRequest(t, client, core, http.MethodPost,
		"/v1/adapters/"+homekitJourneyAdapter+"/functions/"+key+"/invocations",
		`{"catalog_revision":`+strconv.Quote(revision)+`,"arguments":`+args+`}`)
	var invocation adaptersapi.InvocationBody
	homekitJourneyDecode(t, raw, &invocation)
	if invocation.ID == "" || invocation.AdapterID != homekitJourneyAdapter || invocation.FunctionKey != key ||
		invocation.CatalogRevision != revision || invocation.Status != status || invocation.ReasonCode != reason ||
		invocation.CompletedAt == nil {
		t.Fatalf(
			"%s invocation outcome = %s/%s, want %s/%s",
			key,
			invocation.Status,
			invocation.ReasonCode,
			status,
			reason,
		)
	}
	var retained adaptersapi.InvocationBody
	homekitJourneyDecode(t, homekitJourneyRequest(t, client, core, http.MethodGet,
		"/v1/adapter-function-invocations/"+invocation.ID, ""), &retained)
	if !reflect.DeepEqual(invocation, retained) {
		t.Fatalf("%s invocation differs from retained history", key)
	}
	var page adaptersapi.InvocationCollectionBody
	homekitJourneyDecode(t, homekitJourneyRequest(t, client, core, http.MethodGet,
		"/v1/adapters/"+homekitJourneyAdapter+"/function-invocations?function_key="+key+"&limit=50", ""), &page)
	found := false
	for _, entry := range page.Items {
		if entry.ID == invocation.ID && reflect.DeepEqual(entry, invocation) {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s invocation missing from retained history collection", key)
	}
	return invocation
}

func homekitJourneyRequest(t *testing.T, client *http.Client, core *functionCoreRun, method, path, body string) []byte {
	t.Helper()
	status, raw, err := functionNetworkRequest(t.Context(), client, core.address, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: HTTP %d, error %v", method, path, status, err)
	}
	return raw
}

func homekitJourneyDecode(t *testing.T, raw []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatalf("invalid HomeKit journey response JSON: %v", err)
	}
}
