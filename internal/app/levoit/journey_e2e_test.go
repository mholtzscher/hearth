//go:build e2e

package levoit //nolint:testpackage // Exercises production assembly with an injected cloud transport.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/app/hearthd"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

// The synthetic cloud implements the pinned packet contract, not a real device.
type cloudFixture struct {
	mu          sync.Mutex
	state       map[string]map[string]any
	renamed     bool
	mutations   int
	statusReads int
}

func newCloudFixture() *cloudFixture {
	fixture := &cloudFixture{state: make(map[string]map[string]any)}
	for _, cid := range []string{"Core400S", "Core600S"} {
		fixture.state[cid] = map[string]any{
			"enabled":  false,
			"mode":     "manual",
			"level":    1,
			"levelNew": 1,
			"configuration": map[string]any{
				"display": true,
			},
			"child_lock":        false,
			"air_quality_value": 7.5,
			"filter_life":       80,
		}
	}
	return fixture
}

func (f *cloudFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body struct {
		CID     string `json:"cid"`
		Payload struct {
			Method string         `json:"method"`
			Data   map[string]any `json:"data"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		return nil, err
	}
	var result any
	switch request.URL.Path {
	case "/globalPlatform/api/accountAuth/v1/authByPWDOrOTM":
		result = map[string]any{"accountID": "synthetic", "authorizeCode": "synthetic-code"}
	case "/user/api/accountManage/v1/loginByAuthorizeCode4Vesync":
		result = map[string]any{"accountID": "synthetic", "token": "synthetic-token", "countryCode": "US"}
	case "/cloud/v1/deviceManaged/devices":
		var list []map[string]any
		for _, model := range []string{"Core400S", "Core600S"} {
			name := model
			if f.renamed {
				name += " renamed"
			}
			list = append(list, map[string]any{"cid": model, "deviceType": model, "deviceName": name,
				"configModule": "synthetic-module", "deviceRegion": "US", "connectionStatus": "online"})
		}
		result = map[string]any{"pageNo": 1, "total": 2, "list": list}
	case "/cloud/v2/deviceManaged/bypassV2":
		state := f.state[body.CID]
		if state == nil {
			return nil, fmt.Errorf("unexpected synthetic CID")
		}
		if body.Payload.Method == "getPurifierStatus" {
			f.statusReads++
			result = map[string]any{"code": 0, "result": state}
		} else {
			f.mutations++
			// Deliberately retain a stale cloud snapshot after accepted controls.
			// This is the live-observed contract; the adapter must not read it to
			// confirm a Command or rely on immediate cloud mutation in this fixture.
			switch body.Payload.Method {
			case "setSwitch", "setPurifierMode", "setLevel", "setDisplay", "setChildLock":
			default:
				return nil, fmt.Errorf("unexpected synthetic mutation")
			}
			result = map[string]any{"code": 0}
		}
	default:
		return nil, fmt.Errorf("unexpected synthetic path")
	}
	data, err := json.Marshal(map[string]any{"code": 0, "result": result})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}, nil
}

func TestLevoitCompanionCoreJourney(t *testing.T) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "synthetic-unused-model-key")
	base, natsURL := startCore(t)
	credentials := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(
		credentials,
		[]byte(`{"email":"synthetic@example.com","password":"synthetic-password"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	config := Config{AdapterID: "levoit-home", NATSURL: natsURL, CredentialsFile: credentials}
	cloud := newCloudFixture()
	stop, join := startCompanion(t, config, cloud)
	entities := waitEntities(t, base)
	assertPurifierDevices(t, base)
	for _, entity := range entities {
		if entity.Name == "PM2.5" && entity.State.Value != 7.5 {
			t.Fatal("PM2.5 concentration changed")
		}
		if entity.Name == "Filter life" && entity.State.Value != float64(80) {
			t.Fatal("filter life changed")
		}
	}
	powerID, commandID := assertControls(t, base, entities)
	stop()
	join()
	cloud.mu.Lock()
	reads, writes := cloud.statusReads, cloud.mutations
	cloud.mu.Unlock()
	if reads != 2 || writes != 10 {
		t.Fatal("Commands read status or repeated mutations")
	}
	assertSensorStateUntouched(t, base, entities)
	var original devicesapi.EntityBody
	for _, entity := range entities {
		if entity.Name == "Filter life" {
			original = entity
			break
		}
	}
	var patched devicesapi.EntityBody
	coreJSON(
		t,
		base,
		http.MethodPatch,
		"/v1/entities/"+original.ID,
		`{"enabled":false,"name_override":"My filter"}`,
		&patched,
	)
	cloud.mu.Lock()
	cloud.renamed = true
	mutations := cloud.mutations
	cloud.mu.Unlock()
	stopAgain, joinAgain := startCompanion(t, config, cloud)
	var after devicesapi.EntityBody
	waitCondition(t, func() bool {
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+original.ID, "", &after)
		var page struct {
			Items []devicesapi.DeviceBody `json:"items"`
		}
		coreJSON(t, base, http.MethodGet, "/v1/devices", "", &page)
		return len(page.Items) == 2 && strings.HasSuffix(page.Items[0].Name, "renamed") &&
			after.Availability.Status == "available"
	})
	if after.Enabled || after.Name != "My filter" || after.DeviceID != original.DeviceID || after.State == nil ||
		after.State.ObservationID != patched.State.ObservationID {
		t.Fatal("restart lost identity, override, enablement or disabled State")
	}
	cloud.mu.Lock()
	afterMutations := cloud.mutations
	cloud.mu.Unlock()
	if mutations != afterMutations {
		t.Fatal("restart replayed a mutation")
	}
	// The next acquisition rolls optimistic power back, without changing its
	// already-retained satisfied Command outcome or replaying any mutation.
	waitCondition(t, func() bool {
		var power devicesapi.EntityBody
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+powerID, "", &power)
		return power.State != nil && power.State.Value == false
	})
	var retained struct {
		Status string `json:"status"`
	}
	coreJSON(t, base, http.MethodGet, "/v1/commands/"+commandID, "", &retained)
	if retained.Status != "satisfied" {
		t.Fatal("stale cloud report rewrote retained Command outcome")
	}
	stopAgain()
	joinAgain()
}

func assertPurifierDevices(t *testing.T, base string) {
	t.Helper()
	var page struct {
		Items []devicesapi.DeviceBody `json:"items"`
	}
	coreJSON(t, base, http.MethodGet, "/v1/devices", "", &page)
	if len(page.Items) != 2 {
		t.Fatal("expected both purifier Devices")
	}
	for _, device := range page.Items {
		if device.Kind != "air-purifier" {
			t.Fatal("incorrect Device kind")
		}
	}
}

func assertControls(t *testing.T, base string, entities []devicesapi.EntityBody) (string, string) {
	t.Helper()
	var powerID, commandID string
	for _, entity := range entities {
		var value any
		switch entity.Name {
		case "Power", "Child lock":
			value = true
		case "Display":
			value = false
		case "Mode":
			value = "sleep"
		case "Manual fan level":
			value = "4"
		default:
			continue
		}
		data, err := json.Marshal(map[string]any{"operation": "set", "parameters": map[string]any{"value": value}})
		if err != nil {
			t.Fatal(err)
		}
		var result devicesapi.CommandResultBody
		coreJSON(t, base, http.MethodPost, "/v1/entities/"+entity.ID+"/commands", string(data), &result)
		if result.Status != "satisfied" || result.ObservationID == nil || result.Value == nil ||
			*result.Value != value {
			t.Fatalf("Command did not optimistically satisfy %s: %+v", entity.Name, result)
		}
		var retained struct {
			Status        string `json:"status"`
			ObservationID string `json:"outcome_observation_id"`
		}
		coreJSON(t, base, http.MethodGet, "/v1/commands/"+result.CommandID, "", &retained)
		if retained.Status != "satisfied" || retained.ObservationID != *result.ObservationID {
			t.Fatal("Command evidence was not retained")
		}
		var after devicesapi.EntityBody
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+entity.ID, "", &after)
		if after.State == nil || after.State.Value != value || after.State.ObservationID != *result.ObservationID {
			t.Fatal("canonical optimistic State disagrees with Command evidence")
		}
		if entity.Name == "Power" {
			powerID, commandID = entity.ID, result.CommandID
		}
	}
	return powerID, commandID
}

func assertSensorStateUntouched(t *testing.T, base string, entities []devicesapi.EntityBody) {
	t.Helper()
	for _, entity := range entities {
		if entity.Name != "PM2.5" && entity.Name != "Filter life" {
			continue
		}
		var after devicesapi.EntityBody
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+entity.ID, "", &after)
		if after.State == nil || after.State.ObservationID != entity.State.ObservationID ||
			after.Availability.Status != "available" {
			t.Fatal("optimistic controls changed sensor State or availability")
		}
	}
}

func startCore(t *testing.T) (string, string) {
	t.Helper()
	broker := natstest.StartServer(t)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	database := dbtest.MigratedPath(t)
	go func() {
		done <- hearthd.Run(ctx, hearthd.Config{HouseholdTimezone: "UTC", HTTPAddr: address,
			NATSURL: broker.ClientURL(), SQLitePath: database}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(20 * time.Second):
			t.Error("Core did not join")
		}
	})
	base := "http://" + address
	waitCondition(t, func() bool {
		status, _, requestErr := coreRequest(t.Context(), base, http.MethodGet, "/readyz", "")
		return requestErr == nil && status == http.StatusOK
	})
	return base, broker.ClientURL()
}

func startCompanion(t *testing.T, config Config, transport http.RoundTripper) (context.CancelFunc, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var result error
	go func() { result = run(ctx, config, slog.New(slog.DiscardHandler), transport); close(done) }()
	join := func() {
		t.Helper()
		select {
		case <-done:
			if result != nil {
				t.Error(result)
			}
		case <-time.After(10 * time.Second):
			t.Error("companion did not join")
		}
	}
	t.Cleanup(func() { cancel(); join() })
	return cancel, join
}

func waitCondition(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("Levoit journey condition timed out")
		}
	}
}

func coreRequest(ctx context.Context, base, method, path, body string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPatch {
		request.Header.Set("Content-Type", "application/merge-patch+json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func coreJSON(t *testing.T, base, method, path, body string, target any) {
	t.Helper()
	status, data, err := coreRequest(t.Context(), base, method, path, body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("%s %s: %d %s %v", method, path, status, data, err)
	}
	if err = json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func waitEntities(t *testing.T, base string) []devicesapi.EntityBody {
	t.Helper()
	var page struct {
		Items []devicesapi.EntityBody `json:"items"`
	}
	waitCondition(t, func() bool {
		coreJSON(t, base, http.MethodGet, "/v1/entities?units=canonical", "", &page)
		if len(page.Items) != 14 {
			return false
		}
		for _, entity := range page.Items {
			if entity.State == nil || entity.Availability.Status != "available" {
				return false
			}
		}
		return true
	})
	return page.Items
}
