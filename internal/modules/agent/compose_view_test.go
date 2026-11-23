package agent //nolint:testpackage // Tests the native tool and its model-visible failures.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestComposeViewSafeArgumentDiagnostics(t *testing.T) {
	t.Parallel()
	const sentinel = "private-value-do-not-echo"
	for _, tc := range []struct {
		name, args, code, path string
	}{
		{"malformed JSON", `{`, "invalid_json", ""},
		{"not object", `[]`, "type_mismatch", ""},
		{"schema closed", `{"mode":"create","title":"View","layout":{"type":"Note","text":"text","` + sentinel + `":"secret"}}`, "unknown_property", "/layout"},
		{"bad variant", `{"mode":"create","title":"View","layout":{"type":"` + sentinel + `","text":"text"}}`, "invalid_variant", "/layout/type"},
		{"needs title", `{"mode":"create","layout":{"type":"Note","text":"text"}}`, "missing_property", "/title"},
		{"stringified layout", `{"mode":"create","title":"View","layout":"` + sentinel + `"}`, "type_mismatch", "/layout"},
		{"stringified children", `{"mode":"create","title":"View","layout":{"type":"Stack","children":"` + sentinel + `"}}`, "type_mismatch", "/layout/children"},
		{"nested series shape", `{"mode":"create","title":"View","layout":{"type":"Grid","columns":2,"children":[{"type":"TimeSeries","title":"Trend","window":"24h","series":"` + sentinel + `"}]}}`, "type_mismatch", "/layout/children/0/series"},
		{"no raw graph", `{"mode":"edit","definition":{},"changes":[{"op":"set_title","title":"New"}]}`, "unknown_property", ""},
		{"unsupported edit", `{"mode":"edit","changes":[{"op":"set_card"}]}`, "invalid_variant", "/changes/0/op"},
		{"bad edit field", `{"mode":"edit","changes":[{"op":"set_chart_window","card_id":"trend","window":null}]}`, "constraint_violation", "/changes/0/window"},
		{"too large", strings.Repeat("x", maxComposeArguments+1), "argument_limit", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			native := testComposeTool(t, nil)
			output, failure := invokedComposeFailure(t, native, tc.args)
			require.Equal(t, composeViewFailure{
				Code: "invalid_arguments", Message: "Invalid compose_view arguments.",
				Diagnostics: []composeDiagnostic{{Code: tc.code, Path: tc.path}},
			}, failure, "tool output: %s", output)
			require.NotContains(t, output, sentinel)
		})
	}
}

func TestComposeViewSourceAndBudgetRejections(t *testing.T) {
	t.Parallel()
	const first = "ent_01900000-0000-7000-8000-000000000001"
	const second = "ent_01900000-0000-7000-8000-000000000002"
	const daily = "ent_01900000-0000-7000-8000-000000000003"
	const missing = "ent_01900000-0000-7000-8000-000000000000"
	inventory := composeInventory{
		first: {
			ID:      first,
			TypeID:  devices.EntityTypeNumericsensorV1,
			Support: devices.EntitySupport(`{"state":{"unit":"mm"},"operations":{}}`),
		},
		second: {
			ID:      second,
			TypeID:  devices.EntityTypeNumericsensorV1,
			Support: devices.EntitySupport(`{"state":{"unit":"mm/h"},"operations":{}}`),
		},
		daily: {ID: daily, TypeID: devices.EntityTypeDailyforecastV1},
	}
	group := func(layout string, n int) string {
		return `{"type":"Stack","children":[` + strings.TrimSuffix(strings.Repeat(layout+",", n), ",") + `]}`
	}
	var resources []string
	for i := range 17 {
		id := devices.EntityID(fmt.Sprintf("ent_01900000-0000-7000-8000-%012d", 400+i))
		inventory[id] = devices.Entity{ID: id, TypeID: devices.EntityTypeTemperatureV1}
		resources = append(resources, fmt.Sprintf(`{"type":"Metric","source":{"entity_id":%q}}`, id))
	}
	overReferences := `{"type":"Grid","columns":2,"children":[` + group(
		strings.Join(resources[:16], ","),
		1,
	) + `,` + resources[16] + `]}`
	note := `{"type":"Note","text":"` + strings.Repeat("x", 500) + `"}`
	portableSize := `{"type":"Stack","children":[` + group(note, 14) + "," + group(note, 15) + `]}`
	tooDeep := note
	for range 8 {
		tooDeep = group(tooDeep, 1)
	}
	for _, tc := range []struct {
		name, layout, code, reason, path string
	}{
		{"unknown source", `{"type":"Metric","source":{"entity_id":"` + missing + `"}}`, "invalid_definition", "an entity_id names no Entity", "/layout/source/entity_id"},
		{"wrong source type", `{"type":"Pressure","entity_id":"` + first + `"}`, "invalid_definition", "the selected Entity type is not compatible with this card or role", "/layout/entity_id"},
		{"wrong source field", `{"type":"Metric","source":{"entity_id":"` + first + `","field":"/value"}}`, "invalid_definition", "choose a State field", "/layout/source/field"},
		{"mixed chart units", `{"type":"TimeSeries","title":"Chart","window":"24h","series":[{"entity_id":"` + first + `"},{"entity_id":"` + second + `"}]}`, "invalid_definition", "share one unit", ""},
		{"unknown series field", `{"type":"TimeSeries","title":"Chart","window":"24h","field":"/value","series":[{"entity_id":"` + first + `"}]}`, "invalid_definition", "field shared by the chart", "/layout/field"},
		{"invalid period count", `{"type":"Forecast","entity_id":"` + daily + `","periods":12}`, "invalid_definition", "daily forecasts show 3 to 8", "/layout/periods"},
		{"weather unit", `{"type":"Rain","rate":"` + first + `"}`, "invalid_definition", "rain rates need a numeric sensor in mm/h", ""},
		{"portable size", portableSize, "invalid_definition", "at most 16 KiB", ""},
		{"layout depth", tooDeep, "invalid_arguments", "Invalid compose_view arguments", "/layout" + strings.Repeat("/children/0", 8)},
		{"source limit", overReferences, "invalid_definition", "at most 16 distinct Entities and Devices", "/layout/children/1/source/entity_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			native := testComposeTool(t, dashboards.NewService(nil, inventory, inventory))
			output, failure := invokedComposeFailure(
				t,
				native,
				`{"mode":"create","title":"View","layout":`+tc.layout+`}`,
			)
			require.Equal(t, tc.code, failure.Code, "tool output: %s", output)
			require.Contains(t, failure.Message, tc.reason)
			require.NotContains(t, output, first)
			require.NotContains(t, output, missing)
			if tc.path != "" {
				require.Len(t, failure.Diagnostics, 1)
				require.Equal(t, tc.path, failure.Diagnostics[0].Path)
			}
		})
	}
}

func TestComposeViewRequiresCurrentDraftMode(t *testing.T) {
	t.Parallel()
	const create = `{"mode":"create","title":"New","layout":{"type":"Heading","text":"Text"}}`
	const edit = `{"mode":"edit","changes":[{"op":"set_title","title":"Renamed"}]}`
	service := dashboards.NewService(nil, nil, nil)
	without := testComposeTool(t, service)
	_, failure := invokedComposeFailure(t, without, edit)
	require.Equal(t, "draft_required", failure.Code)
	result, err := without.InvokableRun(t.Context(), create)
	require.NoError(t, err)
	artifact, err := dashboards.ParseArtifact([]byte(result))
	require.NoError(t, err)
	definition, err := dashboards.ParseDefinition(artifact.Definition)
	require.NoError(t, err)
	with := composeViewTool{
		composeViewContract: without.composeViewContract,
		draft:               &ViewContext{Definition: definition},
	}
	_, failure = invokedComposeFailure(t, with, create)
	require.Equal(t, "draft_selected", failure.Code)
	result, err = with.InvokableRun(t.Context(), edit)
	require.NoError(t, err)
	revised, err := dashboards.ParseArtifact([]byte(result))
	require.NoError(t, err)
	updated, err := dashboards.ParseDefinition(revised.Definition)
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Title)
	require.JSONEq(t, string(definition.Spec), string(updated.Spec))
}

func TestComposeViewNormalizesIntegralJSONNumbers(t *testing.T) {
	t.Parallel()
	native := testComposeTool(t, dashboards.NewService(nil, nil, nil))
	result, err := native.InvokableRun(
		t.Context(),
		`{"mode":"create","title":"Grid","layout":{"type":"Grid","columns":2.0,"children":[{"type":"Note","text":"Text"}]}}`,
	)
	require.NoError(t, err)
	artifact, err := dashboards.ParseArtifact([]byte(result))
	require.NoError(t, err)
	require.Contains(t, string(artifact.Definition), `"columns":2`)
}

func TestComposeViewCancellationAndUnexpectedFailuresStayErrors(t *testing.T) {
	t.Parallel()
	const args = `{"mode":"create","title":"New","layout":{"type":"Note","text":"Text"}}`
	cause := errors.New("private dependency cause")
	native := testComposeTool(t, brokenViewAuthoring{err: cause})
	output, err := native.InvokableRun(t.Context(), args)
	require.Empty(t, output)
	require.ErrorIs(t, err, cause)
	var classified toolCallError
	require.ErrorAs(t, err, &classified)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	output, err = native.InvokableRun(ctx, args)
	require.Empty(t, output)
	require.ErrorIs(t, err, context.Canceled)
}

type brokenViewAuthoring struct {
	ViewAuthoring

	err error
}

func (authoring brokenViewAuthoring) ComposeView(
	context.Context,
	dashboards.ComposeViewInput,
) (dashboards.Artifact, error) {
	return dashboards.Artifact{}, authoring.err
}

func testComposeTool(t *testing.T, authoring ViewAuthoring) composeViewTool {
	t.Helper()
	contract, err := newComposeViewContract(authoring)
	require.NoError(t, err)
	return composeViewTool{composeViewContract: contract}
}

func invokedComposeFailure(t *testing.T, native composeViewTool, args string) (string, composeViewFailure) {
	t.Helper()
	status := &toolResultStatus{}
	output, err := native.InvokableRun(context.WithValue(t.Context(), toolResultStatusKey{}, status), args)
	require.NoError(t, err)
	require.True(t, status.modeledFailure)
	var failure composeViewFailure
	require.NoError(t, json.Unmarshal([]byte(output), &failure))
	require.NotEmpty(t, failure.Code)
	require.NotContains(t, output, `"artifact_id"`)
	return output, failure
}
