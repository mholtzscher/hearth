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
	// idleWaitTimeout bounds a Wait that should have returned already. Inside a
	// synctest bubble the fake clock skips the wait, so it only bites on a real
	// stuck group.
	idleWaitTimeout = 5 * time.Second

	// testChildCount is the fan-out width one reservation must track.
	testChildCount = 3

	// concurrencyIterations widens the overlap between close and acquire.
	concurrencyIterations = 100

	// concurrentAcquirers races CloseAdmission from many goroutines.
	concurrentAcquirers = 8

	// concurrentClosers repeats CloseAdmission from many goroutines.
	concurrentClosers = 16

	// concurrentChildren widens the overlap between concurrent child starts.
	concurrentChildren = 8

	// workerPanicHelperEnv marks the re-executed test binary that panics a child.
	workerPanicHelperEnv = "LIFECYCLE_WORKER_PANIC_HELPER"

	// workerPanicGrace lets a recovered panic fall through before the helper
	// gives up; a real panic kills the process first.
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

// A new group is open and idle, so Wait joins immediately without an admission.
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

// Closing admission while a reservation is live still joins it after release.
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

// A group that went idle must reopen its idle channel for a later admission, so
// a wait taken after admission closes joins the new work instead of an
// already-closed idle channel. This is the admit, drain, admit, stop, join
// sequence the services perform.
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

// Go registers the child before its goroutine starts, so releasing the parent
// cannot let Wait observe an idle gap while the child still runs.
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

// Wait must join every child one reservation starts, not only the last one.
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

// Each tracked child must keep the group busy on its own: when siblings finish
// one at a time, Wait may not report idle before the last one finishes. This is
// the sequenced counterpart of the simultaneous fan-out above.
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

// A child finishing must not idle the group while its parent admission is still
// live: the parent's own reservation keeps the group busy until Release.
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
		// The tracked child has finished and recorded its own completion.
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

// A live reservation may start children after admission closes so shutdown can
// join work admitted before the close.
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

// A canceled Wait must not close admission, cancel admitted work, or refuse a
// later admission.
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

// Repeated Release calls release one admission exactly once, so a still-live
// second reservation keeps the group busy.
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

// Concurrent TryAcquire, CloseAdmission, and Release must leave a consistent
// group: no double-closed idle channel, no leaked reservation, and no admission
// once close has returned.
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

// Concurrent CloseAdmission calls must not close the idle channel twice.
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

// Concurrent Go calls must each register a tracked child, so a concurrent
// fan-out keeps the group busy until the last tracked child finishes.
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

// Concurrent Release calls must release one admission exactly once: the group
// stays busy while a tracked child is live and goes idle once it finishes.
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

// A panicking child must crash the process: the group never recovers worker
// panics, so it cannot silently drop tracked work.
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

// runWorkerPanicHelper panics inside a tracked child. A framework that recovered
// the panic would fall through and exit 3 instead of crashing.
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
