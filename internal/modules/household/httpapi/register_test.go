package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/mholtzscher/hearth/internal/modules/household"
	"github.com/mholtzscher/hearth/internal/modules/household/httpapi"
)

func TestGetHouseholdReportsSettings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		settings household.Settings
		want     map[string]string
	}{
		{
			household.Settings{Timezone: "America/Chicago", UnitSystem: household.UnitSystemUS},
			map[string]string{"timezone": "America/Chicago", "unit_system": "us"},
		},
		{household.Settings{Timezone: "UTC"}, map[string]string{"timezone": "UTC", "unit_system": "metric"}},
	} {
		_, api := humatest.New(t)
		httpapi.Register(api, test.settings)
		response := api.Get("/household")
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		var got map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		delete(got, "$schema")
		if len(got) != len(test.want) || got["timezone"] != test.want["timezone"] ||
			got["unit_system"] != test.want["unit_system"] {
			t.Fatalf("body = %v, want %v", got, test.want)
		}
	}
}
