package devices //nolint:testpackage // Exercises the service with a narrow read-store fixture.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type seriesReadStub struct {
	ReadRepository

	snapshot TemperatureSeriesSnapshot
	err      error
	calls    int
}

func (r *seriesReadStub) GetTemperatureSeries(
	context.Context,
	TemperatureSeriesParams,
) (TemperatureSeriesSnapshot, error) {
	r.calls++
	return r.snapshot, r.err
}

func TestGetTemperatureSeriesValidatesWindowAndEntityType(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	params := TemperatureSeriesParams{EntityID: commandTestEntityID, From: from, To: from.Add(6 * time.Hour)}
	r := &seriesReadStub{
		snapshot: TemperatureSeriesSnapshot{
			TypeID: "hearth.temperature/v1",
			Series: TemperatureSeries{EntityID: params.EntityID},
		},
	}
	svc := NewService(Stores{Reads: r}, nil, nil, Dependencies{})
	if got, err := svc.GetTemperatureSeries(t.Context(), params); err != nil || got.EntityID != params.EntityID {
		t.Fatalf("valid: %+v %v", got, err)
	}
	for _, bad := range []TemperatureSeriesParams{
		{EntityID: params.EntityID, From: from, To: from.Add(time.Hour)},
		{EntityID: params.EntityID, From: from.Add(time.Nanosecond), To: params.To},
		{EntityID: params.EntityID, From: from.In(time.FixedZone("other", 3600)), To: params.To},
		{EntityID: "bad", From: from, To: params.To},
	} {
		if _, err := svc.GetTemperatureSeries(t.Context(), bad); !errors.Is(err, ErrInvalidTemperatureSeries) {
			t.Fatalf("bad params: %v", err)
		}
	}
	if r.calls != 1 {
		t.Fatalf("invalid params called store %d times", r.calls)
	}
	r.snapshot.TypeID = "hearth.light/v1"
	if _, err := svc.GetTemperatureSeries(t.Context(), params); !errors.Is(err, ErrInvalidTemperatureSeries) {
		t.Fatalf("wrong type: %v", err)
	}
	r.err = ErrEntityNotFound
	if _, err := svc.GetTemperatureSeries(t.Context(), params); !errors.Is(err, ErrEntityNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
