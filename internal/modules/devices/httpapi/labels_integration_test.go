//go:build integration

package httpapi //nolint:testpackage // HTTP integration tests reuse real SQLite transport fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createLabelHTTP(t *testing.T, fixture entityEventAPIFixture, name, kind string) LabelBody {
	t.Helper()
	response := labelRequest(fixture.router, http.MethodPost, "/v1/labels",
		`{"name":"`+name+`","kind":"`+kind+`"}`, "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("create %s = %d: %s", name, response.Code, response.Body.String())
	}
	var body LabelBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestLabelsHTTPLifecycleAgainstSQLite(t *testing.T) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	area := createLabelHTTP(t, fixture, "Office", "area")
	tag := createLabelHTTP(t, fixture, "Buttons", "tag")
	if response := labelRequest(fixture.router, http.MethodPost, "/v1/labels",
		`{"name":"office","kind":"tag"}`, "application/json"); response.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d: %s", response.Code, response.Body.String())
	}
	renamed := labelRequest(
		fixture.router,
		http.MethodPatch,
		"/v1/labels/"+area.ID,
		`{"name":"Study"}`,
		mergePatchContentType,
	)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", renamed.Code, renamed.Body.String())
	}
	var list LabelCollectionBody
	if err := json.Unmarshal(performRequest(fixture.router, "/v1/labels").Body.Bytes(), &list); err != nil ||
		len(list.Items) != 2 || list.Items[0].Name != "Study" || list.Items[1].ID != tag.ID {
		t.Fatalf("list = %#v, %v", list, err)
	}
	if response := labelRequest(
		fixture.router,
		http.MethodDelete,
		"/v1/labels/"+tag.ID,
		"",
		"",
	); response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", response.Code)
	}
	if response := labelRequest(
		fixture.router,
		http.MethodDelete,
		"/v1/labels/"+tag.ID,
		"",
		"",
	); response.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", response.Code)
	}
}

func TestLabelsHTTPAssignmentSelectorAndCursorAgainstSQLite(t *testing.T) {
	t.Parallel()
	fixture := newEntityEventAPIFixture(t)
	ctx := context.Background()
	view, err := fixture.service.GetEntity(ctx, fixture.power)
	if err != nil {
		t.Fatal(err)
	}
	devicePath := "/v1/devices/" + string(view.Entity.DeviceID)
	powerPath := "/v1/entities/" + string(fixture.power)
	office := createLabelHTTP(t, fixture, "Office", "area")
	porch := createLabelHTTP(t, fixture, "Porch", "area")
	tag := createLabelHTTP(t, fixture, "Buttons", "tag")

	// An Area set on the Device is inherited by both Entities.
	response := metadataRequest(fixture.router, http.MethodPatch, devicePath, `{"area_id":"`+office.ID+`"}`)
	var device DeviceBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &device) != nil ||
		device.Area == nil || device.Area.ID != office.ID {
		t.Fatalf("device area = %d: %s", response.Code, response.Body.String())
	}
	entityIDs := func(query string) []string { return listEntityIDs(t, fixture, query) }
	if ids := entityIDs("?label_id=" + office.ID); len(ids) != 2 {
		t.Fatalf("inherited Area selector = %v", ids)
	}
	// A label-only Entity patch overrides the Area and sets tags.
	response = metadataRequest(fixture.router, http.MethodPatch, powerPath,
		`{"area_id":"`+porch.ID+`","tag_ids":["`+tag.ID+`"]}`)
	var entity EntityBody
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &entity) != nil ||
		entity.Area == nil || entity.Area.Source != "entity" || entity.Area.ID != porch.ID ||
		len(entity.Tags) != 1 || entity.Tags[0].ID != tag.ID {
		t.Fatalf("entity labels = %d: %s", response.Code, response.Body.String())
	}
	if ids := entityIDs("?label_id=" + office.ID); len(ids) != 1 || ids[0] != string(fixture.buttons) {
		t.Fatalf("Office selector = %v", ids)
	}
	if ids := entityIDs("?label_id=" + porch.ID + "," + tag.ID + "&type=hearth.power/v1"); len(ids) != 1 ||
		ids[0] != string(fixture.power) {
		t.Fatalf("combined selector = %v", ids)
	}
	// null area_id on the Entity restores inheritance.
	response = metadataRequest(fixture.router, http.MethodPatch, powerPath, `{"area_id":null}`)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &entity) != nil ||
		entity.Area == nil || entity.Area.Source != "device" || entity.Area.ID != office.ID {
		t.Fatalf("inherit = %d: %s", response.Code, response.Body.String())
	}
	assertRejectedLabelPatchesWriteNothing(t, fixture, powerPath, office.ID, tag.ID)
	assertSelectorErrorsAndCursorScope(t, fixture, office.ID, porch.ID)
	// Deleting a Label clears the Area it named.
	if response = labelRequest(
		fixture.router,
		http.MethodDelete,
		"/v1/labels/"+office.ID,
		"",
		"",
	); response.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", response.Code)
	}
	var detail DeviceDetailBody
	response = performRequest(fixture.router, "/v1/devices/"+string(view.Entity.DeviceID))
	if err = json.Unmarshal(response.Body.Bytes(), &detail); err != nil || detail.Area != nil {
		t.Fatalf("device after delete = %s, %v", response.Body.String(), err)
	}
}

func assertSelectorErrorsAndCursorScope(t *testing.T, fixture entityEventAPIFixture, officeID, porchID string) {
	t.Helper()
	var response *httptest.ResponseRecorder
	var err error
	// Selector errors.
	for _, query := range []string{
		"?label_id=lbl_01890f47-7a6b-7c4d-8e9f-0123456789ff", "?label_id=bad", "?type=nonsense",
	} {
		if response = performRequest(fixture.router, "/v1/entities"+query); response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d: %s", query, response.Code, response.Body.String())
		}
	}
	// A cursor from one selector is rejected under another.
	response = performRequest(fixture.router, "/v1/entities?limit=1&label_id="+officeID)
	var page EntityCollectionBody
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || page.NextCursor == nil {
		t.Fatalf("first page = %s, %v", response.Body.String(), err)
	}
	if response = performRequest(
		fixture.router,
		"/v1/entities?limit=1&label_id="+officeID+"&cursor="+*page.NextCursor,
	); response.Code != http.StatusOK {
		t.Fatalf("same selector = %d: %s", response.Code, response.Body.String())
	}
	if response = performRequest(
		fixture.router,
		"/v1/entities?limit=1&label_id="+porchID+"&cursor="+*page.NextCursor,
	); response.Code != http.StatusBadRequest {
		t.Fatalf("other selector = %d: %s", response.Code, response.Body.String())
	}
	if response = performRequest(
		fixture.router,
		"/v1/entities?limit=1&cursor="+*page.NextCursor,
	); response.Code != http.StatusBadRequest {
		t.Fatalf("no selector = %d: %s", response.Code, response.Body.String())
	}
}

func listEntityIDs(t *testing.T, fixture entityEventAPIFixture, query string) []string {
	t.Helper()
	listed := performRequest(fixture.router, "/v1/entities"+query)
	if listed.Code != http.StatusOK {
		t.Fatalf("%s = %d: %s", query, listed.Code, listed.Body.String())
	}
	var page EntityCollectionBody
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(page.Items))
	for index, item := range page.Items {
		ids[index] = item.ID
	}
	return ids
}

func assertRejectedLabelPatchesWriteNothing(t *testing.T, fixture entityEventAPIFixture, path, officeID, tagID string) {
	t.Helper()
	for _, body := range []string{
		`{"enabled":false,"area_id":"` + tagID + `"}`,
		`{"enabled":false,"tag_ids":["` + officeID + `"]}`,
		`{"enabled":false,"tag_ids":["lbl_01890f47-7a6b-7c4d-8e9f-0123456789ff"]}`,
	} {
		if response := metadataRequest(
			fixture.router,
			http.MethodPatch,
			path,
			body,
		); response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d: %s", body, response.Code, response.Body.String())
		}
	}
	after, err := fixture.service.GetEntity(context.Background(), fixture.power)
	if err != nil || !after.Entity.Enabled || len(after.Labels.Tags) != 1 {
		t.Fatalf("rejected patches mutated Entity = %#v, %v", after, err)
	}
}
