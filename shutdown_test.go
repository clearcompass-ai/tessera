// Copyright 2025 The Tessera authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tessera

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// verifyNoNewLeaks snapshots the goroutines already running and returns a check
// (intended to be deferred) that flags only goroutines created afterwards, so the
// check isn't tripped by goroutines other tests in the same binary leave running.
func verifyNoNewLeaks(t *testing.T) func() {
	opt := goleak.IgnoreCurrent()
	return func() { goleak.VerifyNone(t, opt) }
}

// TestGoGroup_ShutdownJoinsAllGoroutines is the core guarantee: shutdown does not
// return until every goroutine started via Go has fully returned.
func TestGoGroup_ShutdownJoinsAllGoroutines(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	g := newGoGroup(context.Background())

	const n = 50
	var started, exited atomic.Int64
	startedCh := make(chan struct{}, n)
	for range n {
		g.Go(func(ctx context.Context) {
			started.Add(1)
			startedCh <- struct{}{}
			<-ctx.Done()
			exited.Add(1)
		})
	}
	// Make sure every goroutine is actually running before we shut down.
	for range n {
		<-startedCh
	}
	if got := started.Load(); got != n {
		t.Fatalf("started=%d, want %d", got, n)
	}

	if err := g.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// shutdown joined the goroutines, so each one's body (including exited.Add) has
	// completed by the time it returned.
	if got := exited.Load(); got != n {
		t.Fatalf("exited=%d after shutdown, want %d (shutdown must join every goroutine)", got, n)
	}
}

// TestGoGroup_ShutdownCancelsGoroutineContext asserts the goroutines are stopped
// via context cancellation.
func TestGoGroup_ShutdownCancelsGoroutineContext(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	g := newGoGroup(context.Background())
	errCh := make(chan error, 1)
	g.Go(func(ctx context.Context) {
		<-ctx.Done()
		errCh <- ctx.Err()
	})

	if err := g.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("goroutine ctx.Err()=%v, want context.Canceled", err)
		}
	default:
		t.Fatal("goroutine did not observe context cancellation before shutdown returned")
	}
}

// TestGoGroup_ShutdownRespectsDeadline asserts shutdown gives up waiting when its
// context is done, returning that error, while still having signalled the
// goroutines to stop.
func TestGoGroup_ShutdownRespectsDeadline(t *testing.T) {
	g := newGoGroup(context.Background())

	release := make(chan struct{})
	g.Go(func(ctx context.Context) {
		// Deliberately ignore ctx cancellation until released, to force the
		// deadline path.
		<-release
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := g.shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown err=%v, want context.DeadlineExceeded", err)
	}

	// Release and join cleanly so we don't leak the stubborn goroutine.
	close(release)
	if err := g.shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown: %v", err)
	}
}

// TestGoGroup_ShutdownIdempotent asserts shutdown can be called repeatedly.
func TestGoGroup_ShutdownIdempotent(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	g := newGoGroup(context.Background())
	g.Go(func(ctx context.Context) { <-ctx.Done() })

	for i := range 3 {
		if err := g.shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown #%d: %v", i, err)
		}
	}
}

// TestGoGroup_ShutdownNoGoroutines asserts shutdown returns promptly when nothing
// was started.
func TestGoGroup_ShutdownNoGoroutines(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	g := newGoGroup(context.Background())
	if err := g.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// TestGoGroup_ParentCancellationStopsGoroutines asserts the historical contract is
// preserved: cancelling the context passed to newGoGroup (i.e. the NewAppender
// context) stops the goroutines, even without an explicit shutdown call.
func TestGoGroup_ParentCancellationStopsGoroutines(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	parent, cancelParent := context.WithCancel(context.Background())
	g := newGoGroup(parent)

	exited := make(chan struct{})
	g.Go(func(ctx context.Context) {
		<-ctx.Done()
		close(exited)
	})

	cancelParent()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not stop on parent context cancellation")
	}

	// shutdown is still safe and returns promptly once already cancelled.
	if err := g.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown after parent cancel: %v", err)
	}
}

// TestAppenderImplementsShutdown documents the feature-detection contract that
// consumers (e.g. the ledger) rely on: *Appender satisfies an anonymous
// Shutdown(context.Context) error interface. Upstream Tessera, which lacks this
// method, would fail this assertion — that's exactly how a consumer distinguishes
// the two and degrades gracefully.
func TestAppenderImplementsShutdown(t *testing.T) {
	var a any = &Appender{}
	if _, ok := a.(interface{ Shutdown(context.Context) error }); !ok {
		t.Fatal("*Appender must implement Shutdown(context.Context) error for consumer feature-detection")
	}
}

// TestAppenderShutdownNilFields asserts Shutdown is safe on an Appender that was
// not constructed via NewAppender (drain and lc are nil), which is how the
// storage drivers build the bare struct.
func TestAppenderShutdownNilFields(t *testing.T) {
	a := &Appender{Add: func(context.Context, *Entry) IndexFuture { return nil }}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown on bare Appender: %v", err)
	}
}
