//nolint:goconst // Literal schema and wire keys keep the local contract readable.
package mcpapi

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerQueryEntityStateHistory(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[QueryEntityStateHistoryInput, historyResult]{
		Name:        "query_entity_state_history",
		Description: "Summarize a numeric State field from retained observations. sample_mean is arithmetic, including for circular quantities such as hue and wind direction. Omit units for household units; canonical retains stored units.",
		Handler:     h.queryEntityStateHistory,
	})
}

type historyRange struct {
	From string `json:"from" format:"date-time"`
	To   string `json:"to"   format:"date-time"`
}
type historyMetric struct {
	Operation string `json:"operation" enum:"min,max,sample_mean"`
}
type historyBucket struct {
	Duration  string `json:"duration"`
	Alignment string `json:"alignment" enum:"range_start"`
}
type historyRequestResult struct {
	Kind    string                   `json:"kind"             enum:"summary"`
	Metrics map[string]historyMetric `json:"metrics"                            minProperties:"1" maxProperties:"16"`
	Bucket  *historyBucket           `json:"bucket,omitempty"`
	Nulls   string                   `json:"nulls,omitempty"  enum:"reject,gap"`
}
type QueryEntityStateHistoryInput struct {
	EntityID    entityID             `json:"entity_id"`
	Range       historyRange         `json:"range"`
	SelectField string               `json:"select_field"    maxLength:"64" pattern:"^(/[a-z][a-z0-9_]*)?$"`
	Result      historyRequestResult `json:"result"`
	Units       string               `json:"units,omitempty"                                                enum:",canonical"`
}

// UnmarshalJSON rejects nulls, unknown fields, and case variants even where the generic
// decoder would accept them. Schema validation still enforces required fields.
//
//nolint:gocognit // The checks mirror the nested request shape.
func (input *QueryEntityStateHistoryInput) UnmarshalJSON(data []byte) error {
	invalid := invalidRequestFailure("invalid history query")
	if len(data) > devices.HistoryMaxRequestBytes || !jsontext.Value(data).IsValid() {
		return invalid
	}
	var raw any
	if json.Unmarshal(data, &raw) != nil {
		return invalid
	}
	object := func(v any, allowed ...string) (map[string]any, bool) {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		for k, value := range m {
			if value == nil || !slices.Contains(allowed, k) {
				return nil, false
			}
		}
		return m, true
	}
	root, ok := object(raw, historyEntityIDKey, "range", "select_field", "result", "units")
	if !ok {
		return invalid
	}
	if _, ok = object(root["range"], "from", "to"); !ok {
		return invalid
	}
	result, ok := object(root["result"], "kind", "metrics", "bucket", "nulls")
	if !ok {
		return invalid
	}
	if bucket, exists := result["bucket"]; exists {
		if _, ok = object(bucket, "duration", "alignment"); !ok {
			return invalid
		}
	}
	metrics, ok := result["metrics"].(map[string]any)
	if !ok {
		return invalid
	}
	for _, metric := range metrics {
		if _, ok = object(metric, "operation"); !ok {
			return invalid
		}
	}
	type arguments QueryEntityStateHistoryInput
	var decoded arguments
	if err := decodeJSON(data, &decoded); err != nil {
		return err
	}
	*input = QueryEntityStateHistoryInput(decoded)
	return nil
}

type historyCoverage struct {
	Basis        string `json:"basis"`
	Completeness string `json:"completeness"`
}
type historyResultBucket struct {
	Range   historyRange               `json:"range"`
	Metrics map[string]json.RawMessage `json:"metrics"`
}
type historyResult struct {
	Kind     string                     `json:"kind"`
	EntityID string                     `json:"entity_id"`
	Range    historyRange               `json:"range"`
	TimeAxis string                     `json:"time_axis"`
	Coverage historyCoverage            `json:"coverage"`
	Quantity devices.QuantityMetadata   `json:"quantity"`
	Metrics  map[string]json.RawMessage `json:"metrics,omitempty"`
	Bucket   *historyBucket             `json:"bucket,omitempty"`
	Buckets  *[]historyResultBucket     `json:"buckets,omitempty"`
}

const historyEntityIDKey = "entity_id"

func (historyResult) Schema(r huma.Registry) *huma.Schema {
	rangeSchema := r.Schema(reflect.TypeFor[historyRange](), false, "")
	coverageSchema := &huma.Schema{Type: "object", AdditionalProperties: false,
		Properties: map[string]*huma.Schema{
			"basis":        {Type: "string", Const: "retained_observations"},
			"completeness": {Type: "string", Const: "unverified"},
		}, Required: []string{"basis", "completeness"},
	}
	quantitySchema := r.Schema(reflect.TypeFor[devices.QuantityMetadata](), false, "")
	metricSchema := &huma.Schema{Type: "object", AdditionalProperties: &huma.Schema{Type: "number", Nullable: true}}
	base := func() map[string]*huma.Schema {
		return map[string]*huma.Schema{
			"kind": {Type: "string", Const: "summary"}, historyEntityIDKey: {Type: "string"},
			"range": rangeSchema, "time_axis": {Type: "string", Const: "observed_at"},
			"coverage": coverageSchema, "quantity": quantitySchema,
		}
	}
	common := []string{"kind", historyEntityIDKey, "range", "time_axis", "coverage", "quantity"}
	summary := base()
	summary["metrics"] = metricSchema
	bucketed := base()
	bucketed["bucket"] = r.Schema(reflect.TypeFor[historyBucket](), false, "")
	bucketed["buckets"] = &huma.Schema{Type: "array", Items: &huma.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Properties: map[string]*huma.Schema{
			"range":   rangeSchema,
			"metrics": metricSchema,
		},
		Required: []string{"range", "metrics"},
	}}
	return &huma.Schema{Type: "object", OneOf: []*huma.Schema{
		{
			Type:                 "object",
			AdditionalProperties: false,
			Properties:           summary,
			Required:             append(slices.Clone(common), "metrics"),
		},
		{
			Type:                 "object",
			AdditionalProperties: false,
			Properties:           bucketed,
			Required:             append(slices.Clone(common), "bucket", "buckets"),
		},
	}}
}

//nolint:funlen // The operation keeps request translation and result mapping together.
func (h *handler) queryEntityStateHistory(
	ctx context.Context,
	input QueryEntityStateHistoryInput,
) (historyResult, error) {
	units, err := devices.ParseReadUnits(input.Units)
	if err != nil {
		return historyResult{}, invalidRequestFailure("invalid units")
	}
	from, fromErr := time.Parse(time.RFC3339Nano, input.Range.From)
	to, toErr := time.Parse(time.RFC3339Nano, input.Range.To)
	if fromErr != nil || toErr != nil {
		return historyResult{}, invalidRequestFailure("invalid range")
	}
	query := devices.HistoryQuery{
		EntityID: devices.EntityID(input.EntityID),
		Range:    devices.HistoryRange{From: from.UTC(), To: to.UTC()},
		Field:    input.SelectField,
		Metrics:  make(map[string]devices.HistoryMetric, len(input.Result.Metrics)),
		Nulls:    devices.HistoryNullPolicy(input.Result.Nulls),
	}
	if query.Nulls == "" {
		query.Nulls = devices.HistoryNullReject
	}
	if b := input.Result.Bucket; b != nil {
		duration, parseErr := time.ParseDuration(b.Duration)
		if parseErr != nil {
			return historyResult{}, invalidRequestFailure("invalid bucket duration")
		}
		query.Bucket = &devices.HistoryBucket{Duration: duration, Alignment: b.Alignment}
	}
	for name, metric := range input.Result.Metrics {
		switch metric.Operation {
		case "min":
			query.Metrics[name] = devices.HistoryMinimum{}
		case "max":
			query.Metrics[name] = devices.HistoryMaximum{}
		case "sample_mean":
			query.Metrics[name] = devices.HistorySampleMean{}
		default:
			return historyResult{}, invalidRequestFailure("invalid metric")
		}
	}
	answer, err := h.devices.QueryHistory(ctx, query)
	if err != nil {
		return historyResult{}, historyFailure(ctx, err)
	}
	view, err := h.devices.GetEntity(ctx, query.EntityID)
	if err != nil {
		return historyResult{}, historyFailure(ctx, err)
	}
	answer, quantity, err := h.presenter.History(view, query.Field, answer, units)
	if err != nil {
		return historyResult{}, mcptools.InternalToolError(err)
	}
	rangeValue := func(r devices.HistoryRange) historyRange {
		return historyRange{From: formatTime(r.From), To: formatTime(r.To)}
	}
	result := historyResult{
		Kind:     "summary",
		EntityID: string(answer.EntityID),
		Range:    rangeValue(answer.Range),
		TimeAxis: "observed_at",
		Coverage: historyCoverage{Basis: "retained_observations", Completeness: "unverified"},
		Quantity: quantity,
	}
	switch data := answer.Data.(type) {
	case devices.HistorySummaryResult:
		result.Metrics = data.Metrics
	case devices.HistoryBucketedSummaryResult:
		result.Bucket = &historyBucket{Duration: data.Bucket.Duration.String(), Alignment: data.Bucket.Alignment}
		buckets := make([]historyResultBucket, 0, len(data.Buckets))
		for _, b := range data.Buckets {
			buckets = append(buckets, historyResultBucket{Range: rangeValue(b.Range), Metrics: b.Metrics})
		}
		result.Buckets = &buckets
	default:
		return historyResult{}, mcptools.InternalToolError(fmt.Errorf("unknown history result %T", answer.Data))
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return historyResult{}, mcptools.InternalToolError(err)
	}
	if len(encoded) > devices.HistoryMaxResultBytes {
		return historyResult{}, toolError("query_limit", "history query result exceeds the limit")
	}
	return result, nil
}

func historyFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return toolError("query_canceled", "query canceled")
	}
	if errors.Is(err, devices.ErrEntityNotFound) {
		return toolError(failureEntityNotFound, "entity not found")
	}
	if queryErr, ok := errors.AsType[*devices.HistoryQueryError](err); ok {
		switch queryErr.Code {
		case "invalid_query", "unsupported_source", "query_limit", "value_type":
			return toolError(failureCode(queryErr.Code), "history query failed")
		}
	}
	return mcptools.InternalToolError(err)
}
