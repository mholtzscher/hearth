package httpapi

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"math/big"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	historyRequestMaxBytes    = 1024 * 1024
	historyRequestReadTimeout = 5 * time.Second
	historyObservationsKind   = "observations"
	historyTimelineKind       = "timeline"
	historySummaryKind        = "summary"
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
	Kind      string       `json:"kind"                 enum:"observations"`
	WhereExpr *string      `json:"where_expr,omitempty"                             maxLength:"4096"`
	Order     string       `json:"order,omitempty"      enum:"ascending,descending"`
	Limit     historyLimit `json:"limit,omitempty"                                                   minimum:"1" maximum:"200"`
	Cursor    string       `json:"cursor,omitempty"`
}

// historyLimit accepts any JSON number whose exact value is an allowed integer.
type historyLimit int

func (historyLimit) Schema(_ huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeInteger, Format: "int64"}
}

func (l *historyLimit) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || (data[0] != '-' && (data[0] < '0' || data[0] > '9')) {
		return errors.New("invalid history limit")
	}
	value, ok := new(big.Rat).SetString(string(data))
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return errors.New("invalid history limit")
	}
	n := value.Num().Int64()
	if n < 1 || n > 200 {
		return errors.New("invalid history limit")
	}
	*l = historyLimit(n)
	return nil
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
	SelectExpr *string          `json:"select_expr,omitempty" minLength:"1" maxLength:"4096"`
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
type historyCoverage interface{ historyCoverage() }

func (HistoryQueryBasicCoverage) historyCoverage() {}
func (historyTemporalCoverage) historyCoverage()   {}

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
	ObservationID     string          `json:"observation_id"`
	ObservedAt        string          `json:"observed_at"         format:"date-time"`
	AdapterReceivedAt string          `json:"adapter_received_at" format:"date-time"`
	SourceUpdatedAt   *string         `json:"source_updated_at"`
	Disposition       string          `json:"disposition"                            enum:"applied,unchanged"`
	Value             json.RawMessage `json:"value"`
}
type historyObservationsBody struct {
	HistoryQueryEnvelope

	Coverage   HistoryQueryBasicCoverage `json:"coverage"`
	Order      string                    `json:"order"                 enum:"ascending,descending"`
	Items      []historyObservationBody  `json:"items"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}
type historyValueIntervalBody struct {
	Kind  string          `json:"kind"  enum:"value"`
	From  string          `json:"from"               format:"date-time"`
	To    string          `json:"to"                 format:"date-time"`
	Value json.RawMessage `json:"value"`
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
	Intervals []any                   `json:"intervals"`
}
type historySummaryBody struct {
	HistoryQueryEnvelope

	Coverage historyCoverage            `json:"coverage"`
	Metrics  map[string]json.RawMessage `json:"metrics"`
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
type historyNumericBucketedBody struct {
	HistoryQueryEnvelope

	Coverage HistoryQueryBasicCoverage  `json:"coverage"`
	Bucket   historyBucketBody          `json:"bucket"`
	Buckets  []historyNumericBucketBody `json:"buckets"`
}
type historyTemporalBucketedBody struct {
	HistoryQueryEnvelope

	Coverage historyTemporalCoverage     `json:"coverage"`
	Bucket   historyBucketBody           `json:"bucket"`
	Buckets  []historyTemporalBucketBody `json:"buckets"`
}
type historyProblemError struct {
	huma.ErrorModel

	Code string `json:"code,omitempty"`
}
type queryHistoryInput struct {
	EntityID    string `path:"entity_id"`
	ContentType string `                 header:"Content-Type"`
	RawBody     []byte
}
type queryHistoryOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// historySchema derives a closed inline object without modifying registry references.
func historySchema[T any](r huma.Registry) *huma.Schema {
	s := r.Schema(reflect.TypeFor[T](), false, "")
	s.AdditionalProperties = false
	return s
}

func (historySummaryRequest) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	metricAlternatives := &huma.Schema{OneOf: []*huma.Schema{
		historySchema[historyNumericMetric](r),
		historySchema[historyDurationMetric](r),
		historySchema[historyTransitionMetric](r),
	}}
	metricAlternatives.PrecomputeMessages()
	s.Properties["metrics"] = &huma.Schema{
		Type:                 "object",
		MinProperties:        new(1),
		MaxProperties:        new(devices.HistoryMaxMetrics),
		AdditionalProperties: metricAlternatives,
	}
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	return s
}

func (historyObservationRequest) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["where_expr"].Nullable = false
	return s
}

func (historyRequest) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["select_expr"].Nullable = false
	s.Properties["result"] = &huma.Schema{OneOf: []*huma.Schema{
		historySchema[historyObservationRequest](r),
		historySchema[historyTimelineRequest](r),
		historySchema[historySummaryRequest](r),
	}}
	return s
}

func (historyObservationBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["value"] = &huma.Schema{}
	s.Properties["source_updated_at"].Nullable = true
	return s
}

func (historyObservationsBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historyObservationsKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[HistoryQueryBasicCoverage](r)
	s.Properties["items"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[historyObservationBody](r)}
	return s
}

func (historyTimelineBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historyTimelineKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[historyTemporalCoverage](r)
	s.Properties["intervals"] = &huma.Schema{Type: huma.TypeArray, Items: &huma.Schema{OneOf: []*huma.Schema{
		historySchema[historyValueIntervalBody](r), historySchema[historyUnknownIntervalBody](r),
	}}}
	return s
}

func historyMetricValuesSchema() *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: &huma.Schema{Type: "number", Nullable: true}}
}

func (historySummaryBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historySummaryKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = &huma.Schema{
		OneOf: []*huma.Schema{historySchema[HistoryQueryBasicCoverage](r), historySchema[historyTemporalCoverage](r)},
	}
	s.Properties["metrics"] = historyMetricValuesSchema()
	return s
}

func (historyNumericBucketBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["metrics"] = historyMetricValuesSchema()
	return s
}

func (historyTemporalBucketBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["metrics"] = historyMetricValuesSchema()
	s.Properties["coverage"] = historySchema[historyDurationCoverage](r)
	return s
}

func (historyNumericBucketedBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historySummaryKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[HistoryQueryBasicCoverage](r)
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	s.Properties["buckets"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[historyNumericBucketBody](r)}
	return s
}

func (historyTemporalBucketedBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historySummaryKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[historyTemporalCoverage](r)
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	s.Properties["buckets"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[historyTemporalBucketBody](r)}
	return s
}

func historyQuerySchemas(r huma.Registry) (*huma.Schema, *huma.Schema) {
	return historySchema[historyRequest](r), &huma.Schema{OneOf: []*huma.Schema{
		historySchema[historyObservationsBody](r), historySchema[historyTimelineBody](r),
		historySchema[historySummaryBody](r), historySchema[historyNumericBucketedBody](r),
		historySchema[historyTemporalBucketedBody](r),
	}}
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
	for _, status := range []int{400, 404, 408, 413, 415, 422, 500, 503} {
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
		// Huma rejects at the read cap, so one extra byte makes the limit inclusive.
		MaxBodyBytes:    historyRequestMaxBytes + 1,
		BodyReadTimeout: historyRequestReadTimeout,
		Responses:       responses,
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

//nolint:cyclop,gocognit,funlen // The decoder owns validation and conversion of every request variant.
func decodeHistoryQuery(
	in *queryHistoryInput,
	registry huma.Registry,
	schema *huma.Schema,
) (devices.HistoryQuery, error) {
	invalidBody := historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "")
	mediaType, _, err := mime.ParseMediaType(in.ContentType)
	if err != nil || mediaType != "application/json" {
		return devices.HistoryQuery{}, historyQueryProblem(
			http.StatusUnsupportedMediaType,
			"invalid_query",
			"Expected application/json.",
			"",
		)
	}
	id, err := devices.ParseEntityID(in.EntityID)
	if err != nil {
		return devices.HistoryQuery{}, historyQueryProblem(
			http.StatusBadRequest,
			"invalid_query",
			"Invalid Entity ID.",
			"",
		)
	}
	var raw any
	if !jsontext.Value(in.RawBody).IsValid() || json.Unmarshal(in.RawBody, &raw) != nil || !strictHistoryShape(raw) {
		return devices.HistoryQuery{}, invalidBody
	}
	result := &huma.ValidateResult{}
	huma.Validate(registry, schema, &huma.PathBuffer{}, huma.ModeReadFromServer, raw, result)
	if len(result.Errors) > 0 {
		return devices.HistoryQuery{}, invalidBody
	}
	var body historyRequest
	if json.Unmarshal(in.RawBody, &body) != nil {
		return devices.HistoryQuery{}, invalidBody
	}
	from, e1 := time.Parse(time.RFC3339Nano, body.Range.From)
	to, e2 := time.Parse(time.RFC3339Nano, body.Range.To)
	if e1 != nil || e2 != nil {
		return devices.HistoryQuery{}, historyQueryProblem(
			http.StatusBadRequest,
			"invalid_query",
			"Invalid query body.",
			"range",
		)
	}
	query := devices.HistoryQuery{
		EntityID:   id,
		Range:      devices.HistoryRange{From: from, To: to},
		SelectExpr: body.SelectExpr,
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return devices.HistoryQuery{}, invalidBody
	}
	resultObject, ok := root["result"].(map[string]any)
	if !ok {
		return devices.HistoryQuery{}, invalidBody
	}
	kind, ok := resultObject["kind"].(string)
	if !ok {
		return devices.HistoryQuery{}, invalidBody
	}
	switch kind {
	case historyObservationsKind:
		var b historyObservationRequest
		if json.Unmarshal(body.Result, &b) != nil {
			return devices.HistoryQuery{}, invalidBody
		}
		query.Result = devices.HistoryObservations{
			WhereExpr: b.WhereExpr,
			Order:     devices.HistoryOrder(b.Order),
			Limit:     int(b.Limit),
			Cursor:    b.Cursor,
		}
	case historyTimelineKind:
		query.Result = devices.HistoryTimeline{}
	case historySummaryKind:
		var b historySummaryRequest
		if json.Unmarshal(body.Result, &b) != nil {
			return devices.HistoryQuery{}, invalidBody
		}
		s := devices.HistorySummary{Metrics: make(map[string]devices.HistoryMetric, len(b.Metrics))}
		if b.Bucket != nil {
			d, e := time.ParseDuration(b.Bucket.Duration)
			if e != nil {
				return devices.HistoryQuery{}, historyQueryProblem(
					http.StatusBadRequest,
					"invalid_query",
					"Invalid bucket duration.",
					"result.bucket.duration",
				)
			}
			s.Bucket = &devices.HistoryBucket{Duration: d, Alignment: b.Bucket.Alignment}
		}
		for name, data := range b.Metrics {
			// Numeric metrics contain only the operation, which also identifies
			// the DTO used to decode each temporal metric below.
			var metric historyNumericMetric
			if json.Unmarshal(data, &metric) != nil {
				return devices.HistoryQuery{}, invalidBody
			}
			switch metric.Operation {
			case "min":
				s.Metrics[name] = devices.HistoryMinimum{}
			case "max":
				s.Metrics[name] = devices.HistoryMaximum{}
			case "sample_mean":
				s.Metrics[name] = devices.HistorySampleMean{}
			case "duration_where":
				var duration historyDurationMetric
				if json.Unmarshal(data, &duration) != nil {
					return devices.HistoryQuery{}, invalidBody
				}
				s.Metrics[name] = devices.HistoryDurationWhere{WhereExpr: duration.WhereExpr, Unit: duration.Unit}
			case "transitions_into":
				var transition historyTransitionMetric
				if json.Unmarshal(data, &transition) != nil {
					return devices.HistoryQuery{}, invalidBody
				}
				s.Metrics[name] = devices.HistoryTransitionsInto{WhereExpr: transition.WhereExpr}
			default:
				return devices.HistoryQuery{}, invalidBody
			}
		}
		query.Result = s
	default:
		return devices.HistoryQuery{}, invalidBody
	}
	return query, nil
}

func (h *handler) queryEntityStateHistory(
	ctx context.Context,
	in *queryHistoryInput,
	registry huma.Registry,
	schema *huma.Schema,
) (*queryHistoryOutput, error) {
	query, err := decodeHistoryQuery(in, registry, schema)
	if err != nil {
		return nil, err
	}
	answer, err := h.devices.QueryHistory(ctx, query)
	if err != nil {
		return nil, h.historyQueryFailure(ctx, err)
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

func (h *handler) historyQueryFailure(ctx context.Context, err error) error {
	if expectedRequestCancellation(ctx, err) {
		return historyQueryProblem(http.StatusServiceUnavailable, "query_canceled", "Query canceled.", "")
	}
	if errors.Is(err, devices.ErrEntityNotFound) {
		return historyQueryProblem(http.StatusNotFound, "entity_not_found", "Entity not found.", "")
	}
	if q, ok := errors.AsType[*devices.HistoryQueryError](err); ok {
		switch q.Code {
		case "invalid_cursor":
			return historyQueryProblem(http.StatusBadRequest, q.Code, "History query failed.", q.Field)
		case "invalid_query", "unsupported_source", "query_limit", "expression_type",
			"expression_limit", "expression_value_invalid", "expression_evaluation":
			return historyQueryProblem(http.StatusUnprocessableEntity, q.Code, "History query failed.", q.Field)
		}
	}
	h.logInternalFailure(ctx, "query-entity-state-history", "service", err)
	return historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
}

//nolint:funlen,gocognit // Closed variants and their coverage checks share the response envelope.
func historyQueryResponse(result devices.HistoryQueryResult) (any, error) {
	rangeBody := func(r devices.HistoryRange) historyRangeBody {
		return historyRangeBody{From: formatTime(r.From), To: formatTime(r.To)}
	}
	basic := HistoryQueryBasicCoverage{Basis: "retained_observations", Completeness: "unverified"}
	temporalCoverage := func(c devices.HistoryTemporalCoverage) historyTemporalCoverage {
		return historyTemporalCoverage{
			HistoryQueryBasicCoverage: basic,
			SeedAvailable:             c.SeedAvailable,
			InferredKnownSeconds:      c.InferredKnownSeconds,
			UnknownSeconds:            c.UnknownSeconds,
		}
	}
	coverage := func(c *devices.HistoryTemporalCoverage) historyCoverage {
		if c == nil {
			return basic
		}
		return temporalCoverage(*c)
	}
	envelope := HistoryQueryEnvelope{
		EntityID: string(result.EntityID),
		Range:    rangeBody(result.Range),
		TimeAxis: "observed_at",
	}
	switch data := result.Data.(type) {
	case devices.HistoryObservationsResult:
		envelope.Kind = historyObservationsKind
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
		return historyObservationsBody{
			HistoryQueryEnvelope: envelope,
			Coverage:             basic,
			Order:                string(data.Order),
			Items:                items,
			NextCursor:           data.NextCursor,
		}, nil
	case devices.HistoryTimelineResult:
		envelope.Kind = historyTimelineKind
		intervals := make([]any, 0, len(data.Intervals))
		for _, interval := range data.Intervals {
			switch v := interval.(type) {
			case devices.HistoryValueInterval:
				intervals = append(
					intervals,
					historyValueIntervalBody{
						Kind:  "value",
						From:  formatTime(v.Range.From),
						To:    formatTime(v.Range.To),
						Value: v.Value,
					},
				)
			case devices.HistoryUnknownInterval:
				intervals = append(
					intervals,
					historyUnknownIntervalBody{
						Kind:   "unknown",
						From:   formatTime(v.Range.From),
						To:     formatTime(v.Range.To),
						Reason: "no_prior_observation",
					},
				)
			default:
				return nil, fmt.Errorf("unknown interval %T", interval)
			}
		}
		return historyTimelineBody{
			HistoryQueryEnvelope: envelope,
			Coverage:             temporalCoverage(data.Coverage),
			Intervals:            intervals,
		}, nil
	case devices.HistorySummaryResult:
		envelope.Kind = historySummaryKind
		return historySummaryBody{
			HistoryQueryEnvelope: envelope,
			Coverage:             coverage(data.Coverage),
			Metrics:              data.Metrics,
		}, nil
	case devices.HistoryBucketedSummaryResult:
		envelope.Kind = historySummaryKind
		bucket := historyBucketBody{Duration: data.Bucket.Duration.String(), Alignment: data.Bucket.Alignment}
		if data.Coverage == nil {
			buckets := make([]historyNumericBucketBody, 0, len(data.Buckets))
			for _, b := range data.Buckets {
				if b.Coverage != nil {
					return nil, errors.New("numeric summary bucket has temporal coverage")
				}
				buckets = append(buckets, historyNumericBucketBody{Range: rangeBody(b.Range), Metrics: b.Metrics})
			}
			return historyNumericBucketedBody{
				HistoryQueryEnvelope: envelope, Coverage: basic, Bucket: bucket, Buckets: buckets,
			}, nil
		}
		buckets := make([]historyTemporalBucketBody, 0, len(data.Buckets))
		for _, b := range data.Buckets {
			if b.Coverage == nil {
				return nil, errors.New("temporal summary bucket lacks coverage")
			}
			buckets = append(buckets, historyTemporalBucketBody{
				Range: rangeBody(b.Range), Metrics: b.Metrics,
				Coverage: historyDurationCoverage{
					InferredKnownSeconds: b.Coverage.InferredKnownSeconds,
					UnknownSeconds:       b.Coverage.UnknownSeconds,
				},
			})
		}
		return historyTemporalBucketedBody{
			HistoryQueryEnvelope: envelope,
			Coverage:             temporalCoverage(*data.Coverage),
			Bucket:               bucket,
			Buckets:              buckets,
		}, nil
	default:
		return nil, fmt.Errorf("unknown result %T", result.Data)
	}
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
