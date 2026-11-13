//go:build e2e

package august //nolint:testpackage // Exercises production assembly using a synthetic cloud transport.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
)

const journeyID = "11111111111111111111111111111111"
const replacementID = "22222222222222222222222222222222"

// The synthetic peer is a scripted protocol fixture, not physical compatibility evidence.
type journeyCloud struct {
	mu               sync.Mutex
	id, name         string
	missing, offline bool
}

func (f *journeyCloud) RoundTrip(request *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if request.URL.Host != "api-production.august.com" {
		return nil, fmt.Errorf("unexpected synthetic origin")
	}
	var body string
	switch request.URL.Path {
	case "/session":
		body = `{"vPassword":true,"vInstallId":true,"expiresAt":"2099-01-01T00:00:00Z"}`
	case "/users/locks/mine":
		body = `{}`
		if !f.missing {
			body = fmt.Sprintf(`{%q:{"LockName":%q,"UserType":"superuser"}}`, f.id, f.name)
		}
	case "/locks/" + f.id:
		bridge := `{"operative":true,"status":{"current":"online"}}`
		if f.offline {
			bridge = `{"operative":false,"status":{"current":"offline"}}`
		}
		body = fmt.Sprintf(
			`{"LockID":%q,"LockName":%q,"skuNumber":"AUG-SL05-M01-G01","currentFirmwareVersion":"synthetic","battery":0.7157,"LockStatus":{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"},"Bridge":%s}`,
			f.id,
			f.name,
			bridge,
		)
	case "/locks/" + f.id + "/status":
		if f.offline {
			return nil, fmt.Errorf("offline status must not be requested")
		}
		body = `{"status":"locked","doorState":"closed","dateTime":"2020-01-01T00:00:00Z"}`
	default:
		return nil, fmt.Errorf("unexpected synthetic route")
	}
	header := make(http.Header)
	header.Set("X-August-Access-Token", "synthetic")
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

//nolint:gocognit,gocyclo,cyclop // One continuous restart and replacement journey.
func TestMonitoringCoreJourney(
	t *testing.T,
) {
	t.Setenv("HEARTH_OPENAI_API_KEY", "synthetic-unused-model-key")
	base, natsURL := startCore(t)
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"email":"synthetic@example.com","password":"synthetic"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	c := testConfig(path)
	c.NATSURL = natsURL
	c.Locks[0].LockID = journeyID
	cloud := &journeyCloud{id: journeyID, name: "Front"}
	stop, join := startCompanion(t, c, cloud)
	var page struct {
		Items []devicesapi.EntityBody `json:"items"`
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last page: %+v", page.Items)
		}
	})
	waitCondition(t, func() bool {
		coreJSON(t, base, http.MethodGet, "/v1/entities?units=canonical", "", &page)
		if len(page.Items) != 3 {
			return false
		}
		for _, entity := range page.Items {
			if entity.State == nil || entity.Availability.Status != "available" {
				return false
			}
		}
		return true
	})
	byName := map[string]devicesapi.EntityBody{}
	for _, e := range page.Items {
		byName[e.Name] = e
	}
	lock, door, battery := byName["Lock"], byName["Door open"], byName["Reported battery estimate"]
	if lock.ID == "" || door.ID == "" || battery.ID == "" || lock.DeviceID != door.DeviceID ||
		lock.DeviceID != battery.DeviceID ||
		lock.State.Value != "locked" ||
		door.State.Value != false ||
		battery.State.Value != 71.57 {
		t.Fatal("incorrect three-entity identity or state")
	}
	operations, ok := lock.Support["operations"].(map[string]any)
	if !ok || len(operations) != 0 {
		t.Fatal("monitoring exposed set")
	}
	var history struct {
		Items []struct {
			ID string `json:"observation_id"`
		} `json:"items"`
	}
	waitCondition(t, func() bool {
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+battery.ID+"/state/history?disposition=all", "", &history)
		for _, item := range history.Items {
			if item.ID == battery.State.ObservationID {
				return true
			}
		}
		return false
	})
	var patched devicesapi.EntityBody
	coreJSON(
		t,
		base,
		http.MethodPatch,
		"/v1/entities/"+battery.ID,
		`{"enabled":false,"name_override":"My battery"}`,
		&patched,
	)
	stop()
	join()
	cloud.mu.Lock()
	cloud.name = "Renamed"
	cloud.mu.Unlock()
	stop, join = startCompanion(t, c, cloud)
	var after devicesapi.EntityBody
	waitCondition(t, func() bool {
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+battery.ID, "", &after)
		var devices struct {
			Items []devicesapi.DeviceBody `json:"items"`
		}
		coreJSON(t, base, http.MethodGet, "/v1/devices", "", &devices)
		return len(devices.Items) == 1 && devices.Items[0].Name == "Renamed" && after.Availability.Status == "available"
	})
	if after.Enabled || after.Name != "My battery" || after.DeviceID != battery.DeviceID || after.State == nil ||
		after.State.ObservationID != patched.State.ObservationID {
		t.Fatal("restart lost retained identity, override, enablement or state")
	}
	stop()
	join()
	cloud.mu.Lock()
	cloud.missing = true
	cloud.mu.Unlock()
	// The successful renamed runtime published fresh unchanged evidence. Capture
	// its final retained identity after release, before testing absence/replacement.
	coreJSON(t, base, http.MethodGet, "/v1/entities/"+lock.ID, "", &lock)
	stop, join = startCompanion(t, c, cloud)
	assertUnavailableRetained(t, base, lock, "device_missing")
	stop()
	join()
	cloud.mu.Lock()
	cloud.missing = false
	cloud.offline = true
	cloud.id = replacementID
	cloud.name = "Replacement"
	cloud.mu.Unlock()
	c.Locks[0].LockID = replacementID
	stop, join = startCompanion(t, c, cloud)
	waitCondition(t, func() bool {
		var devices struct {
			Items []devicesapi.DeviceBody `json:"items"`
		}
		coreJSON(t, base, http.MethodGet, "/v1/devices", "", &devices)
		return len(devices.Items) == 1 && devices.Items[0].Name == "Replacement"
	})
	assertUnavailableRetained(t, base, lock, "device_offline")
	var retained struct {
		Items []struct {
			ID string `json:"observation_id"`
		} `json:"items"`
	}
	coreJSON(t, base, http.MethodGet, "/v1/entities/"+battery.ID+"/state/history?disposition=all", "", &retained)
	found := false
	for _, item := range retained.Items {
		if item.ID == battery.State.ObservationID {
			found = true
		}
	}
	if !found {
		data, _ := json.Marshal(retained)
		t.Fatalf("lost prior history: %s", data)
	}
	stop()
	join()
}

func assertUnavailableRetained(t *testing.T, base string, before devicesapi.EntityBody, reason string) {
	t.Helper()
	var after devicesapi.EntityBody
	waitCondition(t, func() bool {
		coreJSON(t, base, http.MethodGet, "/v1/entities/"+before.ID, "", &after)
		return after.Availability.Status == "unavailable" && after.Availability.Reason != nil &&
			strings.HasSuffix(after.Availability.Reason.Code, reason)
	})
	if after.State == nil || after.State.ObservationID != before.State.ObservationID ||
		after.State.Value != before.State.Value {
		t.Fatal("unavailability fabricated physical evidence")
	}
}
