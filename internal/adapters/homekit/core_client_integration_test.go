//go:build integration

package homekit_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/homekit"
)

const (
	testDeviceID  = "dev_01900000-0000-7000-8000-000000000001"
	testEntityID  = "ent_01900000-0000-7000-8000-000000000001"
	testCommandID = "cmd_01900000-0000-7000-8000-000000000001"
)

func TestCoreClientReadsAllPagesInCanonicalUnits(t *testing.T) {
	t.Parallel()
	const cursor = "opaque+/="
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !assert.Equal(t, http.MethodGet, req.Method) || !assert.Equal(t, "200", req.URL.Query().Get("limit")) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch req.URL.Path {
		case "/v1/devices":
			fmt.Fprintf(w, `{"items":[{"id":%q,"name":"Kitchen","kind":"light"}]}`, testDeviceID)
		case "/v1/entities":
			if !assert.Equal(t, "canonical", req.URL.Query().Get("units")) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			switch req.URL.Query().Get("cursor") {
			case "":
				rows := make([]json.RawMessage, 0, 200)
				for i := 1; i <= 200; i++ {
					rows = append(rows, inventoryItem(i, "null"))
				}
				assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"items": rows, "next_cursor": cursor}))
			case cursor:
				state := `{"value":21500,"observation_id":"obs_01900000-0000-7000-8000-000000000001",` +
					`"observed_at":"2026-11-19T10:00:00Z"}`
				fmt.Fprintf(w, `{"items":[%s]}`, inventoryItem(201, state))
			default:
				w.WriteHeader(http.StatusBadRequest)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	client, err := homekit.NewCoreClient(api.URL)
	require.NoError(t, err)
	inventory, err := client.ReadInventory(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Kitchen", inventory.Devices[testDeviceID].Name)
	require.Len(t, inventory.Entities, 201)
	assert.Nil(t, inventory.Entities[testEntityID].State)
	last := inventory.Entities["ent_01900000-0000-7000-8000-0000000000c9"]
	require.NotNil(t, last.State)
	assert.Equal(t, "21500", string(last.State.Value))
	assert.Equal(t, "available", last.Availability)
	assert.True(t, last.Enabled)
}

func TestCoreClientPreservesOnlyReceiptsCoreSends(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{"disabled", `{"code":"entity_disabled","command_id":"` + testCommandID + `"}`, http.StatusConflict},
		{"unavailable", `{"detail":"entity unavailable"}`, http.StatusServiceUnavailable},
		{"timeout", `{"detail":"command outcome timed out"}`, http.StatusGatewayTimeout},
		{"rejected", `{"detail":"upstream rejected command"}`, http.StatusBadGateway},
		{"satisfied", `{"status":"satisfied","command_id":"` + testCommandID + `",` +
			`"observation_id":"obs_01900000-0000-7000-8000-000000000001","value":true}`, http.StatusOK},
		{"dispatched", `{"status":"dispatched","command_id":"` + testCommandID + `"}`, http.StatusOK},
		{"not terminal", `{"status":"accepted","command_id":"` + testCommandID + `"}`, http.StatusOK},
		{"redirect not followed", "", http.StatusTemporaryRedirect},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodPost, req.Method)
				assert.Equal(t, "/v1/entities/"+testEntityID+"/commands", req.URL.Path)
				assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
				var request json.RawMessage
				assert.NoError(t, json.NewDecoder(req.Body).Decode(&request))
				assert.JSONEq(t, `{"operation":"set","parameters":{"value":true}}`, string(request))
				if tc.code == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/other")
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer api.Close()
			client, err := homekit.NewCoreClient(api.URL)
			require.NoError(t, err)
			err = client.ExecuteCommand(t.Context(), testEntityID, "set", map[string]bool{"value": true})
			assert.EqualValues(t, 1, calls.Load())
			switch tc.name {
			case "satisfied", "dispatched":
				require.NoError(t, err)
			case "not terminal":
				require.ErrorContains(t, err, "confirmed outcome")
			default:
				var commandErr *homekit.CommandError
				require.ErrorAs(t, err, &commandErr)
				assert.Equal(t, tc.code, commandErr.HTTPStatus)
				if tc.name == "disabled" {
					assert.Equal(t, "entity_disabled", commandErr.Code)
					assert.Equal(t, testCommandID, commandErr.CommandID)
				} else {
					assert.Empty(t, commandErr.Code)
					assert.Empty(t, commandErr.CommandID)
				}
			}
		})
	}
}

func inventoryItem(index int, state string) json.RawMessage {
	var item strings.Builder
	fmt.Fprintf(&item, `{"id":"ent_01900000-0000-7000-8000-%012x","device_id":%q,`, index, testDeviceID)
	fmt.Fprintf(&item, `"name":"Temperature","type":"temperature/v1","support":{},"enabled":true,`+
		`"availability":{"status":"available"},"state":%s}`, state)
	return json.RawMessage(item.String())
}
