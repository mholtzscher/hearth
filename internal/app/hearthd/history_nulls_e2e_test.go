//go:build e2e

package hearthd //nolint:testpackage // Exercises the public history journey through production Core.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestHistoryNullGapThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startCoreNATSServer(t)
	address, stop, errors := startDeviceFactsCore(ctx, t, server.ClientURL(), dbtest.MigratedPath(t))
	defer stopDeviceFactsCore(t, stop, errors)
	waitForCoreReady(ctx, t, address, errors)
	client := newNonPoolingHTTPClient(t)
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "history-nulls", SoftwareVersion: "1.0", NATSURL: server.ClientURL(),
	})
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "conditional-history",
		Device:     adapter.DeviceDescriptor{Name: "Conditional setting", Kind: "sensor"},
		Entities: []adapter.EntityDescriptor{
			{
				Key:        "setting",
				ExternalID: "conditional-history.setting",
				Name:       "Setting",
				Type:       "hearth.numericsetting/v1",
				Support: json.RawMessage(
					`{"state":{"minimum":0,"maximum":100,"choices":["auto"],"unit":"%"},"operations":{"set":{}}}`,
				),
			},
		},
	})
	require.NoError(t, err)
	entity := string(bindingEntityID(t, binding, "setting"))
	reports := make([]historyJourneyReport, 0, 3)
	for _, value := range []string{`{"mode":"value","value":40}`, `{"mode":"choice","choice":"auto"}`, `{"mode":"value","value":60}`} {
		id, publishErr := session.PublishObservation(ctx, adapter.Observation{
			EntityID:          entity,
			Value:             json.RawMessage(value),
			AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})
		require.NoError(t, publishErr)
		report := historyJourneyAwait(ctx, t, client, address, entity, string(id))
		require.Equal(t, "applied", report.Disposition)
		require.Nil(t, report.Rejection)
		reports = append(reports, report)
	}
	first, err := time.Parse(time.RFC3339Nano, reports[0].Observed)
	require.NoError(t, err)
	last, err := time.Parse(time.RFC3339Nano, reports[2].Observed)
	require.NoError(t, err)
	// Numeric summaries start on the one-minute rollup grid.
	rangeBody := map[string]any{
		"from": first.Truncate(time.Minute).UTC().Format(time.RFC3339Nano),
		"to":   last.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano),
	}
	basic := map[string]any{"basis": "retained_observations", "completeness": "unverified"}
	metrics := map[string]any{
		"min": map[string]any{"operation": "min"}, "max": map[string]any{"operation": "max"},
		"mean": map[string]any{"operation": "sample_mean"},
	}
	bucket := map[string]any{"duration": "1h", "alignment": "range_start"}
	want := map[string]any{
		"kind": "summary", "entity_id": entity, "range": rangeBody, "time_axis": "observed_at", "coverage": basic,
		"bucket": map[string]any{"duration": "1h0m0s", "alignment": "range_start"},
		"buckets": []any{map[string]any{
			"range": rangeBody, "metrics": map[string]any{"min": nil, "max": nil, "mean": nil},
		}},
	}
	historyJourneyQuery(ctx, t, client, address, entity, map[string]any{
		"range": rangeBody, "select_field": "/value",
		"result": map[string]any{"kind": "summary", "nulls": "gap", "metrics": metrics, "bucket": bucket},
	}, want)
	// The default reject policy refuses the known null instead of averaging around it.
	historyJourneyProblem(ctx, t, client, address, entity, map[string]any{
		"range": rangeBody, "select_field": "/value",
		"result": map[string]any{"kind": "summary", "metrics": metrics, "bucket": bucket},
	}, http.StatusUnprocessableEntity, "value_type", "body.result.metrics.max")
}
