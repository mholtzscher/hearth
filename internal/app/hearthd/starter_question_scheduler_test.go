package hearthd //nolint:testpackage // Tests exercise package-private scheduling and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/agent"
)

// stubStarterRefresher records when each pass starts. Each pass returns the
// next scripted error; when block is set, a pass waits for its context to end.
type stubStarterRefresher struct {
	mutex  sync.Mutex
	starts []time.Time
	errs   []error
	block  bool
}

func (stub *stubStarterRefresher) RefreshStarterQuestions(ctx context.Context) error {
	stub.mutex.Lock()
	stub.starts = append(stub.starts, time.Now())
	var err error
	if len(stub.errs) > 0 {
		err, stub.errs = stub.errs[0], stub.errs[1:]
	}
	block := stub.block
	stub.mutex.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (stub *stubStarterRefresher) passes() []time.Time {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	return append([]time.Time(nil), stub.starts...)
}

func requireStarterPasses(t *testing.T, stub *stubStarterRefresher, want int, when string) {
	t.Helper()
	if got := len(stub.passes()); got != want {
		t.Fatalf("passes %s = %d, want %d", when, got, want)
	}
}

// This test fails if the first pass runs before Adapters have had the startup
// delay to report, if later passes are not one interval apart, if a failed
// pass stops the schedule, or if a stopped worker runs another pass.
func TestStarterQuestionSchedulerRunsAfterStartupDelayThenEachInterval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stub := &stubStarterRefresher{errs: []error{errors.New("provider down")}}
		logger, records := withRecording(slog.LevelInfo)
		worker := startStarterQuestionRefresh(t.Context(), logger, stub, time.Hour)
		started := time.Now()

		time.Sleep(starterQuestionStartupDelay - time.Second)
		synctest.Wait()
		requireStarterPasses(t, stub, 0, "before the startup delay")

		time.Sleep(time.Second)
		synctest.Wait()
		requireStarterPasses(t, stub, 1, "after the startup delay")
		if got := stub.passes()[0].Sub(started); got != starterQuestionStartupDelay {
			t.Fatalf("first pass after %s, want %s", got, starterQuestionStartupDelay)
		}
		logged := records.snapshot()
		if len(logged) != 1 || logged[0].Level != slog.LevelWarn {
			t.Fatalf("records after a failed pass = %v", logged)
		}
		requireRecordAttr(t, logged[0], "event", "core.starter_questions_refresh_failed")
		requireRecordAttr(t, logged[0], "stage", "unknown")

		time.Sleep(time.Hour - time.Second)
		synctest.Wait()
		requireStarterPasses(t, stub, 1, "before the first interval")
		time.Sleep(time.Second)
		synctest.Wait()
		requireStarterPasses(t, stub, 2, "after the first interval")
		if len(records.snapshot()) != 1 {
			t.Fatalf("a successful pass logged %v", records.snapshot())
		}

		if err := worker.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		requireStarterPasses(t, stub, 2, "after stop")
	})
}

// This test fails if the logged stage loses the refresh error's fixed stage,
// or if a pass that outlives its deadline is not reported as a timeout.
func TestStarterQuestionSchedulerLogsStageAndTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		replyErr := &agent.StarterQuestionRefreshError{Stage: agent.StarterQuestionStageReply}
		stub := &stubStarterRefresher{errs: []error{replyErr}}
		logger, records := withRecording(slog.LevelInfo)
		scheduler := newStarterQuestionScheduler(logger, stub, time.Hour)

		scheduler.runPass(t.Context())
		stub.mutex.Lock()
		stub.block = true
		stub.mutex.Unlock()
		scheduler.runPass(t.Context())

		logged := records.snapshot()
		if len(logged) != 2 {
			t.Fatalf("records = %v", logged)
		}
		requireRecordAttr(t, logged[0], "stage", agent.StarterQuestionStageReply)
		requireRecordAttr(t, logged[1], "stage", starterQuestionTimeoutStage)
	})
}

// A pass interrupted by shutdown is not a failure.
func TestStarterQuestionSchedulerDoesNotLogShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stub := &stubStarterRefresher{block: true}
		logger, records := withRecording(slog.LevelInfo)
		worker := startStarterQuestionRefresh(t.Context(), logger, stub, time.Hour)
		time.Sleep(starterQuestionStartupDelay)
		synctest.Wait()
		requireStarterPasses(t, stub, 1, "after the startup delay")
		if err := worker.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if logged := records.snapshot(); len(logged) != 0 {
			t.Fatalf("shutdown logged %v", logged)
		}
	})
}
