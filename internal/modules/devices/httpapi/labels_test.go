package httpapi //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	apiAreaID = devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	apiTagID  = devices.LabelID("lbl_01890f47-7a6b-7c4d-8e9f-0123456789ac")
)

func apiLabel(id devices.LabelID, name string, kind devices.LabelKind) devices.Label {
	at := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	return devices.Label{ID: id, Name: name, Kind: kind, CreatedAt: at, UpdatedAt: at.Add(time.Hour)}
}

func labelRequest(handler http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestListLabelsReturnsCatalog(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{listLabels: func(context.Context) ([]devices.Label, error) {
		return []devices.Label{apiLabel(apiAreaID, "Kitchen", devices.LabelKindArea)}, nil
	}})
	response := performRequest(router, "/v1/labels")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body LabelCollectionBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := LabelBody{
		ID: string(apiAreaID), Name: "Kitchen", Kind: "area",
		CreatedAt: "2026-09-01T08:00:00Z", UpdatedAt: "2026-09-01T09:00:00Z",
	}
	if len(body.Items) != 1 || body.Items[0] != want {
		t.Fatalf("items = %#v", body.Items)
	}
}

func TestListLabelsEmptyCatalogIsAnArray(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{listLabels: func(context.Context) ([]devices.Label, error) {
		return nil, nil
	}})
	response := performRequest(router, "/v1/labels")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil || string(raw["items"]) != "[]" {
		t.Fatalf("body = %s, %v", response.Body.String(), err)
	}
}

func TestCreateLabel(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		body       string
		err        error
		status     int
		code       string
		wantParams *devices.CreateLabelParams
	}{
		{
			name: "created", body: `{"name":"Kitchen","kind":"area"}`, status: http.StatusCreated,
			wantParams: &devices.CreateLabelParams{Name: "Kitchen", Kind: devices.LabelKindArea},
		},
		{name: "invalid", body: `{"name":" ","kind":"tag"}`, err: devices.ErrInvalidLabel, status: http.StatusBadRequest},
		{
			name: "name conflict", body: `{"name":"Kitchen","kind":"tag"}`, err: devices.ErrLabelNameConflict,
			status: http.StatusConflict, code: "label_name_conflict",
		},
		{
			name: "limit", body: `{"name":"Kitchen","kind":"tag"}`, err: devices.ErrLabelLimitReached,
			status: http.StatusConflict, code: "label_limit_reached",
		},
		{name: "unknown kind", body: `{"name":"Kitchen","kind":"floor"}`, status: http.StatusUnprocessableEntity},
		{name: "missing kind", body: `{"name":"Kitchen"}`, status: http.StatusUnprocessableEntity},
		{name: "extra field", body: `{"name":"Kitchen","kind":"tag","x":1}`, status: http.StatusUnprocessableEntity},
		{name: "internal", body: `{"name":"Kitchen","kind":"tag"}`, err: errors.New("db"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var got *devices.CreateLabelParams
			router, _ := testAPI(t, &stubDevices{createLabel: func(
				_ context.Context, params devices.CreateLabelParams,
			) (devices.Label, error) {
				got = &params
				return apiLabel(apiAreaID, params.Name, params.Kind), test.err
			}})
			response := labelRequest(router, http.MethodPost, "/v1/labels", test.body, "application/json")
			if response.Code != test.status {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if test.wantParams != nil && (got == nil || *got != *test.wantParams) {
				t.Fatalf("params = %#v", got)
			}
			assertCreateLabelBody(t, response, test.status, test.code)
		})
	}
}

func assertCreateLabelBody(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if status == http.StatusCreated {
		var body LabelBody
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.ID != string(apiAreaID) ||
			body.Kind != "area" {
			t.Fatalf("body = %s, %v", response.Body.String(), err)
		}
	}
	if code != "" {
		var problem labelConflictError
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil ||
			problem.Code != code || problem.Status != http.StatusConflict {
			t.Fatalf("problem = %s, %v", response.Body.String(), err)
		}
	}
}

func TestUpdateLabel(t *testing.T) {
	t.Parallel()
	path := "/v1/labels/" + string(apiAreaID)
	for _, test := range []struct {
		name        string
		path        string
		body        string
		contentType string
		err         error
		status      int
		renamed     string
	}{
		{name: "rename", path: path, body: `{"name":"Pantry"}`, status: http.StatusOK, renamed: "Pantry"},
		{name: "empty reads", path: path, body: `{}`, status: http.StatusOK},
		{name: "invalid name", path: path, body: `{"name":" "}`, err: devices.ErrInvalidLabel, status: http.StatusBadRequest},
		{name: "not found", path: path, body: `{"name":"Pantry"}`, err: devices.ErrLabelNotFound, status: http.StatusNotFound},
		{name: "conflict", path: path, body: `{"name":"Pantry"}`, err: devices.ErrLabelNameConflict, status: http.StatusConflict},
		{name: "bad id", path: "/v1/labels/bad", body: `{"name":"Pantry"}`, status: http.StatusBadRequest},
		{name: "null name", path: path, body: `{"name":null}`, status: http.StatusUnprocessableEntity},
		{name: "kind rejected", path: path, body: `{"kind":"tag"}`, status: http.StatusUnprocessableEntity},
		{name: "unknown rejected", path: path, body: `{"name":"x","x":1}`, status: http.StatusUnprocessableEntity},
		{name: "not object", path: path, body: `null`, status: http.StatusUnprocessableEntity},
		{name: "media type", path: path, body: `{"name":"x"}`, contentType: "application/json", status: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var renamed string
			router, _ := testAPI(t, &stubDevices{
				renameLabel: func(_ context.Context, id devices.LabelID, name string) (devices.Label, error) {
					renamed = name
					return apiLabel(id, name, devices.LabelKindArea), test.err
				},
				listLabels: func(context.Context) ([]devices.Label, error) {
					return []devices.Label{apiLabel(apiAreaID, "Kitchen", devices.LabelKindArea)}, nil
				},
			})
			contentType := test.contentType
			if contentType == "" {
				contentType = mergePatchContentType
			}
			response := labelRequest(router, http.MethodPatch, test.path, test.body, contentType)
			if response.Code != test.status {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if test.status == http.StatusOK && renamed != test.renamed {
				t.Fatalf("renamed = %q, want %q", renamed, test.renamed)
			}
			if test.name == "empty reads" {
				var body LabelBody
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Name != "Kitchen" {
					t.Fatalf("body = %s, %v", response.Body.String(), err)
				}
			}
		})
	}
}

func TestUpdateLabelEmptyPatchOfUnknownLabelIsNotFound(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(
		t,
		&stubDevices{listLabels: func(context.Context) ([]devices.Label, error) { return nil, nil }},
	)
	response := labelRequest(router, http.MethodPatch, "/v1/labels/"+string(apiAreaID), `{}`, mergePatchContentType)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestDeleteLabel(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		path   string
		err    error
		status int
	}{
		{name: "deleted", path: "/v1/labels/" + string(apiAreaID), status: http.StatusNoContent},
		{name: "not found", path: "/v1/labels/" + string(apiAreaID), err: devices.ErrLabelNotFound, status: http.StatusNotFound},
		{name: "bad id", path: "/v1/labels/bad", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(
				t,
				&stubDevices{deleteLabel: func(context.Context, devices.LabelID) error { return test.err }},
			)
			response := labelRequest(router, http.MethodDelete, test.path, "", "")
			if response.Code != test.status || (test.status == http.StatusNoContent && response.Body.Len() != 0) {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPatchEntityLabelFieldsAreWritesWithOmittedVersusNull(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		body     string
		wantArea *devices.AreaEdit
		wantTags *devices.TagEdit
	}{
		{name: "area set", body: `{"area_id":"` + string(apiAreaID) + `"}`, wantArea: &devices.AreaEdit{AreaID: new(apiAreaID)}},
		{name: "area null", body: `{"area_id":null}`, wantArea: &devices.AreaEdit{}},
		{name: "tags", body: `{"tag_ids":["` + string(apiTagID) + `"]}`, wantTags: &devices.TagEdit{TagIDs: []devices.LabelID{apiTagID}}},
		{name: "tags clear", body: `{"tag_ids":[]}`, wantTags: &devices.TagEdit{TagIDs: []devices.LabelID{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var got devices.EntityPatch
			router, _ := testAPI(t, &stubDevices{patchEntity: func(
				_ context.Context, _ devices.EntityID, patch devices.EntityPatch,
			) (devices.EntityWithState, error) {
				got = patch
				return apiEntityWithState(nil), nil
			}})
			response := patchEntityRequest(router, string(apiEntityID), test.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			if got.Enabled != nil || got.NameEdit != nil || !equalAreaEdit(got.AreaEdit, test.wantArea) ||
				!equalTagEdit(got.TagEdit, test.wantTags) {
				t.Fatalf("patch = %#v", got)
			}
		})
	}
}

func TestPatchDeviceLabelFieldsAreWrites(t *testing.T) {
	t.Parallel()
	var got devices.DevicePatch
	router, _ := testAPI(t, &stubDevices{patchDevice: func(
		_ context.Context, _ devices.DeviceID, patch devices.DevicePatch,
	) (devices.Device, error) {
		got = patch
		return devices.Device{ID: apiDeviceID, Labels: devices.DeviceLabels{
			Area: &devices.LabelRef{ID: apiAreaID, Name: "Kitchen"},
			Tags: []devices.LabelRef{{ID: apiTagID, Name: "Lights"}},
		}}, nil
	}})
	response := metadataRequest(router, http.MethodPatch, "/v1/devices/"+string(apiDeviceID),
		`{"area_id":null,"tag_ids":["`+string(apiTagID)+`"]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got.NameEdit != nil || !equalAreaEdit(got.AreaEdit, &devices.AreaEdit{}) ||
		!equalTagEdit(got.TagEdit, &devices.TagEdit{TagIDs: []devices.LabelID{apiTagID}}) {
		t.Fatalf("patch = %#v", got)
	}
	var body DeviceBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Area == nil || body.Area.Name != "Kitchen" || len(body.Tags) != 1 || body.Tags[0].Name != "Lights" {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestPatchLabelFieldValidation(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	for _, path := range []string{"/v1/entities/" + string(apiEntityID), "/v1/devices/" + string(apiDeviceID)} {
		for _, test := range []struct {
			body   string
			status int
		}{
			{`{"tag_ids":null}`, http.StatusUnprocessableEntity},
			{`{"tag_ids":"lbl"}`, http.StatusUnprocessableEntity},
			{`{"area_id":5}`, http.StatusUnprocessableEntity},
			{`{"area_id":"bad"}`, http.StatusBadRequest},
			{`{"tag_ids":["bad"]}`, http.StatusBadRequest},
		} {
			if response := metadataRequest(router, http.MethodPatch, path, test.body); response.Code != test.status {
				t.Fatalf("%s %s = %d: %s", path, test.body, response.Code, response.Body.String())
			}
		}
	}
}

func TestPatchUnknownOrWrongKindLabelIsBadRequest(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{
		patchEntity: func(context.Context, devices.EntityID, devices.EntityPatch) (devices.EntityWithState, error) {
			return devices.EntityWithState{}, errors.Join(devices.ErrInvalidMetadataPatch, devices.ErrLabelNotFound)
		},
		patchDevice: func(context.Context, devices.DeviceID, devices.DevicePatch) (devices.Device, error) {
			return devices.Device{}, devices.ErrInvalidMetadataPatch
		},
	})
	body := `{"area_id":"` + string(apiAreaID) + `"}`
	for _, path := range []string{"/v1/entities/" + string(apiEntityID), "/v1/devices/" + string(apiDeviceID)} {
		response := metadataRequest(router, http.MethodPatch, path, body)
		var problem huma.ErrorModel
		if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &problem) != nil {
			t.Fatalf("%s = %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestReadModelsPublishLabels(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(nil)
	view.Labels = devices.EntityLabels{
		Area: &devices.EffectiveArea{
			Label:  devices.LabelRef{ID: apiAreaID, Name: "Kitchen"},
			Source: devices.AreaSourceDevice,
		},
		Tags: []devices.LabelRef{{ID: apiTagID, Name: "Lights"}},
	}
	router, _ := testAPI(
		t,
		&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			return view, nil
		}},
	)
	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	var body PairedEntityBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Area == nil || body.Area.Source != "device" || body.Area.ID != string(apiAreaID) ||
		len(body.Tags) != 1 || body.Tags[0].ID != string(apiTagID) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestReadModelsWithoutLabelsPublishNullAreaAndEmptyTags(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(
		t,
		&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			return apiEntityWithState(nil), nil
		}},
	)
	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil ||
		string(raw["area"]) != "null" || string(raw["tags"]) != "[]" {
		t.Fatalf("body = %s, %v", response.Body.String(), err)
	}
}

func TestListEntitiesSelectorQuery(t *testing.T) {
	t.Parallel()
	var got devices.ListEntitiesParams
	router, _ := testAPI(t, &stubDevices{listEntities: func(
		_ context.Context, params devices.ListEntitiesParams,
	) (devices.Page[devices.EntityWithState], error) {
		got = params
		return devices.Page[devices.EntityWithState]{}, nil
	}})
	response := performRequest(
		router,
		"/v1/entities?label_id="+string(apiAreaID)+","+string(apiTagID)+"&type=hearth.power/v1",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if len(got.Selector.Labels) != 2 || got.Selector.Labels[0] != apiAreaID || got.Selector.Labels[1] != apiTagID ||
		len(got.Selector.Types) != 1 || got.Selector.Types[0] != devices.EntityTypePowerV1 {
		t.Fatalf("selector = %#v", got.Selector)
	}
}

func TestListEntitiesSelectorErrors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		query string
		err   error
	}{
		{name: "malformed label id", query: "?label_id=bad"},
		{name: "unknown label", query: "?label_id=" + string(apiAreaID), err: devices.ErrLabelNotFound},
		{name: "invalid selector", query: "?type=nonsense", err: devices.ErrInvalidEntitySelector},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, &stubDevices{listEntities: func(
				context.Context, devices.ListEntitiesParams,
			) (devices.Page[devices.EntityWithState], error) {
				return devices.Page[devices.EntityWithState]{}, test.err
			}})
			if response := performRequest(router, "/v1/entities"+test.query); response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestListEntitiesCursorIsScopedToSelector(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{listEntities: func(
		_ context.Context, params devices.ListEntitiesParams,
	) (devices.Page[devices.EntityWithState], error) {
		return devices.Page[devices.EntityWithState]{
			Items: []devices.EntityWithState{apiEntityWithState(nil)}, HasMore: params.AfterID == nil,
		}, nil
	}})
	first := performRequest(router, "/v1/entities?label_id="+string(apiAreaID))
	var page EntityCollectionBody
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || page.NextCursor == nil {
		t.Fatalf("first page = %s, %v", first.Body.String(), err)
	}
	same := performRequest(router, "/v1/entities?label_id="+string(apiAreaID)+"&cursor="+*page.NextCursor)
	if same.Code != http.StatusOK {
		t.Fatalf("same selector = %d: %s", same.Code, same.Body.String())
	}
	for _, query := range []string{
		"?cursor=", "?label_id=" + string(apiTagID) + "&cursor=", "?label_id=" + string(apiAreaID) + "&type=hearth.power/v1&cursor=",
	} {
		if response := performRequest(
			router,
			"/v1/entities"+query+*page.NextCursor,
		); response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d: %s", query, response.Code, response.Body.String())
		}
	}
}

func equalAreaEdit(a, b *devices.AreaEdit) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.AreaID == nil || b.AreaID == nil {
		return a.AreaID == b.AreaID
	}
	return *a.AreaID == *b.AreaID
}

func equalTagEdit(a, b *devices.TagEdit) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.TagIDs) != len(b.TagIDs) || (a.TagIDs == nil) != (b.TagIDs == nil) {
		return false
	}
	for index := range a.TagIDs {
		if a.TagIDs[index] != b.TagIDs[index] {
			return false
		}
	}
	return true
}
