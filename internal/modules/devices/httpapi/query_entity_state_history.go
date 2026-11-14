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
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	historyRequestMaxBytes    = devices.HistoryMaxRequestBytes
	historyRequestReadTimeout = 5 * time.Second
	historySummaryKind        = "summary"
	historyTimeAxis           = "observed_at"
	historyCompleteness       = "unverified"
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
type PairedHistoryEnvelope struct {
	Kind     string                    `json:"kind"`
	EntityID string                    `json:"entity_id"`
	Range    historyRangeBody          `json:"range"`
	TimeAxis string                    `json:"time_axis" enum:"observed_at"`
	Coverage HistoryQueryBasicCoverage `json:"coverage"`
}

type canonicalHistoryMetrics struct {
	Unit    string                     `json:"unit"`
	Metrics map[string]json.RawMessage `json:"metrics"`
}

type displayHistoryMetrics struct {
	Unit           string                     `json:"unit"`
	Label          string                     `json:"label"`
	FractionDigits int                        `json:"fraction_digits"`
	Metrics        map[string]json.RawMessage `json:"metrics"`
}

type pairedHistorySummary struct {
	PairedHistoryEnvelope

	Canonical canonicalHistoryMetrics `json:"canonical"`
	Display   displayHistoryMetrics   `json:"display"`
}

type pairedHistoryBucket struct {
	Range     historyRangeBody        `json:"range"`
	Canonical canonicalHistoryMetrics `json:"canonical"`
	Display   displayHistoryMetrics   `json:"display"`
}

type pairedHistoryBucketed struct {
	PairedHistoryEnvelope

	Bucket  historyBucketBody     `json:"bucket"`
	Buckets []pairedHistoryBucket `json:"buckets"`
}

func (canonicalHistoryMetrics) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["metrics"] = historyMetricValuesSchema()
	s.AdditionalProperties = false
	return s
}

func (displayHistoryMetrics) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["metrics"] = historyMetricValuesSchema()
	s.AdditionalProperties = false
	return s
}

func (pairedHistorySummary) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	historyEnvelopeSchema(r, s)
	return s
}

func (pairedHistoryBucket) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	return s
}

func (pairedHistoryBucketed) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	historyEnvelopeSchema(r, s)
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	s.Properties["buckets"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[pairedHistoryBucket](r)}
	return s
}

func pairedHistoryResponse(
	canonical, display devices.HistoryQueryResult,
	canonicalQuantity, displayQuantity devices.QuantityMetadata,
) (any, error) {
	toRange := func(r devices.HistoryRange) historyRangeBody {
		return historyRangeBody{From: formatTime(r.From), To: formatTime(r.To)}
	}
	envelope := PairedHistoryEnvelope{
		Kind:     historySummaryKind,
		EntityID: string(canonical.EntityID),
		Range:    toRange(canonical.Range),
		TimeAxis: historyTimeAxis,
		Coverage: HistoryQueryBasicCoverage{Basis: "retained_observations", Completeness: historyCompleteness},
	}
	canonicalMetrics := func(metrics devices.HistoryMetricValues) canonicalHistoryMetrics {
		return canonicalHistoryMetrics{Unit: canonicalQuantity.Unit, Metrics: metrics}
	}
	displayMetrics := func(metrics devices.HistoryMetricValues) displayHistoryMetrics {
		return displayHistoryMetrics{
			Unit: displayQuantity.Unit, Label: displayQuantity.Label,
			FractionDigits: displayQuantity.FractionDigits, Metrics: metrics,
		}
	}
	switch c := canonical.Data.(type) {
	case devices.HistorySummaryResult:
		d, ok := display.Data.(devices.HistorySummaryResult)
		if !ok {
			return nil, fmt.Errorf("inconsistent history projection %T", display.Data)
		}
		return pairedHistorySummary{envelope, canonicalMetrics(c.Metrics), displayMetrics(d.Metrics)}, nil
	case devices.HistoryBucketedSummaryResult:
		d, ok := display.Data.(devices.HistoryBucketedSummaryResult)
		if !ok || len(c.Buckets) != len(d.Buckets) {
			return nil, fmt.Errorf("inconsistent history projection %T", display.Data)
		}
		buckets := make([]pairedHistoryBucket, 0, len(c.Buckets))
		for i, bucket := range c.Buckets {
			buckets = append(buckets, pairedHistoryBucket{
				Range:     toRange(bucket.Range),
				Canonical: canonicalMetrics(bucket.Metrics),
				Display:   displayMetrics(d.Buckets[i].Metrics),
			})
		}
		return pairedHistoryBucketed{
			PairedHistoryEnvelope: envelope,
			Bucket: historyBucketBody{
				Duration:  c.Bucket.Duration.String(),
				Alignment: c.Bucket.Alignment,
			},
			Buckets: buckets,
		}, nil
	default:
		return nil, fmt.Errorf("unknown result %T", canonical.Data)
	}
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
		Type:                 huma.TypeObject,
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
	return &huma.Schema{
		Type:                 huma.TypeObject,
		AdditionalProperties: &huma.Schema{Type: huma.TypeNumber, Nullable: true},
	}
}

func historyEnvelopeSchema(r huma.Registry, s *huma.Schema) {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: historySummaryKind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[HistoryQueryBasicCoverage](r)
}

func historyQuerySchemas(r huma.Registry) (*huma.Schema, *huma.Schema) {
	return historySchema[historyRequest](r), &huma.Schema{OneOf: []*huma.Schema{
		historySchema[pairedHistorySummary](r), historySchema[pairedHistoryBucketed](r),
	}}
}

//nolint:goconst // The operation advertises JSON request and response media.
func registerQueryEntityStateHistory(api huma.API, h *handler) {
	request, response := historyQuerySchemas(api.OpenAPI().Components.Schemas)
	responses := historyQueryResponses(api.OpenAPI().Components.Schemas, response)
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
	view, err := h.devices.GetEntity(ctx, query.EntityID)
	if err != nil {
		return nil, h.historyQueryFailure(ctx, err)
	}
	canonical, canonicalQuantity, err := h.presenter.History(view, query.Field, answer, devices.UnitsCanonical)
	if err != nil {
		return nil, h.historyQueryFailure(ctx, err)
	}
	display, displayQuantity, err := h.presenter.History(view, query.Field, answer, "")
	if err != nil {
		return nil, h.historyQueryFailure(ctx, err)
	}
	mapped, err := pairedHistoryResponse(canonical, display, canonicalQuantity, displayQuantity)
	return h.encodeHistoryAnswer(ctx, "query-entity-state-history", mapped, err)
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
