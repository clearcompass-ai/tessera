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

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/transparency-dev/tessera"
	storage "github.com/transparency-dev/tessera/storage/internal"
)

// TestQueue_FailsPendingOnContextCancel asserts that when the worker's context is
// cancelled, entries which have been queued but not yet flushed have their futures
// resolved with the cancellation error rather than left to block forever.
//
// This is what stops a consumer that is blocked in IndexFuture (which itself can't
// be cancelled) from leaking a goroutine per in-flight Add at shutdown.
func TestQueue_FailsPendingOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// A spawn that binds the worker to ctx (the same context we cancel below).
	spawn := func(fn func(context.Context)) { go fn(ctx) }

	// Large batch and a very long max-age so the entries sit in the queue and are
	// never auto-flushed before we cancel.
	flushed := false
	flushFunc := func(_ context.Context, _ []*tessera.Entry) error {
		flushed = true
		return nil
	}
	q := storage.NewQueue(ctx, time.Hour, 1000, spawn, flushFunc)

	const n = 7
	futures := make([]tessera.IndexFuture, n)
	for i := range n {
		futures[i] = q.Add(ctx, tessera.NewEntry([]byte{byte(i)}))
	}

	// Cancel: the worker must fail every pending future with the context error.
	cancel()

	for i, f := range futures {
		done := make(chan error, 1)
		go func() {
			_, err := f()
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("future[%d] resolved with nil error, want a cancellation error", i)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("future[%d] did not resolve after context cancel (stranded)", i)
		}
	}

	if flushed {
		t.Fatalf("flushFunc was unexpectedly called; entries should have been failed, not flushed")
	}
}

// TestQueue_NilSpawnUsesBareGoroutine asserts the historical behaviour is retained
// when no SpawnFunc is supplied: the queue still works and flushes normally.
func TestQueue_NilSpawnUsesBareGoroutine(t *testing.T) {
	ctx := t.Context()

	var gotIndices []uint64
	flushFunc := func(_ context.Context, entries []*tessera.Entry) error {
		for _, e := range entries {
			idx := uint64(len(gotIndices))
			_ = e.MarshalBundleData(idx)
			gotIndices = append(gotIndices, idx)
		}
		return nil
	}
	q := storage.NewQueue(ctx, time.Millisecond, 4, nil, flushFunc)

	const n = 10
	futures := make([]tessera.IndexFuture, n)
	for i := range n {
		futures[i] = q.Add(ctx, tessera.NewEntry([]byte{byte(i)}))
	}
	for i, f := range futures {
		if _, err := f(); err != nil {
			t.Fatalf("future[%d]: %v", i, err)
		}
	}
	if len(gotIndices) != n {
		t.Fatalf("flushed %d entries, want %d", len(gotIndices), n)
	}
}
