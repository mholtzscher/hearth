package httpapi //nolint:testpackage // Tests reuse transport fixtures.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

func testPresenter() *devices.Presenter {
	return devices.NewPresenter(household.UnitSystemMetric, devices.TableConverter{})
}

func TestEntityReadRepresentationsAcrossHTTP(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(&devices.State{Value: devices.Value(`{"active":true,"x":1000,"y":9007199254740993}`)})
	view.Entity.TypeID = devices.EntityTypeColorxyV1
	view.Entity.Support = []byte(`{}`)
	stub := &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) { return view, nil },
		patchEntity: func(context.Context, devices.EntityID, devices.EntityPatch) (devices.EntityWithState, error) {
			return view, nil
		},
		listEntities: func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
			return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}}, nil
		},
		getDevice: func(context.Context, devices.GetDeviceParams) (devices.DeviceAggregate, error) {
			return devices.DeviceAggregate{
				Entities: devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}},
			}, nil
		},
	}
	router, _ := testAPI(t, stub)
	for _, path := range []string{"/v1/entities/" + string(apiEntityID), "/v1/entities", "/v1/devices/" + string(apiDeviceID)} {
		for _, tc := range []struct{ query, value, unit string }{{"", `"x":0.1`, `"unit":"1"`}, {"?units=canonical", `"y":9007199254740993`, `"unit":"10*-4"`}} {
			rec := performRequest(router, path+tc.query)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.value)
			require.Contains(t, rec.Body.String(), tc.unit)
			require.Contains(t, rec.Body.String(), `"quantities"`)
		}
	}
	for _, query := range []string{"", "?units=canonical"} {
		req := httptest.NewRequest(
			http.MethodPatch,
			"/v1/entities/"+string(apiEntityID)+query,
			strings.NewReader(`{"enabled":true}`),
		)
		req.Header.Set("Content-Type", "application/merge-patch+json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		if query == "" {
			require.Contains(t, rec.Body.String(), `"x":0.1`)
		} else {
			require.Contains(t, rec.Body.String(), `"y":9007199254740993`)
		}
	}
	for _, path := range []string{"/v1/entities/" + string(apiEntityID), "/v1/entities", "/v1/devices/" + string(apiDeviceID)} {
		rec := performRequest(router, path+"?units=private_sentinel")
		require.GreaterOrEqual(t, rec.Code, 400)
		require.Less(t, rec.Code, 500)
		require.NotContains(t, rec.Body.String(), "private_sentinel")
	}
	view.State = nil
	rec := performRequest(router, "/v1/entities/"+string(apiEntityID))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"state":null`)
	require.Contains(t, rec.Body.String(), `"/x"`)
	view.Entity.TypeID = devices.EntityTypeColortempV1
	view.Entity.Support = []byte(`{"state":{"minimum":153,"maximum":500}}`)
	rec = performRequest(router, "/v1/entities/"+string(apiEntityID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"range":{"minimum":153,"maximum":500}`)
	view.Entity.TypeID = devices.EntityTypeNumericsettingV1
	view.Entity.Support = []byte(
		`{"state":{"minimum":0,"maximum":100,"unit":"%","choices":["auto"]},"operations":{"set":{}}}`,
	)
	view.State = &devices.State{Value: []byte(`{"mode":"choice","choice":"auto"}`)}
	rec = performRequest(router, "/v1/entities/"+string(apiEntityID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"choice":"auto"`)
	var body struct {
		State struct {
			Value map[string]any `json:"value"`
		} `json:"state"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotContains(t, body.State.Value, "value")
}

func TestEntityWithEqualBoundsRemainsReadable(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(&devices.State{Value: []byte(`7`)})
	view.Entity.TypeID = devices.EntityTypeNumericsensorV1
	view.Entity.Support = []byte(
		`{"state":{"unit":"W","quantity_kind":"electrical_power","minimum":7,"maximum":7},"operations":{}}`,
	)
	stub := &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) { return view, nil },
		listEntities: func(context.Context, devices.ListEntitiesParams) (devices.Page[devices.EntityWithState], error) {
			return devices.Page[devices.EntityWithState]{Items: []devices.EntityWithState{view}}, nil
		},
	}
	router, _ := testAPI(t, stub)
	for _, path := range []string{"/v1/entities/" + string(apiEntityID), "/v1/entities"} {
		rec := performRequest(router, path)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), `"value":7`)
		require.Contains(t, rec.Body.String(), `"unit":"W"`)
		require.NotContains(t, rec.Body.String(), `"range"`)
	}
}
