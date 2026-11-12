//go:build integration

package levoit //nolint:testpackage // Opt-in compatibility and authorized recovery use the real private client.

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestLiveReadStatus is deliberately opt-in and never changes physical settings.
//
//nolint:paralleltest // Real account access must not overlap other opt-in live checks.
func TestLiveReadStatus(t *testing.T) {
	c := liveClient(t)
	devices, err := c.devices(t.Context())
	if err != nil {
		t.Fatalf("live discovery failed: %s", failureCode(err))
	}
	if len(devices) == 0 {
		t.Fatal("live inventory has no supported purifiers")
	}
	t.Logf("discovered %d supported purifier(s)", len(devices))
	for index, d := range devices {
		if !d.Online {
			t.Errorf("purifier %d (%s) is offline", index+1, d.Model)
			continue
		}
		report, readErr := c.status(t.Context(), d)
		if readErr != nil {
			t.Errorf("purifier %d (%s): %s", index+1, d.Model, failureCode(readErr))
			continue
		}
		encoded, encodeErr := json.Marshal(report)
		if encodeErr != nil {
			t.Fatal("could not encode normalized live status")
		}
		t.Logf("purifier %d (%s) normalized status: %s", index+1, d.Model, encoded)
		if report.Power == nil || report.Mode == nil || report.Display == nil || report.ChildLock == nil ||
			report.PM25 == nil || report.FilterLife == nil {
			t.Errorf("purifier %d lacks required live fields", index+1)
		}
	}
}

// TestLiveRestoreAuto is a separate recovery entry point after a terminated or
// failed live-control process. Run only against the explicitly authorized pair.
// Never run it concurrently with the live-control journey.
//
//nolint:paralleltest // Real physical recovery must not overlap other live checks.
func TestLiveRestoreAuto(t *testing.T) {
	if os.Getenv("HEARTH_LEVOIT_LIVE_RECOVERY_PERMISSION") != "restore-auto-manual-1" {
		t.Skip("explicit live recovery permission not supplied")
	}
	c := liveClient(t)
	devices, err := c.devices(t.Context())
	if err != nil || len(devices) != 2 {
		t.Fatal("could not establish recovery inventory")
	}
	for index, d := range devices {
		for _, action := range []struct {
			name string
			set  func() error
		}{
			{"manual-1", func() error { return c.setLevel(t.Context(), d, 1) }},
			{"auto", func() error { return c.setMode(t.Context(), d, modeAuto) }},
			{"display-on", func() error { return c.setDisplay(t.Context(), d, true) }},
			{"child-lock-off", func() error { return c.setChildLock(t.Context(), d, false) }},
			{"power-on", func() error { return c.setPower(t.Context(), d, true) }},
		} {
			if recoveryErr := action.set(); recoveryErr != nil {
				t.Errorf("purifier %d recovery %s: %s", index+1, action.name, failureCode(recoveryErr))
			}
		}
	}
	// Acknowledgement is not restoration evidence. Allow bounded cloud snapshot
	// convergence using reads only; never retry a restoration mutation.
	deadline := time.Now().Add(4 * time.Minute)
	for {
		confirmed := true
		for _, d := range devices {
			report, readErr := c.status(t.Context(), d)
			confirmed = confirmed && readErr == nil && restoredBaseline(report)
		}
		if confirmed {
			t.Log("both purifier operating baselines confirmed by cloud status")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("recovery baseline not confirmed within four minutes")
		}
		time.Sleep(15 * time.Second)
	}
}

func restoredBaseline(report status) bool {
	return report.Power != nil && *report.Power && report.Mode != nil && *report.Mode == modeAuto &&
		report.Display != nil && *report.Display && report.ChildLock != nil && !*report.ChildLock
}

// TestLiveRestoredBaseline is read-only and separate from recovery. Delay initial
// acquisition so an immediately stale pre-test baseline cannot masquerade as
// restoration evidence. Later reads never issue another mutation.
//
//nolint:paralleltest // Run after the live-control or recovery process exits.
func TestLiveRestoredBaseline(t *testing.T) {
	if os.Getenv("HEARTH_LEVOIT_LIVE_BASELINE_PERMISSION") != "confirm-restored-auto" {
		t.Skip("explicit baseline check not requested")
	}
	c := liveClient(t)
	t.Log("waiting three minutes for cloud snapshot convergence before restoration check")
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal("restoration check cancelled")
	case <-timer.C:
	}
	devices, err := c.devices(t.Context())
	if err != nil || len(devices) != 2 {
		t.Fatal("could not establish restoration inventory")
	}
	deadline := time.Now().Add(time.Minute)
	for {
		confirmed := true
		for _, d := range devices {
			report, readErr := c.status(t.Context(), d)
			confirmed = confirmed && readErr == nil && d.Online && restoredBaseline(report)
		}
		if confirmed {
			t.Log("both cloud snapshots report power on, auto, display on, child lock off")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("restored operating settings not confirmed; inspect purifiers")
		}
		time.Sleep(15 * time.Second)
	}
}

func liveClient(t *testing.T) *client {
	t.Helper()
	path := os.Getenv("HEARTH_LEVOIT_LIVE_CREDENTIALS_FILE")
	if path == "" {
		t.Skip("live VeSync credentials not supplied")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("could not read private live credentials")
	}
	var credentials Credentials
	if json.Unmarshal(data, &credentials) != nil {
		t.Fatal("invalid private live credentials")
	}
	c, err := newClient(Config{PollInterval: time.Minute, TimeZone: "UTC"}, credentials, nil)
	if err != nil {
		t.Fatal("invalid live client configuration")
	}
	return c
}
