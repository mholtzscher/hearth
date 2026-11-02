//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core assembly.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type historyJourneyReport struct {
	ID          string  `json:"observation_id"`
	Observed    string  `json:"observed_at"`
	Received    string  `json:"adapter_received_at"`
	Source      *string `json:"source_updated_at"`
	Disposition string  `json:"disposition"`
	Rejection   *string `json:"rejection_code"`
	Value       any     `json:"value"`
}

// TestHistoryQueryThroughCore exercises the public SDK-to-storage-to-HTTP path.
// JetStream assigns observed_at, so all range boundaries and duration expectations
// come from the existing audit API, not the simulator's Adapter clock.
func TestHistoryQueryThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startCoreNATSServer(t)
	address, stop, errors := startDeviceFactsCore(ctx, t, server.ClientURL(), filepath.Join(t.TempDir(), "hearth.db"))
	defer stopDeviceFactsCore(t, stop, errors)
	waitForCoreReady(ctx, t, address, errors)
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(
		ctx,
		adapter.Config{
			AdapterID:       "simulator",
			SoftwareName:    "history-journey",
			SoftwareVersion: "1.0",
			NATSURL:         server.ClientURL(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	runtime, err := scripted.New(
		session,
		[]scripted.DeviceSpec{
			{BindingKey: "history", Name: "History sensors", Kind: "sensor", Entities: []scripted.EntitySpec{
				{
					Key:     "binary",
					Name:    "Contact",
					Type:    "hearth.binarysensor/v1",
					Support: map[string]any{"state": map[string]any{}, "operations": map[string]any{}},
					Initial: false,
				},
				{
					Key:  "numeric",
					Name: "Illuminance",
					Type: "hearth.numericsensor/v1",
					Support: map[string]any{
						"state":      map[string]any{"minimum": 0, "maximum": 1000, "unit": "lx"},
						"operations": map[string]any{},
					},
					Initial: 10,
				},
				{
					Key:  "color",
					Name: "Color",
					Type: "hearth.colorhs/v1",
					Support: map[string]any{
						"state":      map[string]any{},
						"operations": map[string]any{"set": map[string]any{}},
					},
					Initial: map[string]any{"active": true, "hue": 20, "saturation": 40},
				},
			}},
		},
	)
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
	binary := string(bindingEntityID(t, binding, "binary"))
	numeric := string(bindingEntityID(t, binding, "numeric"))
	color := string(bindingEntityID(t, binding, "color"))
	publish := func(entity, value string) historyJourneyReport {
		t.Helper()
		result, publishErr := runtime.PublishNow(ctx, entity, json.RawMessage(value))
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		return historyJourneyAwait(ctx, t, client, address, entity, string(result.ObservationID))
	}
	b0 := publish(binary, `false`)
	b1 := publish(binary, `true`)
	b2 := publish(binary, `true`)
	rejectedID, err := session.PublishObservation(ctx, adapter.Observation{
		EntityID: binary, Value: json.RawMessage(`123`), AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	rejected := historyJourneyAwait(ctx, t, client, address, binary, string(rejectedID))
	if rejected.Disposition != "rejected" || rejected.Rejection == nil || *rejected.Rejection != "invalid_value" {
		t.Fatalf("audit rejection = %#v, want rejected/invalid_value", rejected)
	}
	b3 := publish(binary, `false`)
	n0 := publish(numeric, `10`)
	n1 := publish(numeric, `20`)
	n2 := publish(numeric, `20`)
	c0 := publish(color, `{"active":true,"hue":20,"saturation":40}`)
	c1 := publish(color, `{"active":true,"hue":20,"saturation":60}`)

	// Check the preexisting audit endpoint and the captured repeated report.
	if b0.Disposition != "applied" || b1.Disposition != "applied" || b2.Disposition != "unchanged" ||
		b3.Disposition != "applied" || n0.Disposition != "applied" || n1.Disposition != "applied" ||
		n2.Disposition != "unchanged" {
		t.Fatalf("unexpected audit dispositions: %#v %#v %#v %#v %#v %#v %#v", b0, b1, b2, b3, n0, n1, n2)
	}
	parse := func(s string) time.Time {
		t.Helper()
		v, e := time.Parse(time.RFC3339Nano, s)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	stamp := func(v time.Time) string { return v.UTC().Format(time.RFC3339Nano) }
	brange := map[string]any{
		"from": stamp(parse(b0.Observed).Add(-time.Second)),
		"to":   stamp(parse(b3.Observed).Add(time.Nanosecond)),
	}
	basic := map[string]any{"basis": "retained_observations", "completeness": "unverified"}
	envelope := func(kind, entity string, r map[string]any, coverage map[string]any) map[string]any {
		return map[string]any{
			"kind":      kind,
			"entity_id": entity,
			"range":     r,
			"time_axis": "observed_at",
			"coverage":  coverage,
		}
	}
	item := func(r historyJourneyReport, value any) map[string]any {
		return map[string]any{
			"observation_id":      r.ID,
			"observed_at":         r.Observed,
			"adapter_received_at": r.Received,
			"source_updated_at":   r.Source,
			"disposition":         r.Disposition,
			"value":               value,
		}
	}
	want := envelope("observations", binary, brange, basic)
	want["order"] = "ascending"
	want["items"] = []any{item(b1, true), item(b2, true)}
	historyJourneyQuery(
		ctx,
		t,
		client,
		address,
		binary,
		map[string]any{
			"range":  brange,
			"result": map[string]any{"kind": "observations", "where_expr": "value == true"},
		},
		want,
	)

	temporalCoverage := map[string]any{
		"basis":                  "retained_observations",
		"completeness":           "unverified",
		"seed_available":         false,
		"inferred_known_seconds": parse(brange["to"].(string)).Sub(parse(b0.Observed)).Seconds(),
		"unknown_seconds":        1.0,
	}
	want = envelope("timeline", binary, brange, temporalCoverage)
	want["intervals"] = []any{
		map[string]any{"kind": "unknown", "from": brange["from"], "to": b0.Observed, "reason": "no_prior_observation"},
		map[string]any{"kind": "value", "from": b0.Observed, "to": b1.Observed, "value": false},
		map[string]any{"kind": "value", "from": b1.Observed, "to": b3.Observed, "value": true},
		map[string]any{"kind": "value", "from": b3.Observed, "to": brange["to"], "value": false},
	}
	historyJourneyQuery(
		ctx,
		t,
		client,
		address,
		binary,
		map[string]any{"range": brange, "result": map[string]any{"kind": "timeline"}},
		want,
	)
	want = envelope("summary", binary, brange, temporalCoverage)
	want["metrics"] = map[string]any{
		"open_seconds": parse(b3.Observed).Sub(parse(b1.Observed)).Seconds(),
		"openings":     1,
	}
	historyJourneyQuery(
		ctx,
		t,
		client,
		address,
		binary,
		map[string]any{
			"range": brange,
			"result": map[string]any{
				"kind": "summary",
				"metrics": map[string]any{
					"open_seconds": map[string]any{
						"operation":  "duration_where",
						"where_expr": "value == true",
						"unit":       "seconds",
					},
					"openings": map[string]any{"operation": "transitions_into", "where_expr": "value == true"},
				},
			},
		},
		want,
	)

	nr := map[string]any{
		"from": stamp(parse(n0.Observed).Add(-time.Hour)),
		"to":   stamp(parse(n2.Observed).Add(time.Nanosecond)),
	}
	metrics := map[string]any{
		"minimum": map[string]any{"operation": "min"},
		"maximum": map[string]any{"operation": "max"},
		"average": map[string]any{"operation": "sample_mean"},
	}
	bucket := map[string]any{"duration": "1h", "alignment": "range_start"}
	want = envelope("summary", numeric, nr, basic)
	want["bucket"] = bucket
	want["buckets"] = []any{
		map[string]any{
			"range":   map[string]any{"from": nr["from"], "to": n0.Observed},
			"metrics": map[string]any{"minimum": nil, "maximum": nil, "average": nil},
		},
		map[string]any{
			"range":   map[string]any{"from": n0.Observed, "to": nr["to"]},
			"metrics": map[string]any{"minimum": 10, "maximum": 20, "average": float64(50) / 3},
		},
	}
	historyJourneyQuery(
		ctx,
		t,
		client,
		address,
		numeric,
		map[string]any{"range": nr, "result": map[string]any{"kind": "summary", "metrics": metrics, "bucket": bucket}},
		want,
	)
	cr := map[string]any{"from": c0.Observed, "to": stamp(parse(c1.Observed).Add(time.Nanosecond))}
	want = envelope("observations", color, cr, basic)
	want["order"] = "ascending"
	want["items"] = []any{item(c0, 20), item(c1, 20)}
	historyJourneyQuery(
		ctx,
		t,
		client,
		address,
		color,
		map[string]any{
			"range":       cr,
			"select_expr": "sample.value.hue",
			"result":      map[string]any{"kind": "observations"},
		},
		want,
	)
}

func historyJourneyAwait(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	address, entity, id string,
) historyJourneyReport {
	t.Helper()
	var found historyJourneyReport
	waitForMatrixCondition(t, 20*time.Second, func() (bool, error) {
		req, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"http://"+address+"/v1/entities/"+entity+"/state/history?disposition=all",
			nil,
		)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return false, fmt.Errorf("audit GET status %d: %s", resp.StatusCode, body)
		}
		var page struct {
			Items []historyJourneyReport `json:"items"`
		}
		if err = json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return false, err
		}
		for _, report := range page.Items {
			if report.ID == id {
				found = report
				return true, nil
			}
		}
		return false, nil
	})
	if found.Observed == "" || found.Received == "" {
		t.Fatalf("audit report lacks timestamps: %#v", found)
	}
	return found
}

func historyJourneyQuery(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	address, entity string,
	body, want map[string]any,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+address+"/v1/entities/"+entity+"/state/history",
		bytes.NewReader(encoded),
	)
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
		t.Fatalf("history query POST status = %d, body = %s", resp.StatusCode, raw)
	}
	var actual any
	if err = json.Unmarshal(raw, &actual); err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var expected any
	if err = json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatal(err)
	}
	historyJourneyMatchDurations(actual, expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("history query response\ngot: %s\nwant: %s", raw, expectedJSON)
	}
}

func historyJourneyMatchDurations(actual, expected any) {
	// Independently accumulated fractional seconds may differ by rounding. Permit
	// at most one nanosecond only for the requested duration fields; retain exact
	// comparison for all other values, keys, types, and metric counts.
	actualObject, actualOK := actual.(map[string]any)
	expectedObject, expectedOK := expected.(map[string]any)
	if actualOK && expectedOK {
		for _, field := range [][2]string{{"coverage", "inferred_known_seconds"}, {"coverage", "unknown_seconds"}, {"metrics", "open_seconds"}} {
			actualGroup, aok := actualObject[field[0]].(map[string]any)
			expectedGroup, eok := expectedObject[field[0]].(map[string]any)
			if !aok || !eok {
				continue
			}
			a, aok := actualGroup[field[1]].(float64)
			e, eok := expectedGroup[field[1]].(float64)
			if aok && eok && math.Abs(a-e) <= 1e-9 {
				actualGroup[field[1]] = e
			}
		}
	}
}
