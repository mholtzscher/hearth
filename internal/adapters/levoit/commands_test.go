package levoit //nolint:testpackage // Exercises typed routes and acknowledgement-gated optimistic publication.

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestOptimisticControlsPublishOnlyRequestedValuesAndSideEffects(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		key, value string
		want       map[string]string
	}{
		{"power", "false", map[string]string{"power": "false"}},
		{"power", "true", map[string]string{"power": "true"}},
		{"display", "false", map[string]string{"display": "false"}},
		{"child-lock", "true", map[string]string{"child-lock": "true"}},
		{"mode", `"auto"`, map[string]string{"mode": `"auto"`, "power": "true"}},
		{"mode", `"sleep"`, map[string]string{"mode": `"sleep"`, "power": "true"}},
		{"mode", `"manual"`, map[string]string{"mode": `"manual"`, "power": "true", "fan-level": `"1"`}},
		{"fan-level", `"1"`, map[string]string{"fan-level": `"1"`, "mode": `"manual"`, "power": "true"}},
		{"fan-level", `"2"`, map[string]string{"fan-level": `"2"`, "mode": `"manual"`, "power": "true"}},
		{"fan-level", `"3"`, map[string]string{"fan-level": `"3"`, "mode": `"manual"`, "power": "true"}},
		{"fan-level", `"4"`, map[string]string{"fan-level": `"4"`, "mode": `"manual"`, "power": "true"}},
	} {
		t.Run(test.key+"/"+test.value, func(t *testing.T) {
			t.Parallel()
			session := newRecordingSession()
			requests := 0
			a := newTestAdapter(t, session, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
				requests++
				return response(`{"code":0,"result":{"code":0}}`), nil
			}))
			runtime := registerCommandDevice(t, a)
			baseline := staleControlStatus(t)
			if err := a.publish(t.Context(), runtime, baseline, "", nil); err != nil {
				t.Fatal(err)
			}
			before := len(session.observations)
			availability := maps.Clone(session.availability)
			responder := &recordingResponder{}
			if err := a.HandleCommand(
				t.Context(),
				controlCommand(runtime.ids[test.key], test.value),
				responder,
			); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || !responder.accepted || len(responder.linked) != 1 ||
				responder.linked[0].EntityID != runtime.ids[test.key] || string(responder.linked[0].Value) != test.value {
				t.Fatal("Command did not publish exactly one acknowledged optimistic target")
			}
			updates := append([]adapter.Observation{responder.linked[0]}, session.observations[before:]...)
			assertControlUpdates(t, runtime, updates, test.want)
			assertUntouchedAvailability(t, runtime, session, availability, test.want)
			assertCloudRollback(t, a, runtime, session, baseline)
		})
	}
}

func assertUntouchedAvailability(t *testing.T, runtime *deviceRuntime, session *recordingSession,
	before map[string]adapter.EntityAvailabilityReport, changed map[string]string) {
	t.Helper()
	for key, id := range runtime.ids {
		if _, updated := changed[key]; !updated && session.availability[id] != before[id] {
			t.Fatalf("partial update changed untouched %s availability", key)
		}
	}
}

func assertCloudRollback(t *testing.T, a *Adapter, runtime *deviceRuntime, session *recordingSession, baseline status) {
	t.Helper()
	// Cloud status is authoritative even when it rolls optimism back. Missing
	// manual-level evidence once again withdraws that Entity's availability.
	if err := a.publish(t.Context(), runtime, baseline, "", nil); err != nil {
		t.Fatal(err)
	}
	if session.availability[runtime.ids["fan-level"]].Status != adapter.AvailabilityUnavailable {
		t.Fatal("stale full report did not withdraw missing manual level")
	}
	last := latestValues(session.observations)
	if last[runtime.ids["power"]] != "false" || last[runtime.ids["mode"]] != `"auto"` ||
		last[runtime.ids["pm25"]] != "4" || last[runtime.ids["filter-life"]] != "80" {
		t.Fatal("cloud report did not overwrite optimism with real readings")
	}
}

func TestUnsuccessfulMutationNeverPublishesOptimismOrRetries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, body     string
		lost, rejected bool
	}{
		{"rejected", `{"code":0,"result":{"code":11}}`, false, true},
		{"malformed", `{"code":0,"result":{}}`, false, false},
		{"unknown-code", `{"code":0,"result":{"code":1234567}}`, false, false},
		{"lost", "", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := newRecordingSession()
			requests := 0
			a := newTestAdapter(t, session, syntheticTransport(func(_ *http.Request) (*http.Response, error) {
				requests++
				if test.lost {
					return nil, errors.New("synthetic lost response")
				}
				return response(test.body), nil
			}))
			runtime := registerCommandDevice(t, a)
			responder := &recordingResponder{}
			err := a.HandleCommand(t.Context(), controlCommand(runtime.ids["power"], "true"), responder)
			if test.rejected != (responder.rejected != "") || (!test.rejected && err == nil) {
				t.Fatal("incorrect failure classification")
			}
			if requests != 1 || responder.accepted || len(responder.linked) != 0 || len(session.observations) != 0 ||
				len(session.availability) != 0 {
				t.Fatal("failed mutation read/retried or changed State/availability optimistically")
			}
		})
	}
}

func registerCommandDevice(t *testing.T, a *Adapter) *deviceRuntime {
	t.Helper()
	runtime := &deviceRuntime{gate: make(chan struct{}, 1)}
	if err := a.register(
		t.Context(),
		runtime,
		device{CID: "cid", Name: "Purifier", ConfigModule: "module"},
	); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func controlCommand(id, value string) adapter.Command {
	return adapter.Command{EntityID: id, OperationName: "set", Parameters: json.RawMessage(`{"value":` + value + `}`),
		Deadline: time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339Nano)}
}

func staleControlStatus(t *testing.T) status {
	t.Helper()
	report, err := decodeStatus(
		[]byte(
			`{"enabled":false,"mode":"auto","configuration":{"display":true},"child_lock":false,"air_quality_value":4,"filter_life":80}`,
		),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func assertControlUpdates(t *testing.T, runtime *deviceRuntime, updates []adapter.Observation, want map[string]string) {
	t.Helper()
	expected := make(map[string]string)
	for key, value := range want {
		expected[runtime.ids[key]] = value
	}
	if len(updates) != len(want) || !reflect.DeepEqual(latestValues(updates), expected) {
		t.Fatalf("unexpected optimistic target/sibling values: %+v", updates)
	}
}

func latestValues(observations []adapter.Observation) map[string]string {
	values := make(map[string]string)
	for _, observation := range observations {
		values[observation.EntityID] = string(observation.Value)
	}
	return values
}
