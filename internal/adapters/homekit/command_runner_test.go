package homekit //nolint:testpackage // Drives the Adapter's internal HAP admission and Command batching.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type executedWrite struct {
	id, operation string
	parameters    any
}

type recordingExecutor struct {
	writes chan executedWrite
	err    error
}

func (recorder recordingExecutor) ExecuteCommand(ctx context.Context, id, operation string, parameters any) error {
	select {
	case recorder.writes <- executedWrite{id: id, operation: operation, parameters: parameters}:
		return recorder.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLightBatchOrdersSeparateCommandsAndFillsMissingColor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		calls := make(chan executedWrite, 3)
		runner := newCommandRunner(t.Context(), recordingExecutor{writes: calls},
			func(string) Entity {
				return Entity{Enabled: true, Availability: "available",
					State: &EntityState{Value: json.RawMessage(`{"hue":30,"saturation":60}`)}}
			}, slog.New(slog.DiscardHandler))
		defer runner.close()
		require.Zero(t, runner.submit(2, writeIntent{
			EntityID: "color", Operation: "set", Group: "hsv", Field: "hue", Parameters: map[string]any{"hue": 200},
		}))
		require.Zero(t, runner.submit(2, writeIntent{
			EntityID: "level", Operation: "set", Group: "brightness", Parameters: map[string]any{"value": 40},
		}))
		require.Zero(t, runner.submit(2, writeIntent{
			EntityID: "power", Operation: "set", Group: "on", Parameters: map[string]any{"value": true},
		}))
		synctest.Wait()
		time.Sleep(writeBatchWindow)
		synctest.Wait()
		require.Len(t, calls, 3)
		assert.Equal(t, executedWrite{"power", "set", map[string]any{"value": true}}, <-calls)
		assert.Equal(t, executedWrite{"level", "set", map[string]any{"value": 40}}, <-calls)
		assert.Equal(t, executedWrite{"color", "set", map[string]any{"hue": 200, "saturation": 60}}, <-calls)
	})
}

func TestLightOffSuppressesSiblingWritesAndSignalsFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		calls := make(chan executedWrite, 2)
		finished := make(chan bool, 2)
		runner := newCommandRunner(t.Context(), recordingExecutor{
			writes: calls, err: &CommandError{HTTPStatus: http.StatusGatewayTimeout},
		}, func(string) Entity {
			return Entity{Enabled: true, Availability: "available", State: &EntityState{Value: json.RawMessage("true")}}
		}, slog.New(slog.DiscardHandler))
		defer runner.close()
		report := func(failed bool) { finished <- failed }
		require.Zero(t, runner.submit(2, writeIntent{
			EntityID: "level", Operation: "set", Group: "brightness", Parameters: map[string]any{"value": 50},
			finish: report,
		}))
		require.Zero(t, runner.submit(2, writeIntent{
			EntityID: "power", Operation: "set", Group: "on", Parameters: map[string]any{"value": false},
			finish: report,
		}))
		synctest.Wait()
		time.Sleep(writeBatchWindow)
		synctest.Wait()
		require.Len(t, calls, 1)
		assert.Equal(t, executedWrite{"power", "set", map[string]any{"value": false}}, <-calls)
		require.Len(t, finished, 2)
		assert.True(t, <-finished)
		assert.True(t, <-finished)
	})
}

func TestOnlyLastColorModeWrites(t *testing.T) {
	t.Parallel()
	hue := writeIntent{Group: "hsv", Field: "hue", Parameters: map[string]any{"hue": 200}}
	temperature := writeIntent{Group: "color_temperature", Parameters: map[string]any{"value": 250}}
	got := collapseWrites([]writeIntent{hue, temperature})
	require.Len(t, got, 1)
	assert.Equal(t, "color_temperature", got[0].Group)
	got = collapseWrites([]writeIntent{temperature, hue})
	require.Len(t, got, 1)
	assert.Equal(t, "hsv", got[0].Group)
}
