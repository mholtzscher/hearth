//go:build e2e

package hearthd //nolint:testpackage // Exercises production Core assembly.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
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
// JetStream assigns observed_at, so all range boundaries and bucket expectations
// come from the existing audit API, not the simulator's Adapter clock.
func TestHistoryQueryThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startCoreNATSServer(t)
	address, stop, errors := startDeviceFactsCore(ctx, t, server.ClientURL(), dbtest.MigratedPath(t))
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
	n0 := publish(numeric, `10`)
	n1 := publish(numeric, `20`)
	n2 := publish(numeric, `20`)
	c0 := publish(color, `{"active":true,"hue":20,"saturation":40}`)
	c1 := publish(color, `{"active":true,"hue":20,"saturation":60}`)
	if n0.Disposition != "applied" || n1.Disposition != "applied" || n2.Disposition != "unchanged" {
		t.Fatalf("unexpected audit dispositions: %#v %#v %#v", n0, n1, n2)
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
	envelope := func(entity string, r map[string]any) map[string]any {
		return map[string]any{
			"kind": "summary", "entity_id": entity, "range": r, "time_axis": "observed_at",
			"coverage": map[string]any{"basis": "retained_observations", "completeness": "unverified"},
		}
	}
	metrics := map[string]any{
		"minimum": map[string]any{"operation": "min"},
		"maximum": map[string]any{"operation": "max"},
		"average": map[string]any{"operation": "sample_mean"},
	}

	// Core assigns observed_at, so the expected one-minute buckets come from the
	// audit API's timestamps. From starts one empty minute before the first report.
	from := parse(n0.Observed).Truncate(time.Minute).Add(-time.Minute)
	to := parse(n2.Observed).Add(time.Nanosecond)
	nr := map[string]any{"from": stamp(from), "to": stamp(to)}
	buckets := historyJourneyMinuteBuckets(from, to, []historyJourneySample{
		{parse(n0.Observed), 10}, {parse(n1.Observed), 20}, {parse(n2.Observed), 20},
	})
	want := envelope(numeric, nr)
	want["quantity"] = map[string]any{
		"unit":            "lx",
		"label":           "lx",
		"fraction_digits": 1,
		"range":           map[string]any{"minimum": 0, "maximum": 1000},
	}
	want["bucket"] = map[string]any{"duration": "1m0s", "alignment": "range_start"}
	want["buckets"] = buckets
	historyJourneyQuery(ctx, t, client, address, numeric, map[string]any{
		"range": nr, "select_field": "",
		"result": map[string]any{
			"kind":    "summary",
			"metrics": metrics,
			"bucket":  map[string]any{"duration": "1m", "alignment": "range_start"},
		},
	}, want)
	want = envelope(numeric, nr)
	want["quantity"] = map[string]any{
		"unit":            "lx",
		"label":           "lx",
		"fraction_digits": 1,
		"range":           map[string]any{"minimum": 0, "maximum": 1000},
	}
	want["metrics"] = map[string]any{"minimum": 10, "maximum": 20, "average": float64(50) / 3}
	historyJourneyQuery(ctx, t, client, address, numeric, map[string]any{
		"range": nr, "select_field": "", "result": map[string]any{"kind": "summary", "metrics": metrics},
	}, want)

	cr := map[string]any{
		"from": stamp(parse(c0.Observed).Truncate(time.Minute)), "to": stamp(parse(c1.Observed).Add(time.Nanosecond)),
	}
	want = envelope(color, cr)
	want["quantity"] = map[string]any{
		"unit":            "%",
		"label":           "%",
		"fraction_digits": 0,
		"range":           map[string]any{"minimum": 0, "maximum": 100},
	}
	want["metrics"] = map[string]any{"minimum": 40, "maximum": 60, "average": 50}
	historyJourneyQuery(ctx, t, client, address, color, map[string]any{
		"range": cr, "select_field": "/saturation", "result": map[string]any{"kind": "summary", "metrics": metrics},
	}, want)

	summary := map[string]any{"kind": "summary", "metrics": metrics}
	for _, tc := range []struct {
		name, entity string
		body         map[string]any
		status       int
		code, field  string
	}{
		{"boolean field", color, map[string]any{"range": cr, "select_field": "/active", "result": summary},
			http.StatusUnprocessableEntity, "invalid_query", "body.select_field"},
		{"boolean root", binary, map[string]any{"range": nr, "select_field": "", "result": summary},
			http.StatusUnprocessableEntity, "invalid_query", "body.select_field"},
		{"unaligned from", numeric, map[string]any{
			"range": map[string]any{"from": stamp(from.Add(time.Second)), "to": nr["to"]}, "select_field": "", "result": summary,
		}, http.StatusUnprocessableEntity, "invalid_query", "body.range"},
		{"removed expression query", numeric, map[string]any{
			"range": nr, "select_expr": "sample.value", "result": map[string]any{"kind": "observations"},
		}, http.StatusBadRequest, "invalid_query", ""},
	} {
		historyJourneyProblem(ctx, t, client, address, tc.entity, tc.body, tc.status, tc.code, tc.field)
	}
}

type historyJourneySample struct {
	at    time.Time
	value float64
}

// historyJourneyMinuteBuckets is the test's independent oracle for one-minute
// min/max/mean buckets including the final minute whole. Only its displayed
// range is clipped at to.
func historyJourneyMinuteBuckets(from, to time.Time, samples []historyJourneySample) []any {
	stamp := func(v time.Time) string { return v.UTC().Format(time.RFC3339Nano) }
	var buckets []any
	for start := from; start.Before(to); start = start.Add(time.Minute) {
		end := start.Add(time.Minute)
		if end.After(to) {
			end = to
		}
		metrics := map[string]any{"minimum": nil, "maximum": nil, "average": nil}
		var values []float64
		for _, sample := range samples {
			if !sample.at.Before(start) && sample.at.Before(start.Add(time.Minute)) {
				values = append(values, sample.value)
			}
		}
		if len(values) > 0 {
			sum := 0.0
			for _, value := range values {
				sum += value
			}
			metrics = map[string]any{
				"minimum": slices.Min(values), "maximum": slices.Max(values), "average": sum / float64(len(values)),
			}
		}
		buckets = append(buckets, map[string]any{
			"range": map[string]any{"from": stamp(start), "to": stamp(end)}, "metrics": metrics,
		})
	}
	return buckets
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
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("history query response\ngot: %s\nwant: %s", raw, expectedJSON)
	}
}

func historyJourneyProblem(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	address, entity string,
	body map[string]any,
	status int,
	code, field string,
) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+address+"/v1/entities/"+entity+"/state/history", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var problem struct {
		Code   string `json:"code"`
		Errors []struct {
			Location string `json:"location"`
		} `json:"errors"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	location := ""
	if len(problem.Errors) > 0 {
		location = problem.Errors[0].Location
	}
	if resp.StatusCode != status || problem.Code != code || location != field {
		t.Fatalf("%s: status %d code %q location %q, want %d %q %q", body, resp.StatusCode, problem.Code, location,
			status, code, field)
	}
}
