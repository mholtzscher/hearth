package httpapi

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	stateChangesTimelineKind = "timeline"
	stateChangesSummaryKind  = "summary"
)

type stateChangesResultRequest struct {
	Kind   string             `json:"kind"             enum:"timeline,summary"`
	Bucket *historyBucketBody `json:"bucket,omitempty"`
}
type stateChangesRequest struct {
	Range       historyRangeBody          `json:"range"`
	SelectField string                    `json:"select_field" maxLength:"64" pattern:"^(/[a-z][a-z0-9_]*)?$"`
	Result      stateChangesResultRequest `json:"result"`
}

// stateChangesCoverage describes retained changes; Seeded reports whether a
// change before the range set its opening value.
type stateChangesCoverage struct {
	Basis        string `json:"basis"        enum:"retained_changes"`
	Completeness string `json:"completeness" enum:"unverified"`
	Seeded       bool   `json:"seeded"`
}

// StateChangesQueryEnvelope is exported so Huma flattens it into both result schemas.
type StateChangesQueryEnvelope struct {
	Kind        string               `json:"kind"`
	EntityID    string               `json:"entity_id"`
	SelectField string               `json:"select_field"`
	Range       historyRangeBody     `json:"range"`
	TimeAxis    string               `json:"time_axis"    enum:"observed_at"`
	Coverage    stateChangesCoverage `json:"coverage"`
}

// stateChangeIntervalBody omits value for an unknown interval; a known value
// of null means the field was absent.
type stateChangeIntervalBody struct {
	Kind  string          `json:"kind"            enum:"value,unknown"`
	From  string          `json:"from"                                 format:"date-time"`
	To    string          `json:"to"                                   format:"date-time"`
	Value json.RawMessage `json:"value,omitempty"`
}
type stateChangesTimelineBody struct {
	StateChangesQueryEnvelope

	Intervals []stateChangeIntervalBody `json:"intervals"`
}
type stateChangeBucketBody struct {
	Range               historyRangeBody `json:"range"`
	SecondsTrue         float64          `json:"seconds_true"          minimum:"0"`
	TransitionsIntoTrue int              `json:"transitions_into_true" minimum:"0"`
	KnownSeconds        float64          `json:"known_seconds"         minimum:"0"`
}
type stateChangesSummaryBody struct {
	StateChangesQueryEnvelope

	Bucket  historyBucketBody       `json:"bucket"`
	Buckets []stateChangeBucketBody `json:"buckets"`
}

func (stateChangesResultRequest) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	return s
}

func (stateChangesRequest) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["result"] = historySchema[stateChangesResultRequest](r)
	return s
}

func (stateChangeIntervalBody) TransformSchema(_ huma.Registry, s *huma.Schema) *huma.Schema {
	s.Properties["value"] = &huma.Schema{
		AnyOf: []*huma.Schema{{Type: huma.TypeBoolean}, {Type: huma.TypeString}, {Type: "null"}},
	}
	return s
}

func stateChangesEnvelopeSchema(r huma.Registry, s *huma.Schema, kind string) {
	s.Properties["kind"] = &huma.Schema{Type: huma.TypeString, Const: kind}
	s.Properties["range"] = historySchema[historyRangeBody](r)
	s.Properties["coverage"] = historySchema[stateChangesCoverage](r)
}

func (stateChangesTimelineBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	stateChangesEnvelopeSchema(r, s, stateChangesTimelineKind)
	s.Properties["intervals"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[stateChangeIntervalBody](r)}
	return s
}

func (stateChangesSummaryBody) TransformSchema(r huma.Registry, s *huma.Schema) *huma.Schema {
	stateChangesEnvelopeSchema(r, s, stateChangesSummaryKind)
	s.Properties["bucket"] = historySchema[historyBucketBody](r)
	s.Properties["buckets"] = &huma.Schema{Type: huma.TypeArray, Items: historySchema[stateChangeBucketBody](r)}
	return s
}

//nolint:goconst // The operation advertises JSON request and response media.
func registerQueryEntityStateChanges(api huma.API, h *handler) {
	registry := api.OpenAPI().Components.Schemas
	request := historySchema[stateChangesRequest](registry)
	response := &huma.Schema{OneOf: []*huma.Schema{
		historySchema[stateChangesTimelineBody](registry), historySchema[stateChangesSummaryBody](registry),
	}}
	responses := historyQueryResponses(registry, response)
	op := huma.Operation{
		OperationID:      "query-entity-state-changes",
		Method:           http.MethodPost,
		Path:             "/entities/{entity_id}/state/changes",
		Summary:          "Read the recorded changes of a yes/no or text field of an Entity's State",
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
		return h.queryEntityStateChanges(ctx, in, registry, request)
	})
}

func decodeStateChangeQuery(
	in *queryHistoryInput, registry huma.Registry, schema *huma.Schema,
) (devices.StateChangeQuery, error) {
	invalidBody := historyQueryProblem(http.StatusBadRequest, "invalid_query", "Invalid query body.", "")
	mediaType, _, err := mime.ParseMediaType(in.ContentType)
	if err != nil || mediaType != "application/json" {
		return devices.StateChangeQuery{}, historyQueryProblem(
			http.StatusUnsupportedMediaType, "invalid_query", "Expected application/json.", "",
		)
	}
	id, err := devices.ParseEntityID(in.EntityID)
	if err != nil {
		return devices.StateChangeQuery{}, historyQueryProblem(
			http.StatusBadRequest, "invalid_query", "Invalid Entity ID.", "",
		)
	}
	var raw any
	if !jsontext.Value(in.RawBody).IsValid() || json.Unmarshal(in.RawBody, &raw) != nil ||
		!strictStateChangeShape(raw) {
		return devices.StateChangeQuery{}, invalidBody
	}
	result := &huma.ValidateResult{}
	huma.Validate(registry, schema, &huma.PathBuffer{}, huma.ModeReadFromServer, raw, result)
	if len(result.Errors) > 0 {
		return devices.StateChangeQuery{}, invalidBody
	}
	var body stateChangesRequest
	if json.Unmarshal(in.RawBody, &body) != nil {
		return devices.StateChangeQuery{}, invalidBody
	}
	// A summary needs a bucket; a timeline takes none.
	if (body.Result.Kind == stateChangesSummaryKind) != (body.Result.Bucket != nil) {
		return devices.StateChangeQuery{}, historyQueryProblem(
			http.StatusBadRequest, "invalid_query", "Invalid query body.", "result.bucket",
		)
	}
	from, fromErr := time.Parse(time.RFC3339Nano, body.Range.From)
	to, toErr := time.Parse(time.RFC3339Nano, body.Range.To)
	if fromErr != nil || toErr != nil {
		return devices.StateChangeQuery{}, historyQueryProblem(
			http.StatusBadRequest, "invalid_query", "Invalid query body.", "range",
		)
	}
	query := devices.StateChangeQuery{
		EntityID: id, Field: body.SelectField, Range: devices.HistoryRange{From: from.UTC(), To: to.UTC()},
	}
	if b := body.Result.Bucket; b != nil {
		duration, parseErr := time.ParseDuration(b.Duration)
		if parseErr != nil {
			return devices.StateChangeQuery{}, historyQueryProblem(
				http.StatusBadRequest, "invalid_query", "Invalid bucket duration.", "result.bucket.duration",
			)
		}
		query.Bucket = &devices.HistoryBucket{Duration: duration, Alignment: b.Alignment}
	}
	return query, nil
}

func (h *handler) queryEntityStateChanges(
	ctx context.Context, in *queryHistoryInput, registry huma.Registry, schema *huma.Schema,
) (*queryHistoryOutput, error) {
	query, err := decodeStateChangeQuery(in, registry, schema)
	if err != nil {
		return nil, err
	}
	answer, err := h.devices.QueryStateChanges(ctx, query)
	if err != nil {
		return nil, h.stateChangeQueryFailure(ctx, err)
	}
	mapped, err := stateChangesResponse(answer)
	return h.encodeHistoryAnswer(ctx, "query-entity-state-changes", mapped, err)
}

func (h *handler) stateChangeQueryFailure(ctx context.Context, err error) error {
	if expectedRequestCancellation(ctx, err) {
		return historyQueryProblem(http.StatusServiceUnavailable, "query_canceled", "Query canceled.", "")
	}
	if errors.Is(err, devices.ErrEntityNotFound) {
		return historyQueryProblem(http.StatusNotFound, "entity_not_found", "Entity not found.", "")
	}
	if q, ok := errors.AsType[*devices.HistoryQueryError](err); ok {
		switch q.Code {
		case "invalid_query", "unsupported_source", "query_limit", "value_type":
			return historyQueryProblem(http.StatusUnprocessableEntity, q.Code, "State change query failed.", q.Field)
		}
	}
	h.logInternalFailure(ctx, "query-entity-state-changes", "service", err)
	return historyQueryProblem(http.StatusInternalServerError, "internal_error", "Internal error.", "")
}

func stateChangesResponse(result devices.StateChangeQueryResult) (any, error) {
	rangeBody := func(r devices.HistoryRange) historyRangeBody {
		return historyRangeBody{From: formatTime(r.From), To: formatTime(r.To)}
	}
	envelope := StateChangesQueryEnvelope{
		EntityID:    string(result.EntityID),
		SelectField: result.Field,
		Range:       rangeBody(result.Range),
		TimeAxis:    "observed_at",
		Coverage:    stateChangesCoverage{Basis: "retained_changes", Completeness: "unverified", Seeded: result.Seeded},
	}
	switch data := result.Data.(type) {
	case devices.StateChangeTimeline:
		envelope.Kind = stateChangesTimelineKind
		intervals := make([]stateChangeIntervalBody, 0, len(data.Intervals))
		for _, interval := range data.Intervals {
			body := stateChangeIntervalBody{
				Kind: "unknown", From: formatTime(interval.Range.From), To: formatTime(interval.Range.To),
			}
			if interval.Known {
				body.Kind, body.Value = "value", interval.Value
			}
			intervals = append(intervals, body)
		}
		return stateChangesTimelineBody{StateChangesQueryEnvelope: envelope, Intervals: intervals}, nil
	case devices.StateChangeSummary:
		envelope.Kind = stateChangesSummaryKind
		buckets := make([]stateChangeBucketBody, 0, len(data.Buckets))
		for _, b := range data.Buckets {
			buckets = append(buckets, stateChangeBucketBody{
				Range: rangeBody(b.Range), SecondsTrue: b.SecondsTrue,
				TransitionsIntoTrue: b.TransitionsIntoTrue, KnownSeconds: b.KnownSeconds,
			})
		}
		return stateChangesSummaryBody{
			StateChangesQueryEnvelope: envelope,
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

// strictStateChangeShape rejects null optional properties and unknown or
// case-variant keys, which Huma would otherwise accept.
func strictStateChangeShape(raw any) bool {
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
	result, ok := object(root["result"], "kind", "bucket")
	if !ok {
		return false
	}
	if b, exists := result["bucket"]; exists {
		if _, ok = object(b, "duration", "alignment"); !ok {
			return false
		}
	}
	return true
}
