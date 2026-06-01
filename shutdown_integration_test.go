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

// These tests exercise the end-to-end Appender.Shutdown behaviour against the
// real POSIX storage driver. They live in an external test package so they can
// import storage/posix (which imports tessera) without creating an import cycle.
package tessera_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	f_log "github.com/transparency-dev/formats/log"
	"github.com/transparency-dev/tessera"
	"github.com/transparency-dev/tessera/storage/posix"
	"go.uber.org/goleak"
	"golang.org/x/mod/sumdb/note"
)

// verifyNoNewLeaks snapshots the goroutines already running and returns a check
// (intended to be deferred) that flags only goroutines created afterwards. This
// scopes the leak check to the goroutines a test starts itself, so it isn't
// tripped by goroutines that other tests in the same binary leave running — e.g.
// the README example tests (README_test.go), which construct appenders bound to
// context.Background and deliberately never shut them down.
func verifyNoNewLeaks(t *testing.T) func() {
	opt := goleak.IgnoreCurrent()
	return func() { goleak.VerifyNone(t, opt) }
}

// newPOSIXAppender builds an Appender backed by a fresh on-disk POSIX log via the
// public tessera.NewAppender entry point (so the lifecycle group is installed and
// Shutdown is wired up). The provided ctx governs the appender's background
// goroutines; pass a context that is NOT cancelled if you want to prove Shutdown
// alone tears everything down.
func newPOSIXAppender(t *testing.T, ctx context.Context, mutate ...func(*tessera.AppendOptions)) (*tessera.Appender, func(context.Context) error, tessera.LogReader, note.Verifier) {
	t.Helper()

	driver, err := posix.New(ctx, posix.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("posix.New: %v", err)
	}

	sk, vk, err := note.GenerateKey(nil, "shutdown-test-log")
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer, err := note.NewSigner(sk)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	verifier, err := note.NewVerifier(vk)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	opts := tessera.NewAppendOptions().
		WithCheckpointSigner(signer).
		WithCheckpointInterval(100*time.Millisecond).
		WithBatching(8, 25*time.Millisecond).
		// Keep GC enabled (default-ish) so we also prove its goroutine is joined,
		// but tests can override.
		WithGarbageCollectionInterval(time.Second)
	for _, m := range mutate {
		m(opts)
	}

	appender, shutdown, reader, err := tessera.NewAppender(ctx, driver, opts)
	if err != nil {
		t.Fatalf("NewAppender: %v", err)
	}
	return appender, shutdown, reader, verifier
}

// publishedSize reads, signature-verifies, and parses the published checkpoint,
// returning the tree size it commits to.
func publishedSize(t *testing.T, vk note.Verifier, reader tessera.LogReader) uint64 {
	t.Helper()
	raw, err := reader.ReadCheckpoint(context.Background())
	if err != nil {
		t.Fatalf("ReadCheckpoint: %v", err)
	}
	cp, _, _, err := f_log.ParseCheckpoint(raw, vk.Name(), vk)
	if err != nil {
		t.Fatalf("ParseCheckpoint: %v", err)
	}
	return cp.Size
}

// resolveWithin invokes f in a goroutine and fails the test if it doesn't resolve
// within d — i.e. it catches a future that has been stranded forever.
func resolveWithin(t *testing.T, label string, d time.Duration, f tessera.IndexFuture) (tessera.Index, error) {
	t.Helper()
	type res struct {
		idx tessera.Index
		err error
	}
	ch := make(chan res, 1)
	go func() {
		idx, err := f()
		ch <- res{idx, err}
	}()
	select {
	case r := <-ch:
		return r.idx, r.err
	case <-time.After(d):
		t.Fatalf("%s: future did not resolve within %v (stranded)", label, d)
		return tessera.Index{}, nil
	}
}

// TestNewAppender_ShutdownDrainsAndJoins is the headline test. With a context that
// is never cancelled, Shutdown must (a) drain — the published checkpoint commits
// to every entry added — and (b) join — no background goroutine survives. goleak
// runs after Shutdown but before any context cancellation, so it can only pass if
// Shutdown itself tore everything down.
func TestNewAppender_ShutdownDrainsAndJoins(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	ctx := context.Background()
	appender, _, reader, vk := newPOSIXAppender(t, ctx)

	const n = 40
	futures := make([]tessera.IndexFuture, n)
	for i := range n {
		futures[i] = appender.Add(ctx, tessera.NewEntry(fmt.Appendf(nil, "entry-%d", i)))
	}
	for i, f := range futures {
		if _, err := f(); err != nil {
			t.Fatalf("resolve future[%d]: %v", i, err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := appender.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if got := publishedSize(t, vk, reader); got < n {
		t.Fatalf("published checkpoint size=%d, want >= %d (drain must publish all added entries)", got, n)
	}

	// Adds after Shutdown must fail.
	if _, err := appender.Add(ctx, tessera.NewEntry([]byte("late")))(); err == nil {
		t.Fatal("Add after Shutdown succeeded, want an error")
	}
}

// TestNewAppender_ShutdownReleasesUnresolvedFutures proves no future is left
// stranded: entries that are still sitting in the batcher when Shutdown is called
// must resolve (with an index or an error) rather than block forever. This is the
// exact failure mode that previously leaked a goroutine per in-flight Add in the
// consumer.
func TestNewAppender_ShutdownReleasesUnresolvedFutures(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	ctx := context.Background()
	// Huge batch + long max-age so the entries never auto-flush before Shutdown.
	appender, _, _, _ := newPOSIXAppender(t, ctx, func(o *tessera.AppendOptions) {
		o.WithBatching(10000, time.Hour)
	})

	const n = 6
	futures := make([]tessera.IndexFuture, n)
	for i := range n {
		futures[i] = appender.Add(ctx, tessera.NewEntry(fmt.Appendf(nil, "pending-%d", i)))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Drain may legitimately report nothing was committed; we only care that
	// Shutdown returns and releases the futures.
	_ = appender.Shutdown(shutdownCtx)

	for i, f := range futures {
		// We don't assert success or failure, only that it resolves (no hang).
		_, _ = resolveWithin(t, fmt.Sprintf("pending future[%d]", i), 10*time.Second, f)
	}
}

// TestNewAppender_ShutdownIdempotent asserts Shutdown can be called more than once.
func TestNewAppender_ShutdownIdempotent(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	ctx := context.Background()
	appender, _, _, _ := newPOSIXAppender(t, ctx)
	if _, err := appender.Add(ctx, tessera.NewEntry([]byte("x")))(); err != nil {
		t.Fatalf("Add: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := appender.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown #1: %v", err)
	}
	if err := appender.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown #2: %v", err)
	}
}

// TestNewAppender_ReturnedShutdownFuncStillWorks asserts the historical two-step
// teardown (call the returned shutdown function to drain, then cancel the
// context) is unchanged for callers that don't use Appender.Shutdown.
func TestNewAppender_ReturnedShutdownFuncStillWorks(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	ctx, cancel := context.WithCancel(context.Background())
	appender, shutdown, reader, vk := newPOSIXAppender(t, ctx)

	const n = 16
	for i := range n {
		if _, err := appender.Add(ctx, tessera.NewEntry(fmt.Appendf(nil, "z-%d", i)))(); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	if err := shutdown(drainCtx); err != nil {
		t.Fatalf("returned shutdown func: %v", err)
	}
	if got := publishedSize(t, vk, reader); got < n {
		t.Fatalf("published size=%d, want >= %d", got, n)
	}

	// Step two of the historical contract: cancel the context to stop the
	// background goroutines. goleak (deferred) then verifies they're all gone.
	cancel()
}

// TestNewAppender_CancelContextStopsGoroutines asserts that cancelling the
// NewAppender context (the historical stop mechanism), without ever calling
// Shutdown, still stops every background goroutine.
func TestNewAppender_CancelContextStopsGoroutines(t *testing.T) {
	defer verifyNoNewLeaks(t)()

	ctx, cancel := context.WithCancel(context.Background())
	appender, _, _, _ := newPOSIXAppender(t, ctx)
	if _, err := appender.Add(ctx, tessera.NewEntry([]byte("active")))(); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// No Shutdown — just cancel. goleak retries while the goroutines wind down.
	cancel()
}
