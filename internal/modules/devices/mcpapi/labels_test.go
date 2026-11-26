package mcpapi //nolint:testpackage // Exercises the registered Label tools over the module's wire fixture.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const (
	absentLabelID = "lbl_01890f47-7a6b-7c4d-8e9f-0123456789ff"
	adapterName   = "Hue lamp"
)

func labelServer(stub *stubDevices) http.Handler {
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1.0.0"})
	Register(server, stub, testPresenter())
	return server.HTTPHandler()
}

type wireFailure struct {
	Code    string `json:"failure_code"`
	Message string `json:"message"`
}

func failureOf(t *testing.T, result wireResult) wireFailure {
	t.Helper()
	var failure wireFailure
	if !result.IsError || len(result.Content) != 1 || string(result.Structured) != result.Content[0].Text ||
		json.Unmarshal(result.Structured, &failure) != nil {
		t.Fatalf("result = %+v, want a structured tool failure", result)
	}
	return failure
}

func TestLabelToolsReturnTheCatalogAndReceipts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	area := devices.Label{ID: apiAreaLabelID, Name: "Kitchen", Kind: devices.LabelKindArea, CreatedAt: now}
	tag := devices.Label{ID: apiTagLabelID, Name: "Lamps", Kind: devices.LabelKindTag, CreatedAt: now}
	var created devices.CreateLabelParams
	var renamed struct {
		id   devices.LabelID
		name string
	}
	var deleted devices.LabelID
	handler := labelServer(&stubDevices{
		listLabels: func(context.Context) ([]devices.Label, error) { return []devices.Label{area, tag}, nil },
		createLabel: func(_ context.Context, params devices.CreateLabelParams) (devices.Label, error) {
			created = params
			return tag, nil
		},
		renameLabel: func(_ context.Context, id devices.LabelID, name string) (devices.Label, error) {
			renamed.id, renamed.name = id, name
			return devices.Label{ID: id, Name: name, Kind: devices.LabelKindArea}, nil
		},
		deleteLabel: func(_ context.Context, id devices.LabelID) error {
			deleted = id
			return nil
		},
	})
	replace := strings.NewReplacer("$A", string(apiAreaLabelID), "$G", string(apiTagLabelID))

	assertWireJSON(t, callWire(t, handler, "list_labels", `{}`), replace.Replace(
		`{"items":[{"label_id":"$A","name":"Kitchen","kind":"area"},{"label_id":"$G","name":"Lamps","kind":"tag"}]}`,
	))
	assertWireJSON(t, callWire(t, handler, "create_label", `{"name":"Lamps","kind":"tag"}`), replace.Replace(
		`{"label_id":"$G","name":"Lamps","kind":"tag"}`,
	))
	require.Equal(t, devices.CreateLabelParams{Name: "Lamps", Kind: devices.LabelKindTag}, created)
	assertWireJSON(t, callWire(t, handler, "rename_label", replace.Replace(`{"label_id":"$A","name":"Den"}`)),
		replace.Replace(`{"label_id":"$A","name":"Den","kind":"area"}`))
	require.Equal(t, apiAreaLabelID, renamed.id)
	require.Equal(t, "Den", renamed.name)
	assertWireJSON(t, callWire(t, handler, "delete_label", replace.Replace(`{"label_id":"$A"}`)),
		replace.Replace(`{"label_id":"$A","deleted":true}`))
	require.Equal(t, apiAreaLabelID, deleted)
}

func TestEmptyCatalogListsNoLabels(t *testing.T) {
	t.Parallel()
	handler := labelServer(&stubDevices{
		listLabels: func(context.Context) ([]devices.Label, error) { return nil, nil },
	})
	assertWireJSON(t, callWire(t, handler, "list_labels", `{}`), `{"items":[]}`)
}

func TestLabelToolsMapDomainFailures(t *testing.T) {
	t.Parallel()
	id := string(apiAreaLabelID)
	for _, test := range []struct {
		name, tool, arguments string
		err                   error
		code                  string
	}{
		{"create invalid", "create_label", `{"name":" ","kind":"tag"}`, devices.ErrInvalidLabel, "invalid_label"},
		{
			"create conflict", "create_label", `{"name":"Lamps","kind":"tag"}`,
			fmt.Errorf("%w: lamps", devices.ErrLabelNameConflict), "label_name_conflict",
		},
		{
			"create limit", "create_label", `{"name":"Lamps","kind":"tag"}`,
			devices.ErrLabelLimitReached, "label_limit_reached",
		},
		{
			"rename invalid", "rename_label", `{"label_id":"` + id + `","name":"\u0007"}`,
			devices.ErrInvalidLabel, "invalid_label",
		},
		{
			"rename missing", "rename_label", `{"label_id":"` + id + `","name":"Den"}`,
			devices.ErrLabelNotFound, "label_not_found",
		},
		{
			"rename conflict", "rename_label", `{"label_id":"` + id + `","name":"Den"}`,
			devices.ErrLabelNameConflict, "label_name_conflict",
		},
		{
			"delete missing", "delete_label", `{"label_id":"` + id + `"}`,
			devices.ErrLabelNotFound, "label_not_found",
		},
		{
			"create storage fault", "create_label", `{"name":"Lamps","kind":"tag"}`,
			errors.New("PRIVATE storage fault"), mcptools.CodeInternalError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := labelServer(&stubDevices{
				createLabel: func(context.Context, devices.CreateLabelParams) (devices.Label, error) {
					return devices.Label{}, test.err
				},
				renameLabel: func(context.Context, devices.LabelID, string) (devices.Label, error) {
					return devices.Label{}, test.err
				},
				deleteLabel: func(context.Context, devices.LabelID) error { return test.err },
			})
			result := callWire(t, handler, test.tool, test.arguments)
			require.Equal(t, test.code, failureOf(t, result).Code)
			require.NotContains(t, string(result.Structured), "PRIVATE")
		})
	}
}

func TestLabelToolsRejectMalformedInputBeforeTheService(t *testing.T) {
	t.Parallel()
	handler := labelServer(&stubDevices{})
	for _, test := range []struct{ tool, arguments, code string }{
		{"rename_label", `{"label_id":"lbl_bad","name":"Den"}`, "invalid_label_id"},
		{"delete_label", `{"label_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"}`, "invalid_label_id"},
		{"delete_label", `{"label_id":5}`, "invalid_label_id"},
		{"create_label", `{"name":"Lamps","kind":"room"}`, "invalid_arguments"},
		{"create_label", `{"name":"Lamps"}`, "invalid_arguments"},
		{"rename_label", `{"label_id":"` + string(apiAreaLabelID) + `"}`, "invalid_arguments"},
	} {
		require.Equal(t, test.code, failureOf(t, callWire(t, handler, test.tool, test.arguments)).Code, test.arguments)
	}
}

// metadataStub records the patch an update tool builds and serves a baseline
// record for the no-op read.
type metadataStub struct {
	device   devices.DevicePatch
	entity   devices.EntityPatch
	patches  int
	reads    int
	patchErr error
}

func (recorder *metadataStub) stub() *stubDevices {
	return &stubDevices{
		patchDevice: func(_ context.Context, _ devices.DeviceID, patch devices.DevicePatch) (devices.Device, error) {
			recorder.patches++
			recorder.device = patch
			return metadataDevice(), recorder.patchErr
		},
		getDevice: func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error) {
			recorder.reads++
			return devices.DeviceAggregate{Device: metadataDevice()}, nil
		},
		patchEntity: func(_ context.Context, _ devices.EntityID, patch devices.EntityPatch) (devices.EntityWithState, error) {
			recorder.patches++
			recorder.entity = patch
			return metadataEntity(), recorder.patchErr
		},
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			recorder.reads++
			return metadataEntity(), nil
		},
	}
}

func metadataDevice() devices.Device {
	return devices.Device{
		ID: apiDeviceID, Kind: devices.DeviceKindLight, Name: adapterName, AdapterName: adapterName,
		Labels: devices.DeviceLabels{Area: new(apiAreaRef()), Tags: []devices.LabelRef{apiTagRef()}},
	}
}

func metadataEntity() devices.EntityWithState {
	view := apiEntityWithState(nil)
	view.Entity.Name, view.Entity.AdapterName = adapterName, adapterName
	view.Labels = devices.EntityLabels{
		Area: &devices.EffectiveArea{Label: apiAreaRef(), Source: devices.AreaSourceEntity},
		Tags: []devices.LabelRef{apiTagRef()},
	}
	return view
}

func TestUpdateDeviceDistinguishesOmittedFromNull(t *testing.T) {
	t.Parallel()
	override := "  Reading lamp " // The domain service normalizes; the tool forwards the raw value.
	for _, test := range []struct {
		name, arguments string
		want            devices.DevicePatch
	}{
		{"name only", `{"name_override":"  Reading lamp "}`, devices.DevicePatch{NameEdit: &devices.NameEdit{Override: &override}}},
		{"null name restores", `{"name_override":null}`, devices.DevicePatch{NameEdit: &devices.NameEdit{}}},
		{"null area clears", `{"area_id":null}`, devices.DevicePatch{AreaEdit: &devices.AreaEdit{}}},
		{
			"area sets", `{"area_id":"` + string(apiAreaLabelID) + `"}`,
			devices.DevicePatch{AreaEdit: &devices.AreaEdit{AreaID: new(apiAreaLabelID)}},
		},
		{"empty tags clear", `{"tag_ids":[]}`, devices.DevicePatch{TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{}}}},
		{
			"tags replace", `{"tag_ids":["` + string(apiTagLabelID) + `"]}`,
			devices.DevicePatch{TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{apiTagLabelID}}},
		},
		{
			"every field", `{"name_override":null,"area_id":null,"tag_ids":[]}`,
			devices.DevicePatch{
				NameEdit: &devices.NameEdit{}, AreaEdit: &devices.AreaEdit{},
				TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &metadataStub{}
			arguments := `{"device_id":"` + string(apiDeviceID) + `",` + strings.TrimPrefix(test.arguments, "{")
			result := callWire(t, labelServer(recorder.stub()), "update_device", arguments)
			assertWireJSON(
				t,
				result,
				`{"device_id":"`+string(apiDeviceID)+`","name":"Hue lamp","adapter_name":"Hue lamp",`+
					`"name_override":null,"area":{"label_id":"`+string(apiAreaLabelID)+`","name":"Kitchen"},`+
					`"tags":[{"label_id":"`+string(apiTagLabelID)+`","name":"Lamps"}]}`,
			)
			require.Equal(t, 1, recorder.patches)
			require.Zero(t, recorder.reads)
			normalizeEmptyTags(&test.want.TagEdit, &recorder.device.TagEdit)
			require.Equal(t, test.want, recorder.device)
		})
	}
}

func TestUpdateEntityDistinguishesOmittedFromNull(t *testing.T) {
	t.Parallel()
	disabled := false
	for _, test := range []struct {
		name, arguments string
		want            devices.EntityPatch
	}{
		{"enablement only", `{"enabled":false}`, devices.EntityPatch{Enabled: &disabled}},
		{"null name restores", `{"name_override":null}`, devices.EntityPatch{NameEdit: &devices.NameEdit{}}},
		{"null area inherits", `{"area_id":null}`, devices.EntityPatch{AreaEdit: &devices.AreaEdit{}}},
		{
			"area sets", `{"area_id":"` + string(apiAreaLabelID) + `"}`,
			devices.EntityPatch{AreaEdit: &devices.AreaEdit{AreaID: new(apiAreaLabelID)}},
		},
		{"empty tags clear", `{"tag_ids":[]}`, devices.EntityPatch{TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{}}}},
		{
			"enablement and tags", `{"enabled":false,"tag_ids":["` + string(apiTagLabelID) + `"]}`,
			devices.EntityPatch{Enabled: &disabled, TagEdit: &devices.TagEdit{TagIDs: []devices.LabelID{apiTagLabelID}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &metadataStub{}
			arguments := `{"entity_id":"` + string(apiEntityID) + `",` + strings.TrimPrefix(test.arguments, "{")
			result := callWire(t, labelServer(recorder.stub()), "update_entity", arguments)
			assertWireJSON(
				t,
				result,
				`{"entity_id":"`+string(apiEntityID)+`","name":"Hue lamp","adapter_name":"Hue lamp",`+
					`"name_override":null,"enabled":true,"area":{"label_id":"`+string(apiAreaLabelID)+
					`","name":"Kitchen","source":"entity"},"tags":[{"label_id":"`+string(apiTagLabelID)+`","name":"Lamps"}]}`,
			)
			require.Equal(t, 1, recorder.patches)
			require.Zero(t, recorder.reads)
			normalizeEmptyTags(&test.want.TagEdit, &recorder.entity.TagEdit)
			require.Equal(t, test.want, recorder.entity)
		})
	}
}

// normalizeEmptyTags treats nil and empty tag sets alike: both clear the tags.
func normalizeEmptyTags(want, got **devices.TagEdit) {
	for _, edit := range []**devices.TagEdit{want, got} {
		if *edit != nil && len((*edit).TagIDs) == 0 {
			*edit = &devices.TagEdit{}
		}
	}
}

func TestUpdateToolsWithoutEditableFieldsReadTheCurrentReceipt(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ tool, arguments, want string }{
		{
			"update_device", `{"device_id":"` + string(apiDeviceID) + `"}`,
			`{"device_id":"` + string(apiDeviceID) + `","name":"Hue lamp","adapter_name":"Hue lamp","name_override":null,` +
				`"area":{"label_id":"` + string(apiAreaLabelID) + `","name":"Kitchen"},` +
				`"tags":[{"label_id":"` + string(apiTagLabelID) + `","name":"Lamps"}]}`,
		},
		{
			"update_entity", `{"entity_id":"` + string(apiEntityID) + `"}`,
			`{"entity_id":"` + string(apiEntityID) + `","name":"Hue lamp","adapter_name":"Hue lamp","name_override":null,` +
				`"enabled":true,"area":{"label_id":"` + string(apiAreaLabelID) + `","name":"Kitchen","source":"entity"},` +
				`"tags":[{"label_id":"` + string(apiTagLabelID) + `","name":"Lamps"}]}`,
		},
	} {
		t.Run(test.tool, func(t *testing.T) {
			t.Parallel()
			recorder := &metadataStub{}
			assertWireJSON(t, callWire(t, labelServer(recorder.stub()), test.tool, test.arguments), test.want)
			require.Equal(t, 1, recorder.reads)
			require.Zero(t, recorder.patches)
		})
	}
}

func TestUpdateToolsMapMetadataFailures(t *testing.T) {
	t.Parallel()
	const nameMessage = "name_override must be 1 to 128 code points after trimming and contain no control " +
		"characters; area_id must name an Area; tag_ids must name at most 32 tags"
	deviceArgs := `{"device_id":"` + string(apiDeviceID) + `","area_id":"` + absentLabelID + `"}`
	entityArgs := `{"entity_id":"` + string(apiEntityID) + `","tag_ids":["` + absentLabelID + `"]}`
	for _, test := range []struct {
		name, tool, arguments string
		err                   error
		code, message         string
	}{
		{
			"device unknown label", "update_device", deviceArgs,
			fmt.Errorf("%w: %w", devices.ErrInvalidMetadataPatch, devices.ErrLabelNotFound), "label_not_found", "",
		},
		{
			"entity unknown label", "update_entity", entityArgs,
			fmt.Errorf("%w: %w", devices.ErrInvalidMetadataPatch, devices.ErrLabelNotFound), "label_not_found", "",
		},
		{
			"device wrong kind", "update_device", deviceArgs,
			fmt.Errorf("%w: not an area", devices.ErrInvalidMetadataPatch), "invalid_request", nameMessage,
		},
		{
			"entity invalid name", "update_entity", `{"entity_id":"` + string(apiEntityID) + `","name_override":" "}`,
			devices.ErrInvalidMetadataPatch, "invalid_request", nameMessage,
		},
		{"missing device", "update_device", deviceArgs, devices.ErrDeviceNotFound, "device_not_found", ""},
		{"missing entity", "update_entity", entityArgs, devices.ErrEntityNotFound, "entity_not_found", ""},
		{
			"storage fault", "update_device", deviceArgs, errors.New("PRIVATE storage fault"),
			mcptools.CodeInternalError, "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &metadataStub{patchErr: test.err}
			result := callWire(t, labelServer(recorder.stub()), test.tool, test.arguments)
			failure := failureOf(t, result)
			require.Equal(t, test.code, failure.Code)
			if test.message != "" {
				require.Equal(t, test.message, failure.Message)
			}
			require.NotContains(t, string(result.Structured), "PRIVATE")
		})
	}
}

func TestUpdateToolsRejectMalformedInputBeforeTheService(t *testing.T) {
	t.Parallel()
	handler := labelServer(&stubDevices{})
	device, entity := string(apiDeviceID), string(apiEntityID)
	for _, test := range []struct{ name, tool, arguments, code string }{
		{"null enabled", "update_entity", `{"entity_id":"` + entity + `","enabled":null}`, "invalid_request"},
		{"null device tags", "update_device", `{"device_id":"` + device + `","tag_ids":null}`, "invalid_label_id"},
		{"null entity tags", "update_entity", `{"entity_id":"` + entity + `","tag_ids":null}`, "invalid_label_id"},
		{"bad area", "update_device", `{"device_id":"` + device + `","area_id":"nope"}`, "invalid_label_id"},
		{"bad tag", "update_entity", `{"entity_id":"` + entity + `","tag_ids":["` + absentLabelID + `","nope"]}`, "invalid_label_id"},
		{"non-string name", "update_device", `{"device_id":"` + device + `","name_override":5}`, "invalid_arguments"},
		{"unknown field", "update_entity", `{"entity_id":"` + entity + `","label_ids":[]}`, "invalid_arguments"},
		{"bad device", "update_device", `{"device_id":"dev_bad","name_override":"Lamp"}`, "invalid_device_id"},
		{"bad entity", "update_entity", `{"entity_id":"ent_bad","name_override":"Lamp"}`, "invalid_entity_id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.code, failureOf(t, callWire(t, handler, test.tool, test.arguments)).Code)
		})
	}
}

func TestListEntitiesForwardsTheSelector(t *testing.T) {
	t.Parallel()
	var params devices.ListEntitiesParams
	handler := labelServer(&stubDevices{
		listEntities: func(_ context.Context, got devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
			params = got
			return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{metadataEntity()}}, nil
		},
	})
	result := callWire(t, handler, "list_entities", `{"label_ids":["`+string(apiAreaLabelID)+`","`+
		string(apiTagLabelID)+`"],"types":["hearth.power/v1"]}`)
	require.False(t, result.IsError, string(result.Structured))
	require.Equal(t, devices.EntitySelector{
		Labels: []devices.LabelID{apiAreaLabelID, apiTagLabelID},
		Types:  []devices.EntityTypeID{devices.EntityTypePowerV1},
	}, params.Selector)
	require.Contains(
		t,
		string(result.Structured),
		`"area":{"label_id":"`+string(apiAreaLabelID)+`","name":"Kitchen","source":"entity"}`,
	)
	require.Contains(t, string(result.Structured), `"tags":[{"label_id":"`+string(apiTagLabelID)+`","name":"Lamps"}]`)
}

func TestListEntitiesMapsSelectorFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, arguments string
		err             error
		code            string
	}{
		{"unknown label", `{"label_ids":["` + absentLabelID + `"]}`, devices.ErrLabelNotFound, "label_not_found"},
		{
			"malformed selector", `{"types":["not-a-type"]}`,
			fmt.Errorf("%w: unknown type", devices.ErrInvalidEntitySelector), "invalid_selector",
		},
		{"malformed label ID", `{"label_ids":["nope"]}`, nil, "invalid_label_id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := labelServer(&stubDevices{
				listEntities: func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
					return devices.Page[devices.EntityWithState]{}, test.err
				},
			})
			require.Equal(t, test.code, failureOf(t, callWire(t, handler, "list_entities", test.arguments)).Code)
		})
	}
}

// A cursor carries its selector, so a follow-up page continues the same filter
// and a cursor from another selector is rejected before the service runs.
func TestEntityCursorsAreScopedToTheSelector(t *testing.T) {
	t.Parallel()
	calls := 0
	handler := labelServer(&stubDevices{
		listEntities: func(_ context.Context, params devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
			calls++
			if calls == 1 {
				return devices.Page[devices.EntityWithState]{
					Items: []devices.EntityWithState{apiEntityWithState(nil)}, HasMore: true,
				}, nil
			}
			require.NotNil(t, params.AfterID)
			require.Equal(t, []devices.LabelID{apiAreaLabelID}, params.Selector.Labels)
			return devices.Page[devices.EntityWithState]{}, nil
		},
	})
	filter := `"label_ids":["` + string(apiAreaLabelID) + `"]`
	continueWirePage(t, handler, "list_entities", `{`+filter+`,"limit":1}`, "next_cursor", `{"items":[]}`)

	foreign, err := apicursor.EncodeEntitiesCursor(apiEntityID, nil, devices.EntitySelector{
		Labels: []devices.LabelID{apiTagLabelID},
	})
	require.NoError(t, err)
	unfiltered, err := apicursor.EncodeEntitiesCursor(apiEntityID, nil, devices.EntitySelector{})
	require.NoError(t, err)
	before := calls
	for _, cursor := range []string{foreign, unfiltered} {
		result := callWire(t, handler, "list_entities", `{`+filter+`,"cursor":"`+cursor+`"}`)
		require.Equal(t, "invalid_request", failureOf(t, result).Code)
	}
	require.Equal(t, before, calls)
}
