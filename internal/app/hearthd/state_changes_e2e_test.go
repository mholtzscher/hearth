//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core assembly.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type stateChangesJourneyResponse struct {
	Kind     string `json:"kind"`
	Coverage struct {
		Seeded bool `json:"seeded"`
	} `json:"coverage"`
	Intervals []struct {
		Kind  string          `json:"kind"`
		From  string          `json:"from"`
		To    string          `json:"to"`
		Value json.RawMessage `json:"value"`
	} `json:"intervals"`
	Buckets []struct {
		SecondsTrue         float64 `json:"seconds_true"`
		TransitionsIntoTrue int     `json:"transitions_into_true"`
		KnownSeconds        float64 `json:"known_seconds"`
	} `json:"buckets"`
}

// TestStateChangesThroughCore publishes yes/no reports through the Adapter SDK
// and reads them back as a timeline and a summary over public HTTP. JetStream
// assigns observed_at, so every boundary comes from the audit API.
func TestStateChangesThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startCoreNATSServer(t)
	address, stop, errors := startDeviceFactsCore(ctx, t, server.ClientURL(), dbtest.MigratedPath(t))
	defer stopDeviceFactsCore(t, stop, errors)
	waitForCoreReady(ctx, t, address, errors)
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "state-changes-journey", SoftwareVersion: "1.0",
		NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	runtime, err := scripted.New(session, []scripted.DeviceSpec{{
		BindingKey: "doors", Name: "Front door", Kind: "sensor", Entities: []scripted.EntitySpec{{
			Key: "contact", Name: "Contact", Type: "hearth.binarysensor/v1",
			Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{}}, Initial: false,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := session.Register(ctx, runtime.Registrations()[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Attach([]adapter.Binding{binding}); err != nil {
		t.Fatal(err)
	}
	contact := string(bindingEntityID(t, binding, "contact"))
	publish := func(value string) time.Time {
		t.Helper()
		result, publishErr := runtime.PublishNow(ctx, contact, json.RawMessage(value))
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		report := historyJourneyAwait(ctx, t, client, address, contact, string(result.ObservationID))
		at, parseErr := time.Parse(time.RFC3339Nano, report.Observed)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		return at.UTC()
	}
	closed := publish(`false`)
	opened := publish(`true`)
	publish(`true`) // unchanged: records no change
	closedAgain := publish(`false`)
	end := closedAgain.Add(time.Millisecond)
	for time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	stamp := func(v time.Time) string { return v.UTC().Format(time.RFC3339Nano) }

	timeline := stateChangesJourneyQuery(ctx, t, client, address, contact, map[string]any{
		"range": map[string]any{"from": stamp(closed), "to": stamp(end)}, "select_field": "",
		"result": map[string]any{"kind": "timeline"},
	})
	type interval struct{ kind, from, to, value string }
	var got []interval
	for _, i := range timeline.Intervals {
		got = append(got, interval{i.Kind, i.From, i.To, string(i.Value)})
	}
	want := []interval{
		{"value", stamp(closed), stamp(opened), "false"},
		{"value", stamp(opened), stamp(closedAgain), "true"},
		{"value", stamp(closedAgain), stamp(end), "false"},
	}
	if timeline.Kind != "timeline" || timeline.Coverage.Seeded || len(got) != len(want) {
		t.Fatalf("timeline = %+v, want %+v", timeline, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("interval %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A range starting while the door was open opens with the seeded value.
	seeded := stateChangesJourneyQuery(ctx, t, client, address, contact, map[string]any{
		"range":        map[string]any{"from": stamp(opened.Add(time.Microsecond)), "to": stamp(end)},
		"select_field": "", "result": map[string]any{"kind": "timeline"},
	})
	if !seeded.Coverage.Seeded || len(seeded.Intervals) != 2 || string(seeded.Intervals[0].Value) != "true" {
		t.Fatalf("seeded timeline = %+v", seeded)
	}

	summary := stateChangesJourneyQuery(ctx, t, client, address, contact, map[string]any{
		"range":        map[string]any{"from": stamp(closed), "to": stamp(end)},
		"select_field": "",
		"result": map[string]any{
			"kind":   "summary",
			"bucket": map[string]any{"duration": "1h", "alignment": "range_start"},
		},
	})
	if len(summary.Buckets) != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	bucket := summary.Buckets[0]
	if bucket.TransitionsIntoTrue != 1 ||
		math.Abs(bucket.SecondsTrue-closedAgain.Sub(opened).Seconds()) > 1e-6 ||
		math.Abs(bucket.KnownSeconds-end.Sub(closed).Seconds()) > 1e-6 {
		t.Fatalf("summary bucket = %+v", bucket)
	}
}

func stateChangesJourneyQuery(
	ctx context.Context, t *testing.T, client *http.Client, address, entity string, body map[string]any,
) stateChangesJourneyResponse {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+address+"/v1/entities/"+entity+"/state/changes", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("state changes status %d: %s", resp.StatusCode, raw)
	}
	var decoded stateChangesJourneyResponse
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
