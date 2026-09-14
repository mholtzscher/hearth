package lifecycle_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

func TestWorkerHandlePublishesResultAfterCleanup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		terminal := errors.New("worker failed")
		cleanup := make(chan struct{})
		release := sync.OnceFunc(func() { close(cleanup) })
		defer release()
		contexts := make(chan context.Context, 1)
		worker := lifecycle.StartWorker(t.Context(), func(ctx context.Context) error {
			contexts <- ctx
			defer func() { <-cleanup }()
			return terminal
		})
		synctest.Wait()
		select {
		case <-worker.Closed():
			t.Fatal("Closed before worker cleanup returned")
		default:
		}
		release()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		for range 2 {
			if err := worker.Wait(ctx); err != terminal { //nolint:errorlint // Require unchanged identity.
				t.Fatalf("Wait = %v, want original terminal error", err)
			}
		}
		if workerContext := <-contexts; workerContext.Err() != context.Canceled {
			t.Fatal("natural completion did not release the worker context")
		}
	})
}

func TestWorkerHandleWaitCancellationDoesNotCancelWork(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		finish := make(chan struct{})
		release := sync.OnceFunc(func() { close(finish) })
		defer release()
		contexts := make(chan context.Context, 1)
		worker := lifecycle.StartWorker(t.Context(), func(ctx context.Context) error {
			contexts <- ctx
			<-finish
			return nil
		})
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := worker.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait = %v, want deadline exceeded", err)
		}
		if workerContext := <-contexts; workerContext.Err() != nil {
			t.Fatal("Wait canceled the worker")
		}
		select {
		case <-worker.Closed():
			t.Fatal("Wait timeout untracked live work")
		default:
		}
		release()
		joined, cancelJoin := context.WithTimeout(t.Context(), time.Second)
		defer cancelJoin()
		if err := worker.Wait(joined); err != nil {
			t.Fatalf("later Wait = %v", err)
		}
	})
}

func TestWorkerHandleCanceledStopStillCancelsAndTracksCleanup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cleanup := make(chan struct{})
		release := sync.OnceFunc(func() { close(cleanup) })
		defer release()
		contexts := make(chan context.Context, 1)
		worker := lifecycle.StartWorker(t.Context(), func(ctx context.Context) error {
			contexts <- ctx
			<-ctx.Done()
			<-cleanup
			return ctx.Err()
		})
		synctest.Wait()
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := worker.Stop(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("Stop = %v, want canceled wait", err)
		}
		if workerContext := <-contexts; workerContext.Err() != context.Canceled {
			t.Fatal("Stop with canceled wait context did not cancel the worker")
		}
		waiting, cancelWait := context.WithTimeout(t.Context(), time.Second)
		defer cancelWait()
		if err := worker.Stop(waiting); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second Stop = %v, want deadline exceeded while cleanup is blocked", err)
		}
		select {
		case <-worker.Closed():
			t.Fatal("Stop closed tracking before worker cleanup returned")
		default:
		}
		release()
		joined, cancelJoin := context.WithTimeout(t.Context(), time.Second)
		defer cancelJoin()
		if err := worker.Wait(joined); !errors.Is(err, context.Canceled) {
			t.Fatalf("terminal result = %v, want worker's context.Canceled unchanged", err)
		}
	})
}

func TestWorkerHandleInheritsParentContext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		type contextKey struct{}
		parent, cancelParent := context.WithTimeout(context.WithValue(t.Context(), contextKey{}, "trace"), time.Second)
		defer cancelParent()
		contexts := make(chan context.Context, 1)
		worker := lifecycle.StartWorker(parent, func(ctx context.Context) error {
			contexts <- ctx
			<-ctx.Done()
			return ctx.Err()
		})
		synctest.Wait()
		workerContext := <-contexts
		if workerContext.Value(contextKey{}) != "trace" {
			t.Fatal("worker lost parent context value")
		}
		parentDeadline, _ := parent.Deadline()
		if deadline, ok := workerContext.Deadline(); !ok || !deadline.Equal(parentDeadline) {
			t.Fatal("worker lost parent deadline")
		}
		joined, cancelJoin := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancelJoin()
		if err := worker.Wait(joined); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("terminal result = %v, want parent deadline exceeded", err)
		}
		select {
		case <-worker.Closed():
		default:
			t.Fatal("parent deadline did not terminate the worker")
		}
	})
}

func TestWorkerHandleConcurrentStopAndWaitShareOneResult(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		terminal := errors.New("worker terminal result")
		var calls atomic.Int32
		worker := lifecycle.StartWorker(t.Context(), func(ctx context.Context) error {
			calls.Add(1)
			<-ctx.Done()
			return terminal
		})
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		var callers sync.WaitGroup
		for range 16 {
			callers.Go(func() {
				if err := worker.Stop(ctx); err != terminal { //nolint:errorlint // Require unchanged identity.
					t.Errorf("Stop = %v, want original terminal result", err)
				}
			})
			callers.Go(func() {
				if err := worker.Wait(ctx); err != terminal { //nolint:errorlint // Require unchanged identity.
					t.Errorf("Wait = %v, want original terminal result", err)
				}
			})
		}
		callers.Wait()
		if calls.Load() != 1 {
			t.Fatalf("worker calls = %d, want exactly one", calls.Load())
		}
	})
}
