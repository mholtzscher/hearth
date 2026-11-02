package httpapi

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type historyRangeBody struct {
	From string `json:"from" format:"date-time"`
	To   string `json:"to"   format:"date-time"`
}
type historyBucketBody struct {
	Duration  string `json:"duration"`
	Alignment string `json:"alignment" enum:"range_start"`
}
type historyObservationRequest struct {
	Kind      string `json:"kind"                 enum:"observations"`
	WhereExpr string `json:"where_expr,omitempty"                             maxLength:"4096"`
	Order     string `json:"order,omitempty"      enum:"ascending,descending"`
	Limit     int    `json:"limit,omitempty"                                                   minimum:"1" maximum:"200"`
	Cursor    string `json:"cursor,omitempty"`
}
type historyTimelineRequest struct {
	Kind string `json:"kind" enum:"timeline"`
}
type historyNumericMetric struct {
	Operation string `json:"operation" enum:"min,max,sample_mean"`
}
type historyDurationMetric struct {
	Operation string `json:"operation"  enum:"duration_where"`
	WhereExpr string `json:"where_expr"                       maxLength:"4096"`
	Unit      string `json:"unit"       enum:"seconds"`
}
type historyTransitionMetric struct {
	Operation string `json:"operation"  enum:"transitions_into"`
	WhereExpr string `json:"where_expr"                         maxLength:"4096"`
}
type historySummaryRequest struct {
	Kind    string                     `json:"kind"             enum:"summary"`
	Metrics map[string]json.RawMessage `json:"metrics"`
	Bucket  *historyBucketBody         `json:"bucket,omitempty"`
}
type historyRequest struct {
	Range      historyRangeBody `json:"range"`
	SelectExpr *string          `json:"select_expr,omitempty" maxLength:"4096"`
	Result     json.RawMessage  `json:"result"`
}

type HistoryQueryBasicCoverage struct {
	Basis        string `json:"basis"        enum:"retained_observations"`
	Completeness string `json:"completeness" enum:"unverified"`
}
type historyTemporalCoverage struct {
	HistoryQueryBasicCoverage

	SeedAvailable        bool    `json:"seed_available"`
	InferredKnownSeconds float64 `json:"inferred_known_seconds" minimum:"0"`
	UnknownSeconds       float64 `json:"unknown_seconds"        minimum:"0"`
}
type historyDurationCoverage struct {
	InferredKnownSeconds float64 `json:"inferred_known_seconds" minimum:"0"`
	UnknownSeconds       float64 `json:"unknown_seconds"        minimum:"0"`
}
type HistoryQueryEnvelope struct {
	Kind     string           `json:"kind"`
	EntityID string           `json:"entity_id"`
	Range    historyRangeBody `json:"range"`
	TimeAxis string           `json:"time_axis" enum:"observed_at"`
}
type historyObservationBody struct {
	ObservationID     string  `json:"observation_id"`
	ObservedAt        string  `json:"observed_at"         format:"date-time"`
	AdapterReceivedAt string  `json:"adapter_received_at" format:"date-time"`
	SourceUpdatedAt   *string `json:"source_updated_at"`
	Disposition       string  `json:"disposition"                            enum:"applied,unchanged"`
	Value             any     `json:"value"`
}
type historyObservationsBody struct {
	HistoryQueryEnvelope

	Coverage   HistoryQueryBasicCoverage `json:"coverage"`
	Order      string                    `json:"order"                 enum:"ascending,descending"`
	Items      []historyObservationBody  `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}
type historyValueIntervalBody struct {
	Kind  string `json:"kind"  enum:"value"`
	From  string `json:"from"               format:"date-time"`
	To    string `json:"to"                 format:"date-time"`
	Value any    `json:"value"`
}
type historyUnknownIntervalBody struct {
	Kind   string `json:"kind"   enum:"unknown"`
	From   string `json:"from"                               format:"date-time"`
	To     string `json:"to"                                 format:"date-time"`
	Reason string `json:"reason" enum:"no_prior_observation"`
}
type historyTimelineBody struct {
	HistoryQueryEnvelope

	Coverage  historyTemporalCoverage `json:"coverage"`
	Intervals []json.RawMessage       `json:"intervals"`
}
type historySummaryBody struct {
	HistoryQueryEnvelope

	Coverage json.RawMessage            `json:"coverage"`
	Metrics  map[string]json.RawMessage `json:"metrics"`
}
type historySummaryBucketBody struct {
	Range    historyRangeBody           `json:"range"`
	Metrics  map[string]json.RawMessage `json:"metrics"`
	Coverage *historyDurationCoverage   `json:"coverage,omitempty"`
}
type historyNumericBucketBody struct {
	Range   historyRangeBody           `json:"range"`
	Metrics map[string]json.RawMessage `json:"metrics"`
}
type historyTemporalBucketBody struct {
	Range    historyRangeBody           `json:"range"`
	Metrics  map[string]json.RawMessage `json:"metrics"`
	Coverage historyDurationCoverage    `json:"coverage"`
}
type historyBucketedBody struct {
	HistoryQueryEnvelope

	Coverage json.RawMessage            `json:"coverage"`
	Bucket   historyBucketBody          `json:"bucket"`
	Buckets  []historySummaryBucketBody `json:"buckets"`
}
type historyTemporalBucketedBody struct {
	HistoryQueryEnvelope

	Coverage json.RawMessage             `json:"coverage"`
	Bucket   historyBucketBody           `json:"bucket"`
	Buckets  []historyTemporalBucketBody `json:"buckets"`
}
type historyProblemError struct {
	huma.ErrorModel

	Code string `json:"code,omitempty"`
}
type queryHistoryInput struct {
	EntityID string `path:"entity_id"`
	RawBody  []byte
}
type queryHistoryOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// historyQuerySchemas derives ordinary fields from DTOs and specifies the closed
// discriminated alternatives and arbitrary JSON values that tags cannot express.
//
//nolint:funlen,goconst // Keep this operation's closed publication visible together.
func historyQuerySchemas(r huma.Registry) (*huma.Schema, *huma.Schema) {
	schema := func(t reflect.Type) *huma.Schema {
		s := r.Schema(t, false, "")
		s.AdditionalProperties = false
		return s
	}
	rangeSchema := schema(reflect.TypeFor[historyRangeBody]())
	bucket := schema(reflect.TypeFor[historyBucketBody]())
	obsReq := schema(reflect.TypeFor[historyObservationRequest]())
	timelineReq := schema(reflect.TypeFor[historyTimelineRequest]())
	numeric := schema(reflect.TypeFor[historyNumericMetric]())
	duration := schema(reflect.TypeFor[historyDurationMetric]())
	transition := schema(reflect.TypeFor[historyTransitionMetric]())
	summaryReq := schema(reflect.TypeFor[historySummaryRequest]())
	summaryReq.Properties["metrics"] = &huma.Schema{
		Type:                 "object",
		MinProperties:        new(1),
		MaxProperties:        new(devices.HistoryMaxMetrics),
		AdditionalProperties: &huma.Schema{OneOf: []*huma.Schema{numeric, duration, transition}},
	}
	metricAlternatives := summaryReq.Properties["metrics"].AdditionalProperties.(*huma.Schema) //nolint:errcheck // Constructed immediately above.
	metricAlternatives.PrecomputeMessages()
	summaryReq.Properties["bucket"] = bucket
	request := schema(reflect.TypeFor[historyRequest]())
	request.Properties["range"] = rangeSchema
	request.Properties["select_expr"].Nullable = false
	request.Properties["result"] = &huma.Schema{OneOf: []*huma.Schema{obsReq, timelineReq, summaryReq}}
	request.PrecomputeMessages()
	basic := schema(reflect.TypeFor[HistoryQueryBasicCoverage]())
	temporal := schema(reflect.TypeFor[historyTemporalCoverage]())
	durationCoverage := schema(reflect.TypeFor[historyDurationCoverage]())
	observation := schema(reflect.TypeFor[historyObservationBody]())
	observation.Properties["value"] = &huma.Schema{}
	observation.Properties["source_updated_at"].Nullable = true
	observations := schema(reflect.TypeFor[historyObservationsBody]())
	observations.Properties["kind"] = &huma.Schema{Type: "string", Const: "observations"}
	observations.Properties["range"] = rangeSchema
	observations.Properties["coverage"] = basic
	observations.Properties["items"] = &huma.Schema{Type: "array", Items: observation}
	value := schema(reflect.TypeFor[historyValueIntervalBody]())
	value.Properties["value"] = &huma.Schema{}
	unknown := schema(reflect.TypeFor[historyUnknownIntervalBody]())
	timeline := schema(reflect.TypeFor[historyTimelineBody]())
	timeline.Properties["kind"] = &huma.Schema{Type: "string", Const: "timeline"}
	timeline.Properties["range"] = rangeSchema
	timeline.Properties["coverage"] = temporal
	timeline.Properties["intervals"] = &huma.Schema{
		Type:  "array",
		Items: &huma.Schema{OneOf: []*huma.Schema{value, unknown}},
	}
	metricValues := &huma.Schema{Type: "object", AdditionalProperties: &huma.Schema{Type: "number", Nullable: true}}
	coverage := &huma.Schema{OneOf: []*huma.Schema{basic, temporal}}
	summary := schema(reflect.TypeFor[historySummaryBody]())
	summary.Properties["kind"] = &huma.Schema{Type: "string", Const: "summary"}
	summary.Properties["range"] = rangeSchema
	summary.Properties["coverage"] = coverage
	summary.Properties["metrics"] = metricValues
	numericBucket := schema(reflect.TypeFor[historyNumericBucketBody]())
	numericBucket.Properties["range"] = rangeSchema
	numericBucket.Properties["metrics"] = metricValues
	temporalBucket := schema(reflect.TypeFor[historyTemporalBucketBody]())
	temporalBucket.Properties["range"] = rangeSchema
	temporalBucket.Properties["metrics"] = metricValues
	temporalBucket.Properties["coverage"] = durationCoverage
	bucketed := schema(reflect.TypeFor[historyBucketedBody]())
	bucketed.Properties["kind"] = &huma.Schema{Type: "string", Const: "summary"}
	bucketed.Properties["range"] = rangeSchema
	bucketed.Properties["coverage"] = basic
	bucketed.Properties["bucket"] = bucket
	bucketed.Properties["buckets"] = &huma.Schema{Type: "array", Items: numericBucket}
	temporalBucketed := schema(reflect.TypeFor[historyTemporalBucketedBody]())
	temporalBucketed.Properties["kind"] = &huma.Schema{Type: "string", Const: "summary"}
	temporalBucketed.Properties["range"] = rangeSchema
	temporalBucketed.Properties["coverage"] = temporal
	temporalBucketed.Properties["bucket"] = bucket
	temporalBucketed.Properties["buckets"] = &huma.Schema{Type: "array", Items: temporalBucket}
	return request, &huma.Schema{OneOf: []*huma.Schema{observations, timeline, summary, bucketed, temporalBucketed}}
}

//nolint:goconst // The operation advertises JSON request and response media.
func registerQueryEntityStateHistory(api huma.API, h *handler) {
	request, response := historyQuerySchemas(api.OpenAPI().Components.Schemas)
	problem := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[historyProblemError](), false, "")
	framework := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[huma.ErrorModel](), true, "")
	responses := map[string]*huma.Response{
		"200": {
			Description: "Query result",
			Content:     map[string]*huma.MediaType{"application/json": {Schema: response}},
		},
	}
	for _, status := range []int{400, 404, 413, 415, 422, 500, 503} {
		responses[strconv.Itoa(status)] = &huma.Response{
			Description: http.StatusText(status),
			Content: map[string]*huma.MediaType{
				"application/problem+json": {Schema: &huma.Schema{AnyOf: []*huma.Schema{problem, framework}}},
			},
		}
	}
	op := huma.Operation{
		OperationID:      "query-entity-state-history",
		Method:           http.MethodPost,
		Path:             "/entities/{entity_id}/state/history",
		Summary:          "Query an Entity's retained State history",
		Tags:             []string{entitiesTag},
		SkipValidateBody: true,
		Responses:        responses,
		RequestBody: &huma.RequestBody{
			Required: true,
			Content:  map[string]*huma.MediaType{"application/json": {Schema: request}},
		},
	}
	huma.Register(api, op, func(ctx context.Context, in *queryHistoryInput) (*queryHistoryOutput, error) {
		return h.queryEntityStateHistory(ctx, in, api.OpenAPI().Components.Schemas, request)
	})
	op.RequestBody.Content = map[string]*huma.MediaType{"application/json": {Schema: request}}
}

//nolint:modernize // Named embedded field clarifies the Huma problem extension.
func historyQueryProblem(status int, code, detail, field string) *historyProblemError {
	p := &historyProblemError{
		ErrorModel: huma.ErrorModel{Title: http.StatusText(status), Status: status, Detail: detail},
		Code:       code,
	}
	if field != "" {
		p.Errors = []*huma.ErrorDetail{{Location: "body." + field, Message: "Invalid query field."}}
	}
	return p
}

//nolint:gocognit,funlen // This boundary handles each request variant and failure class.
func (h *handler) queryEntityStateHistory(
	ctx context.Context,
	in *queryHistoryInput,
	registry huma.Registry,
	schema *huma.Schema,
) (*queryHistoryOutput, error) {
	id, err := devices.ParseEntityID(in.EntityID)
	if err != nil {
		return nil, historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid Entity ID.", "")
	}
	var raw any
	if !jsontext.Value(in.RawBody).IsValid() || json.Unmarshal(in.RawBody, &raw) != nil || !strictHistoryShape(raw) {
		return nil, historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "")
	}
	result := &huma.ValidateResult{}
	huma.Validate(registry, schema, &huma.PathBuffer{}, huma.ModeReadFromServer, raw, result)
	if len(result.Errors) > 0 {
		return nil, historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "")
	}
	var body historyRequest
	if json.Unmarshal(in.RawBody, &body) != nil {
		return nil, historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "")
	}
	from, e1 := time.Parse(time.RFC3339Nano, body.Range.From)
	to, e2 := time.Parse(time.RFC3339Nano, body.Range.To)
	if e1 != nil || e2 != nil {
		return nil, historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "range")
	}
	query := devices.HistoryQuery{
		EntityID:   id,
		Range:      devices.HistoryRange{From: from, To: to},
		SelectExpr: body.SelectExpr,
	}
	var kind struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(body.Result, &kind)
	switch kind.Kind {
	case "observations":
		var b struct {
			WhereExpr *string `json:"where_expr"`
			Order     string  `json:"order"`
			Limit     int     `json:"limit"`
			Cursor    string  `json:"cursor"`
		}
		_ = json.Unmarshal(body.Result, &b)
		query.Result = devices.HistoryObservations{
			WhereExpr: b.WhereExpr,
			Order:     devices.HistoryOrder(b.Order),
			Limit:     b.Limit,
			Cursor:    b.Cursor,
		}
	case "timeline":
		query.Result = devices.HistoryTimeline{}
	case "summary":
		var b historySummaryRequest
		_ = json.Unmarshal(body.Result, &b)
		s := devices.HistorySummary{Metrics: make(map[string]devices.HistoryMetric, len(b.Metrics))}
		if b.Bucket != nil {
			d, e := time.ParseDuration(b.Bucket.Duration)
			if e != nil {
				return nil, historyQueryProblem(
					http.StatusBadRequest,
					"invalid_query",
					"Invalid bucket duration.",
					"result.bucket.duration",
				)
			}
			s.Bucket = &devices.HistoryBucket{Duration: d, Alignment: b.Bucket.Alignment}
		}
		for name, data := range b.Metrics {
			var m struct {
				Operation string `json:"operation"`
				WhereExpr string `json:"where_expr"`
				Unit      string `json:"unit"`
			}
			_ = json.Unmarshal(data, &m)
			switch m.Operation {
			case "min":
				s.Metrics[name] = devices.HistoryMinimum{}
			case "max":
				s.Metrics[name] = devices.HistoryMaximum{}
			case "sample_mean":
				s.Metrics[name] = devices.HistorySampleMean{}
			case "duration_where":
				s.Metrics[name] = devices.HistoryDurationWhere{WhereExpr: m.WhereExpr, Unit: m.Unit}
			case "transitions_into":
				s.Metrics[name] = devices.HistoryTransitionsInto{WhereExpr: m.WhereExpr}
			}
		}
		query.Result = s
	}
	answer, err := h.devices.QueryHistory(ctx, query)
	if err != nil {
		if expectedRequestCancellation(ctx, err) {
			return nil, historyQueryProblem(http.StatusServiceUnavailable, "query_canceled", "Query canceled.", "")
		}
		if errors.Is(err, devices.ErrEntityNotFound) {
			return nil, historyQueryProblem(http.StatusNotFound, "entity_not_found", "Entity not found.", "")
		}
		if q, ok := errors.AsType[*devices.HistoryQueryError](err); ok {
			status := http.StatusUnprocessableEntity
			switch q.Code {
			case "invalid_cursor":
				status = http.StatusBadRequest
			case "invalid_query",
				"unsupported_source",
				"query_limit",
				"expression_type",
				"expression_limit",
				"expression_value_invalid",
				"expression_evaluation":
			default:
				h.logInternalFailure(ctx, "query-entity-state-history", "service", err)
				return nil, historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
			}
			return nil, historyQueryProblem(status, q.Code, "History query failed.", q.Field)
		}
		h.logInternalFailure(ctx, "query-entity-state-history", "service", err)
		return nil, historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
	}
	mapped, err := historyQueryResponse(answer)
	if err != nil {
		h.logInternalFailure(ctx, "query-entity-state-history", "mapping", err)
		return nil, historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
	}
	encoded, err := json.Marshal(mapped)
	if err != nil {
		h.logInternalFailure(ctx, "query-entity-state-history", "mapping", err)
		return nil, historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
	}
	if len(encoded) > devices.HistoryMaxResultBytes {
		return nil, historyQueryProblem(
			http.StatusUnprocessableEntity,
			"query_limit",
			"History query result exceeds the limit.",
			"",
		)
	}
	return &queryHistoryOutput{ContentType: "application/json", Body: encoded}, nil
}

//nolint:funlen // Closed variants are mapped together to preserve their envelope.
func historyQueryResponse(result devices.HistoryQueryResult) (map[string]any, error) {
	rangeBody := func(r devices.HistoryRange) historyRangeBody {
		return historyRangeBody{From: formatTime(r.From), To: formatTime(r.To)}
	}
	coverage := func(c *devices.HistoryTemporalCoverage) map[string]any {
		v := map[string]any{"basis": "retained_observations", "completeness": "unverified"}
		if c != nil {
			v["seed_available"] = c.SeedAvailable
			v["inferred_known_seconds"] = c.InferredKnownSeconds
			v["unknown_seconds"] = c.UnknownSeconds
		}
		return v
	}
	out := map[string]any{
		"entity_id": string(result.EntityID),
		"range":     rangeBody(result.Range),
		"time_axis": "observed_at",
	}
	switch data := result.Data.(type) {
	case devices.HistoryObservationsResult:
		out["kind"] = "observations"
		out["coverage"] = coverage(nil)
		out["order"] = string(data.Order)
		items := make([]historyObservationBody, 0, len(data.Items))
		for _, item := range data.Items {
			var updated *string
			if item.SourceUpdatedAt != nil {
				v := formatTime(*item.SourceUpdatedAt)
				updated = &v
			}
			items = append(
				items,
				historyObservationBody{
					ObservationID:     string(item.ObservationID),
					ObservedAt:        formatTime(item.ObservedAt),
					AdapterReceivedAt: formatTime(item.AdapterReceivedAt),
					SourceUpdatedAt:   updated,
					Disposition:       string(item.Disposition),
					Value:             item.Value,
				},
			)
		}
		out["items"] = items
		if data.NextCursor != "" {
			out["next_cursor"] = data.NextCursor
		}
	case devices.HistoryTimelineResult:
		out["kind"] = "timeline"
		out["coverage"] = coverage(&data.Coverage)
		intervals := make([]any, 0, len(data.Intervals))
		for _, interval := range data.Intervals {
			switch v := interval.(type) {
			case devices.HistoryValueInterval:
				intervals = append(
					intervals,
					map[string]any{
						"kind":  "value",
						"from":  formatTime(v.Range.From),
						"to":    formatTime(v.Range.To),
						"value": v.Value,
					},
				)
			case devices.HistoryUnknownInterval:
				intervals = append(
					intervals,
					map[string]any{
						"kind":   "unknown",
						"from":   formatTime(v.Range.From),
						"to":     formatTime(v.Range.To),
						"reason": "no_prior_observation",
					},
				)
			default:
				return nil, fmt.Errorf("unknown interval %T", interval)
			}
		}
		out["intervals"] = intervals
	case devices.HistorySummaryResult:
		out["kind"] = "summary"
		out["coverage"] = coverage(data.Coverage)
		out["metrics"] = data.Metrics
	case devices.HistoryBucketedSummaryResult:
		out["kind"] = "summary"
		out["coverage"] = coverage(data.Coverage)
		out["bucket"] = historyBucketBody{Duration: data.Bucket.Duration.String(), Alignment: data.Bucket.Alignment}
		buckets := make([]any, 0, len(data.Buckets))
		for _, b := range data.Buckets {
			item := map[string]any{"range": rangeBody(b.Range), "metrics": b.Metrics}
			if b.Coverage != nil {
				item["coverage"] = historyDurationCoverage{
					InferredKnownSeconds: b.Coverage.InferredKnownSeconds,
					UnknownSeconds:       b.Coverage.UnknownSeconds,
				}
			}
			buckets = append(buckets, item)
		}
		out["buckets"] = buckets
	default:
		return nil, fmt.Errorf("unknown result %T", result.Data)
	}
	return out, nil
}

// Huma intentionally accepts null optional properties and case-insensitive keys;
// this operation's request contract permits neither.
//
//nolint:gocognit // Shape checks mirror the nested request contract.
func strictHistoryShape(raw any) bool {
	object := func(v any, allowed ...string) (map[string]any, bool) {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		for key, value := range m {
			if !slices.Contains(allowed, key) || value == nil {
				return nil, false
			}
		}
		return m, true
	}
	root, ok := object(raw, "range", "select_expr", "result")
	if !ok {
		return false
	}
	if _, ok = object(root["range"], "from", "to"); !ok {
		return false
	}
	result, ok := object(root["result"], "kind", "where_expr", "order", "limit", "cursor", "metrics", "bucket")
	if !ok {
		return false
	}
	if b, exists := result["bucket"]; exists {
		if _, ok = object(b, "duration", "alignment"); !ok {
			return false
		}
	}
	if m, exists := result["metrics"]; exists {
		metrics, valid := m.(map[string]any)
		if !valid {
			return false
		}
		for _, metric := range metrics {
			if _, ok = object(metric, "operation", "where_expr", "unit"); !ok {
				return false
			}
		}
	}
	return true
}
