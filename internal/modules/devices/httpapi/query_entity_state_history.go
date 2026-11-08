package httpapi

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
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
type historyMetricBody struct {
	Operation string `json:"operation" enum:"min,max,sample_mean"`
}
type historySummaryRequest struct {
	Kind    string                       `json:"kind"             enum:"summary"`
	Metrics map[string]historyMetricBody `json:"metrics"`
	Bucket  *historyBucketBody           `json:"bucket,omitempty"`
	Nulls   string                       `json:"nulls,omitempty"  enum:"reject,gap" default:"reject"`
}
type historyRequest struct {
	Range       historyRangeBody      `json:"range"`
	SelectField string                `json:"select_field" maxLength:"64" pattern:"^(/[a-z][a-z0-9_]*)?$"`
	Result      historySummaryRequest `json:"result"`
}

type HistoryQueryBasicCoverage struct {
	Basis        string `json:"basis"        enum:"retained_observations"`
	Completeness string `json:"completeness" enum:"unverified"`
}
type HistoryQueryEnvelope struct {
	Kind     string                    `json:"kind"`
	EntityID string                    `json:"entity_id"`
	Range    historyRangeBody          `json:"range"`
	TimeAxis string                    `json:"time_axis" enum:"observed_at"`
	Coverage HistoryQueryBasicCoverage `json:"coverage"`
}
type historySummaryBody struct {
	HistoryQueryEnvelope

	Metrics map[string]json.RawMessage `json:"metrics"`
}
type historySummaryBucketBody struct {
	Range   historyRangeBody           `json:"range"`
	Metrics map[string]json.RawMessage `json:"metrics"`
}
type historyBucketedBody struct {
	HistoryQueryEnvelope

	Bucket  historyBucketBody          `json:"bucket"`
	Buckets []historySummaryBucketBody `json:"buckets"`
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
	s.Properties["metrics"] = &huma.Schema{
		Type:                 "object",
		MinProperties:        new(1),
		MaxProperties:        new(devices.HistoryMaxMetrics),
		AdditionalProperties: historySchema[historyMetricBody](r),
	}
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	return s
}

func (historyRequest) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["result"] = historySchema[historySummaryRequest](r)
	return s
}

func historyMetricValuesSchema() *huma.Schema {
	return &huma.Schema{Type: "object", AdditionalProperties: &huma.Schema{Type: "number", Nullable: true}}
}

func historyEnvelopeSchema(r huma.Registry, s *huma.Schema) {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historySummaryKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[HistoryQueryBasicCoverage](r)
}

func (historySummaryBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	historyEnvelopeSchema(r, s)
	s.Properties["metrics"] = historyMetricValuesSchema()
	return s
}

func (historySummaryBucketBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["metrics"] = historyMetricValuesSchema()
	return s
}

func (historyBucketedBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	historyEnvelopeSchema(r, s)
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	s.Properties["buckets"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[historySummaryBucketBody](r)}
	return s
}

func historyQuerySchemas(r huma.Registry) (*huma.Schema, *huma.Schema) {
	return historySchema[historyRequest](r), &huma.Schema{OneOf: []*huma.Schema{
		historySchema[historySummaryBody](r), historySchema[historyBucketedBody](r),
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
		Summary:          "Summarize a numeric field of an Entity's retained State history",
		Tags:             []string{entitiesTag},
		SkipValidateBody: true,
		MaxBodyBytes:     historyRequestMaxBytes,
		BodyReadTimeout:  historyRequestReadTimeout,
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

func decodeHistoryQuery(
	in *queryHistoryInput,
	registry huma.Registry,
	schema *huma.Schema,
) (devices.HistoryQuery, error) {
	invalidBody := historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "")
	mediaType, _, err := mime.ParseMediaType(in.ContentType)
	if err != nil || mediaType != "application/json" {
		return devices.HistoryQuery{}, historyQueryProblem(
			http.StatusUnsupportedMediaType, "invalid_query", "Expected application/json.", "",
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
	body := historyRequest{Result: historySummaryRequest{Nulls: string(devices.HistoryNullReject)}}
	if json.Unmarshal(in.RawBody, &body) != nil {
		return devices.HistoryQuery{}, invalidBody
	}
	from, fromErr := time.Parse(time.RFC3339Nano, body.Range.From)
	to, toErr := time.Parse(time.RFC3339Nano, body.Range.To)
	if fromErr != nil || toErr != nil {
		return devices.HistoryQuery{}, historyQueryProblem(
			http.StatusBadRequest, "invalid_query", "Invalid query body.", "range",
		)
	}
	query := devices.HistoryQuery{
		EntityID: id,
		Range:    devices.HistoryRange{From: from.UTC(), To: to.UTC()},
		Field:    body.SelectField,
		Metrics:  make(map[string]devices.HistoryMetric, len(body.Result.Metrics)),
		Nulls:    devices.HistoryNullPolicy(body.Result.Nulls),
	}
	if b := body.Result.Bucket; b != nil {
		duration, parseErr := time.ParseDuration(b.Duration)
		if parseErr != nil {
			return devices.HistoryQuery{}, historyQueryProblem(
				http.StatusBadRequest, "invalid_query", "Invalid bucket duration.", "result.bucket.duration",
			)
		}
		query.Bucket = &devices.HistoryBucket{Duration: duration, Alignment: b.Alignment}
	}
	for name, metric := range body.Result.Metrics {
		switch metric.Operation {
		case "min":
			query.Metrics[name] = devices.HistoryMinimum{}
		case "max":
			query.Metrics[name] = devices.HistoryMaximum{}
		case "sample_mean":
			query.Metrics[name] = devices.HistorySampleMean{}
		default:
			return devices.HistoryQuery{}, invalidBody
		}
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
			http.StatusUnprocessableEntity, "query_limit", "History query result exceeds the limit.", "",
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
		case "invalid_query", "unsupported_source", "query_limit", "value_type":
			return historyQueryProblem(http.StatusUnprocessableEntity, q.Code, "History query failed.", q.Field)
		}
	}
	h.logInternalFailure(ctx, "query-entity-state-history", "service", err)
	return historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
}

func historyQueryResponse(result devices.HistoryQueryResult) (any, error) {
	rangeBody := func(r devices.HistoryRange) historyRangeBody {
		return historyRangeBody{From: formatTime(r.From), To: formatTime(r.To)}
	}
	envelope := HistoryQueryEnvelope{
		Kind:     historySummaryKind,
		EntityID: string(result.EntityID),
		Range:    rangeBody(result.Range),
		TimeAxis: "observed_at",
		Coverage: HistoryQueryBasicCoverage{Basis: "retained_observations", Completeness: "unverified"},
	}
	switch data := result.Data.(type) {
	case devices.HistorySummaryResult:
		return historySummaryBody{HistoryQueryEnvelope: envelope, Metrics: data.Metrics}, nil
	case devices.HistoryBucketedSummaryResult:
		buckets := make([]historySummaryBucketBody, 0, len(data.Buckets))
		for _, b := range data.Buckets {
			buckets = append(buckets, historySummaryBucketBody{Range: rangeBody(b.Range), Metrics: b.Metrics})
		}
		return historyBucketedBody{
			HistoryQueryEnvelope: envelope,
			Bucket: historyBucketBody{
				Duration:  data.Bucket.Duration.String(),
				Alignment: data.Bucket.Alignment,
			},
			Buckets: buckets,
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
	root, ok := object(raw, "range", "select_field", "result")
	if !ok {
		return false
	}
	if _, ok = object(root["range"], "from", "to"); !ok {
		return false
	}
	result, ok := object(root["result"], "kind", "metrics", "bucket", "nulls")
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
			if _, ok = object(metric, "operation"); !ok {
				return false
			}
		}
	}
	return true
}
