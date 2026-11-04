//go:build e2e

package hearthd //nolint:testpackage // Exercises the public history journey through production Core.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestHistoryNullGapThroughCore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := startCoreNATSServer(t)
	address, stop, errors := startDeviceFactsCore(ctx, t, server.ClientURL(), filepath.Join(t.TempDir(), "hearth.db"))
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
	last, err := time.Parse(time.RFC3339Nano, reports[2].Observed)
	require.NoError(t, err)
	rangeBody := map[string]any{
		"from": reports[0].Observed, "to": last.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano),
	}
	basic := map[string]any{"basis": "retained_observations", "completeness": "unverified"}
	metrics := map[string]any{
		"min": map[string]any{"operation": "min"}, "max": map[string]any{"operation": "max"},
		"mean": map[string]any{"operation": "sample_mean"},
	}
	want := map[string]any{
		"kind": "summary", "entity_id": entity, "range": rangeBody, "time_axis": "observed_at", "coverage": basic,
		"bucket": map[string]any{"duration": "1h0m0s", "alignment": "range_start"},
		"buckets": []any{map[string]any{
			"range": rangeBody, "metrics": map[string]any{"min": nil, "max": nil, "mean": nil},
		}},
	}
	historyJourneyQuery(ctx, t, client, address, entity, map[string]any{
		"range": rangeBody, "select_expr": "sample.value.value",
		"result": map[string]any{
			"kind": "summary", "nulls": "gap", "metrics": metrics,
			"bucket": map[string]any{"duration": "1h", "alignment": "range_start"},
		},
	}, want)
	// The gap policy did not discard or rewrite the underlying reports.
	items := make([]any, 0, len(reports))
	for i, value := range []any{40, nil, 60} {
		report := reports[i]
		items = append(items, map[string]any{
			"observation_id": report.ID, "observed_at": report.Observed,
			"adapter_received_at": report.Received, "source_updated_at": nil,
			"disposition": "applied", "value": value,
		})
	}
	historyJourneyQuery(ctx, t, client, address, entity, map[string]any{
		"range": rangeBody, "select_expr": "sample.value.value", "result": map[string]any{"kind": "observations"},
	}, map[string]any{
		"kind": "observations", "entity_id": entity, "range": rangeBody, "time_axis": "observed_at",
		"coverage": basic, "order": "ascending", "items": items,
	})
}
