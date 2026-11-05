package mosquittotest_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/iotest"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestReadinessFailsForStoppedContainersWithUnavailableLogs(t *testing.T) {
	t.Parallel()
	errLogs := errors.New("container logs unavailable during removal")
	for _, test := range []struct {
		name string
		logs func(context.Context) (io.ReadCloser, error)
	}{
		{
			name: "fetch logs",
			logs: func(context.Context) (io.ReadCloser, error) { return nil, errLogs },
		},
		{
			name: "read logs",
			logs: func(context.Context) (io.ReadCloser, error) {
				return io.NopCloser(iotest.ErrReader(errLogs)), nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// A reused Ryuk can exit during readiness. Preserve its failure instead of exhausting the broker's startup budget.
			target := stoppedReadinessTarget{logs: test.logs}
			err := wait.ForLog("Started").WithStartupTimeout(time.Second).WaitUntilReady(t.Context(), target)
			require.ErrorIs(t, err, errLogs)
			require.ErrorContains(t, err, "dead")
			require.NotErrorIs(t, err, context.DeadlineExceeded)
		})
	}
}

type stoppedReadinessTarget struct {
	wait.StrategyTarget

	logs func(context.Context) (io.ReadCloser, error)
}

func (target stoppedReadinessTarget) Logs(ctx context.Context) (io.ReadCloser, error) {
	return target.logs(ctx)
}

func (stoppedReadinessTarget) State(context.Context) (*container.State, error) {
	return &container.State{Status: container.StateDead}, nil
}
