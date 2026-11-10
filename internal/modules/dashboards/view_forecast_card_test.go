package dashboards_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestForecastEntityValidationOnPresentAndSave(t *testing.T) {
	t.Parallel()
	const id = "ent_01900000-0000-7000-8000-000000000201"
	for _, typ := range []string{"hearth.hourlyforecast/v1", "hearth.dailyforecast/v1"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			d := cardDefinition(
				t,
				`{"type":"Forecast","props":{"source":{"kind":"entity-state","entity_id":"`+id+`","entity_type":"`+typ+`"}}}`,
			)
			for _, tc := range []struct {
				name   string
				reader dashboards.EntityReader
				valid  bool
			}{
				{"valid", chartReader{id: {Entity: devices.Entity{ID: id, TypeID: devices.EntityTypeID(typ)}}}, true},
				{"wrong type", chartReader{id: {Entity: devices.Entity{ID: id, TypeID: "hearth.temperature/v1"}}}, false},
				{"missing", entityReader{err: devices.ErrEntityNotFound}, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					repo := &bindingWriteRecorder{}
					service := dashboards.NewService(repo, tc.reader, nil)
					_, presentErr := service.PresentView(context.Background(), d)
					_, createErr := service.CreateDashboard(context.Background(), d)
					_, saveErr := service.SaveDashboard(
						context.Background(),
						dashboards.SaveDashboardParams{
							ID:               "dash_01900000-0000-7000-8000-000000000001",
							ExpectedRevision: 1,
							Definition:       d,
						},
					)
					if tc.valid {
						if presentErr != nil || createErr != nil || saveErr != nil {
							t.Fatalf("present=%v create=%v save=%v", presentErr, createErr, saveErr)
						}
					} else if !errors.Is(presentErr, dashboards.ErrInvalidDefinition) || !errors.Is(createErr, dashboards.ErrInvalidDefinition) || !errors.Is(saveErr, dashboards.ErrInvalidDefinition) {
						t.Fatalf(
							"present=%v create=%v save=%v, want invalid definition",
							presentErr,
							createErr,
							saveErr,
						)
					}
				})
			}
		})
	}
}
