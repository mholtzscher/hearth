//go:build e2e

package levoit //nolint:testpackage // Opt-in live verification exercises real companion assembly and Core.

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/httpapi"
)

// TestLiveCompanionControls requires explicit permission in addition to credentials.
// It changes physical purifiers, restores their initial settings, and deliberately
// leaves their stored manual selection at 1 when the provider omitted that value.
func TestLiveCompanionControls(t *testing.T) {
	path := os.Getenv("HEARTH_LEVOIT_LIVE_CREDENTIALS_FILE")
	if path == "" || os.Getenv("HEARTH_LEVOIT_LIVE_CONTROL_PERMISSION") != "restore-auto-manual-1" {
		t.Skip("live physical control permission and credentials not supplied")
	}
	t.Setenv("HEARTH_OPENAI_API_KEY", "synthetic-unused-model-key")
	base, natsURL := startCore(t)
	stop, join := startCompanion(
		t,
		Config{AdapterID: "levoit-live-check", NATSURL: natsURL, CredentialsFile: path},
		nil,
	)
	defer func() { stop(); join() }()
	byDevice := liveBaseline(t, base)
	// Register restoration before the first physical mutation, including after an
	// ambiguous response. Individual verification Commands are never retried.
	defer restoreLiveSettings(t, base, byDevice)
	for _, entities := range byDevice {
		for _, control := range []struct {
			name  string
			value any
		}{
			{"Power", false}, {"Power", true}, {"Display", false}, {"Display", true},
			{"Child lock", true}, {"Child lock", false}, {"Mode", "sleep"}, {"Mode", "auto"},
			{"Manual fan level", "1"}, {"Manual fan level", "2"}, {"Manual fan level", "3"}, {"Manual fan level", "4"},
			{"Mode", "manual"}, {"Mode", "auto"},
		} {
			if deadline, bounded := t.Deadline(); bounded && time.Until(deadline) < 2*time.Minute {
				t.Error("stopping live probes to reserve restoration time")
				return
			}
			if !liveCommand(t, base, entities[control.name], control.value, "verify") {
				return
			}
		}
	}
}

func liveBaseline(t *testing.T, base string) map[string]map[string]devicesapi.EntityBody {
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
			if entity.Name != "Manual fan level" && (entity.State == nil || entity.Availability.Status != "available") {
				return false
			}
		}
		return true
	})
	byDevice := make(map[string]map[string]devicesapi.EntityBody)
	for _, entity := range page.Items {
		if byDevice[entity.DeviceID] == nil {
			byDevice[entity.DeviceID] = make(map[string]devicesapi.EntityBody)
		}
		byDevice[entity.DeviceID][entity.Name] = entity
	}
	if len(byDevice) != 2 {
		t.Fatal("expected exactly two live purifiers before physical changes")
	}
	for _, entities := range byDevice {
		if entities["Mode"].State.Value != "auto" || entities["Power"].State.Value != true ||
			entities["Display"].State.Value != true || entities["Child lock"].State.Value != false {
			t.Fatal("live baseline changed; refusing physical controls")
		}
	}
	return byDevice
}

func restoreLiveSettings(t *testing.T, base string, byDevice map[string]map[string]devicesapi.EntityBody) {
	t.Helper()
	for _, entities := range byDevice {
		for _, name := range []string{"Manual fan level", "Mode", "Display", "Child lock", "Power"} {
			value := any("1")
			if name != "Manual fan level" {
				value = entities[name].State.Value
			}
			if !liveCommand(t, base, entities[name], value, "restore") {
				t.Errorf("restoration failed for %s; inspect the purifier", name)
			}
		}
	}
}

func liveCommand(t *testing.T, base string, entity devicesapi.EntityBody, value any, phase string) bool {
	t.Helper()
	data, err := json.Marshal(map[string]any{"operation": "set", "parameters": map[string]any{"value": value}})
	if err != nil {
		t.Error("could not encode live Command")
		return false
	}
	status, raw, err := coreRequest(
		t.Context(),
		base,
		http.MethodPost,
		"/v1/entities/"+entity.ID+"/commands",
		string(data),
	)
	var result devicesapi.CommandResultBody
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &result) != nil || result.Status != "satisfied" ||
		result.Value == nil || *result.Value != value || result.ObservationID == nil {
		t.Errorf("%s %s=%v: HTTP %d, satisfied evidence unavailable", phase, entity.Name, value, status)
		return false
	}
	var retained struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		ObservationID string `json:"outcome_observation_id"`
	}
	status, raw, err = coreRequest(t.Context(), base, http.MethodGet, "/v1/commands/"+result.CommandID, "")
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &retained) != nil ||
		retained.ID != result.CommandID || retained.Status != "satisfied" || retained.ObservationID != *result.ObservationID {
		t.Error("live Command durable evidence mismatch")
		return false
	}
	var after devicesapi.EntityBody
	status, raw, err = coreRequest(t.Context(), base, http.MethodGet, "/v1/entities/"+entity.ID, "")
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &after) != nil ||
		after.State == nil || after.State.Value != value || after.State.ObservationID != *result.ObservationID {
		t.Error("live State does not match Command evidence")
		return false
	}
	t.Logf(
		"%s %s=%v: optimistic satisfied %s / %s (not physical confirmation)",
		phase,
		entity.Name,
		value,
		result.CommandID,
		*result.ObservationID,
	)
	return true
}
