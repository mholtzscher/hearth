package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const (
	// Bound a stuck Wait; synctest advances the clock without a real delay.
	idleWaitTimeout = 5 * time.Second

	// One reservation must track multiple children.
	testChildCount = 3

	// Repeat the race to exercise different interleavings.
	concurrencyIterations = 100

	// Race acquisitions against admission closure.
	concurrentAcquirers = 8

	// Exercise concurrent CloseAdmission and Release calls.
	concurrentClosers = 16

	// Start children concurrently on one reservation.
	concurrentChildren = 8

	// Select the subprocess that panics in a tracked child.
	workerPanicHelperEnv = "LIFECYCLE_WORKER_PANIC_HELPER"

	// Allow an unrecovered panic to terminate the subprocess before fallback exit.
	workerPanicGrace = 200 * time.Millisecond
)

// requireIdle fails when Wait does not report the group idle promptly.
func requireIdle(t *testing.T, group *lifecycle.AdmissionGroup) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), idleWaitTimeout)
	defer cancel()
	if err := group.Wait(ctx); err != nil {
		t.Fatalf("AdmissionGroup.Wait returned %v, want nil while idle", err)
	}
}

// An empty group must not block Wait.
func TestNewAdmissionGroupStartsOpenAndIdle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		if !group.AdmissionOpen() {
			t.Fatal("AdmissionOpen = false, want true for a new group")
		}
		requireIdle(t, group)
	})
}

// CloseAdmission permanently refuses TryAcquire and repeated calls are safe.
func TestCloseAdmissionRefusesTryAcquirePermanently(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		group.CloseAdmission()
		group.CloseAdmission()
		if group.AdmissionOpen() {
			t.Fatal("AdmissionOpen = true after CloseAdmission")
		}
		if reservation, ok := group.TryAcquire(); ok {
			t.Fatalf("TryAcquire returned %#v, want refusal after close", reservation)
		}
		requireIdle(t, group)
	})
}

// Closing admission must not prevent an existing reservation from draining.
func TestCloseAdmissionWhileReservedJoinsAfterRelease(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		group.CloseAdmission()
		if _, ok = group.TryAcquire(); ok {
			t.Fatal("TryAcquire succeeded after close while a reservation was live")
		}
		reservation.Release()
		requireIdle(t, group)
	})
}

// A later admission must replace the closed idle channel so Wait tracks new work.
func TestWaitReopensIdleForLaterAdmission(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		first, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		first.Release()
		requireIdle(t, group)

		second, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group after it went idle")
		}
		group.CloseAdmission()
		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("Wait returned %v for a later admission, want it to stay busy", err)
		default:
		}
		second.Release()
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after the later admission released, want nil", err)
		}
	})
}

// Releasing the parent must not leave an idle gap before the child finishes.
func TestReleaseDoesNotIdleGroupWhileChildRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		childStarted := make(chan struct{})
		childFinish := make(chan struct{})
		reservation.Go(func() {
			close(childStarted)
			<-childFinish
		})
		group.CloseAdmission()
		reservation.Release()

		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("Wait returned %v while the child goroutine was live", err)
		default:
		}
		select {
		case <-childStarted:
		default:
			t.Fatal("child goroutine did not start")
		}
		close(childFinish)
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after the child finished, want nil", err)
		}
	})
}

// One reservation must track all children it starts.
func TestReservationGoTracksEveryChild(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		var started atomic.Int64
		childFinish := make(chan struct{})
		for range testChildCount {
			reservation.Go(func() {
				started.Add(1)
				<-childFinish
			})
		}
		group.CloseAdmission()
		reservation.Release()

		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		if got := started.Load(); got != testChildCount {
			t.Fatalf("started children = %d, want %d", got, testChildCount)
		}
		select {
		case err := <-waited:
			t.Fatalf("Wait returned %v while children were live", err)
		default:
		}
		close(childFinish)
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after all children finished, want nil", err)
		}
	})
}

// Finish siblings separately to catch a join that returns after only one child.
func TestReservationGoJoinsChildrenThatFinishOneAtATime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		childFinish := make([]chan struct{}, testChildCount)
		for index := range testChildCount {
			childFinish[index] = make(chan struct{})
			reservation.Go(func() { <-childFinish[index] })
		}
		group.CloseAdmission()
		reservation.Release()

		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		for index := range testChildCount {
			close(childFinish[index])
			synctest.Wait()
			if index == testChildCount-1 {
				break
			}
			select {
			case err := <-waited:
				t.Fatalf("Wait returned %v after child %d of %d finished, want the later children joined",
					err, index+1, testChildCount)
			default:
			}
		}
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after the last child finished, want nil", err)
		}
	})
}

// The parent reservation must keep the group busy after its child finishes.
func TestChildCompletionKeepsGroupBusyWhileParentReserved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		childStarted := make(chan struct{})
		childFinish := make(chan struct{})
		reservation.Go(func() {
			close(childStarted)
			<-childFinish
		})
		<-childStarted
		close(childFinish)
		// Let the child release its tracking slot.
		synctest.Wait()

		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("Wait returned %v after the child finished but before Release", err)
		default:
		}
		reservation.Release()
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after Release, want nil", err)
		}
	})
}

// Admission closure must not prevent an existing reservation from starting work.
func TestGoAllowedAfterCloseAdmissionWhileReservationLive(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		group.CloseAdmission()
		childRan := make(chan struct{})
		reservation.Go(func() { close(childRan) })
		synctest.Wait()
		select {
		case <-childRan:
		default:
			t.Fatal("child goroutine did not run after admission closed")
		}
		reservation.Release()
		requireIdle(t, group)
	})
}

// Go after Release must panic instead of leaking an untracked child.
func TestGoAfterReleasePanics(t *testing.T) {
	t.Parallel()
	group := lifecycle.NewAdmissionGroup()
	reservation, ok := group.TryAcquire()
	if !ok {
		t.Fatal("TryAcquire refused an open group")
	}
	reservation.Release()
	defer func() {
		if recover() == nil {
			t.Error("Reservation.Go after Release did not panic")
		}
	}()
	reservation.Go(func() {})
}

// Canceling Wait must leave admission open and existing work tracked.
func TestWaitContextCancellationHasNoSideEffects(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		childFinish := make(chan struct{})
		reservation.Go(func() { <-childFinish })

		waitContext, cancelWait := context.WithTimeout(t.Context(), time.Second)
		defer cancelWait()
		if err := group.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait error = %v, want %v while work was live", err, context.DeadlineExceeded)
		}
		if !group.AdmissionOpen() {
			t.Fatal("canceled Wait closed admission")
		}
		second, ok := group.TryAcquire()
		if !ok {
			t.Fatal("canceled Wait refused a later admission")
		}
		second.Release()
		reservation.Release()
		close(childFinish)
		requireIdle(t, group)
	})
}

// Releasing one reservation twice must not release another reservation's slot.
func TestReleaseRepeatedIsIdempotent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		first, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		second, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		first.Release()
		first.Release()

		waitContext, cancelWait := context.WithTimeout(t.Context(), time.Second)
		defer cancelWait()
		if err := group.Wait(waitContext); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Wait error = %v after repeated Release, want %v while a reservation is live",
				err, context.DeadlineExceeded)
		}

		second.Release()
		requireIdle(t, group)
	})
}

// Racing acquisition and closure must leave the group closed and idle.
func TestCloseAdmissionConcurrentWithAcquireLeavesGroupIdle(t *testing.T) {
	t.Parallel()
	for range concurrencyIterations {
		group := lifecycle.NewAdmissionGroup()
		start := make(chan struct{})
		var waitGroup sync.WaitGroup
		for range concurrentAcquirers {
			waitGroup.Go(func() {
				<-start
				if reservation, ok := group.TryAcquire(); ok {
					reservation.Release()
				}
			})
		}
		waitGroup.Go(func() {
			<-start
			group.CloseAdmission()
		})
		close(start)
		waitGroup.Wait()

		if group.AdmissionOpen() {
			t.Fatal("AdmissionOpen = true after CloseAdmission returned")
		}
		if reservation, ok := group.TryAcquire(); ok {
			t.Fatalf("TryAcquire returned %#v after CloseAdmission returned", reservation)
		}
		requireIdle(t, group)
	}
}

// Concurrent closure must be idempotent.
func TestCloseAdmissionRepeatedConcurrentlyIsIdempotent(t *testing.T) {
	t.Parallel()
	group := lifecycle.NewAdmissionGroup()
	var waitGroup sync.WaitGroup
	for range concurrentClosers {
		waitGroup.Go(func() {
			group.CloseAdmission()
		})
	}
	waitGroup.Wait()
	if group.AdmissionOpen() {
		t.Fatal("AdmissionOpen = true after concurrent CloseAdmission calls")
	}
	requireIdle(t, group)
}

// Concurrent Go calls must register every child before the parent releases.
func TestConcurrentGoRegistersEveryChild(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		childFinish := make(chan struct{})
		var started sync.WaitGroup
		started.Add(concurrentChildren)
		var registered sync.WaitGroup
		for range concurrentChildren {
			registered.Go(func() {
				reservation.Go(func() {
					started.Done()
					<-childFinish
				})
			})
		}
		registered.Wait()
		started.Wait()
		reservation.Release()

		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("Wait returned %v while %d tracked children were live", err, concurrentChildren)
		default:
		}
		close(childFinish)
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after every child finished, want nil", err)
		}
	})
}

// Concurrent releases must not consume the live child's tracking slot.
func TestConcurrentReleaseJoinsLiveChild(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		group := lifecycle.NewAdmissionGroup()
		reservation, ok := group.TryAcquire()
		if !ok {
			t.Fatal("TryAcquire refused an open group")
		}
		childFinish := make(chan struct{})
		var childStarted sync.WaitGroup
		childStarted.Add(1)
		reservation.Go(func() {
			childStarted.Done()
			<-childFinish
		})
		childStarted.Wait()

		var releases sync.WaitGroup
		for range concurrentClosers {
			releases.Go(reservation.Release)
		}
		releases.Wait()

		waited := make(chan error, 1)
		go func() { waited <- group.Wait(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-waited:
			t.Fatalf("Wait returned %v while the tracked child was live after concurrent Release", err)
		default:
		}
		close(childFinish)
		if err := <-waited; err != nil {
			t.Fatalf("Wait returned %v after the child finished, want nil", err)
		}
	})
}

// A worker panic must terminate the subprocess, not be swallowed.
func TestGoWorkerPanicIsNotRecovered(t *testing.T) {
	t.Parallel()
	if os.Getenv(workerPanicHelperEnv) == "1" {
		runWorkerPanicHelper()
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestGoWorkerPanicIsNotRecovered$")
	command.Env = append(os.Environ(), workerPanicHelperEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("worker panic subprocess exited successfully, want a panic; output:\n%s", output)
	}
	if !strings.Contains(string(output), "panic: lifecycle worker panic sentinel") {
		t.Fatalf("worker panic subprocess did not report the child panic; output:\n%s", output)
	}
}

// runWorkerPanicHelper exits 3 if the tracked child's panic does not crash the process.
func runWorkerPanicHelper() {
	group := lifecycle.NewAdmissionGroup()
	reservation, ok := group.TryAcquire()
	if !ok {
		os.Exit(4)
	}
	childStarted := make(chan struct{})
	reservation.Go(func() {
		close(childStarted)
		panic("lifecycle worker panic sentinel")
	})
	<-childStarted
	time.Sleep(workerPanicGrace)
	fmt.Fprintln(os.Stderr, "lifecycle worker panic was recovered")
	os.Exit(3)
}
