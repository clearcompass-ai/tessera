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
	"sync"
)

// goGroup owns the background goroutines started by an Appender during its
// lifetime so that they can be deterministically cancelled and waited upon.
//
// Background. NewAppender and the storage drivers start a number of long-running
// goroutines (checkpoint publication, integration, garbage collection, antispam
// followers, stats). Historically each was launched with a bare `go` statement
// bound to the context passed to NewAppender, with no completion signal: the
// shutdown function returned by NewAppender only drains in-flight Add futures, it
// neither stops nor joins those goroutines. A caller therefore had no way to
// learn when they had exited, and was forced into an impossible ordering — the
// checkpoint publisher must be alive for the drain to make progress, yet the only
// way to stop it was to cancel the very context the drain relies upon.
//
// goGroup closes that gap. It derives its own cancellable context from the one
// passed to NewAppender; cancelling the NewAppender context still stops the
// goroutines (preserving the historical contract), and Appender.Shutdown
// additionally stops them explicitly and blocks until every one has returned.
//
// goGroup is the mechanism behind Appender.Shutdown — the "Shutdown method for
// #341" anticipated by the Appender doc comment.
type goGroup struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newGoGroup returns a goGroup whose goroutines are bound to a cancellable child
// of parent.
func newGoGroup(parent context.Context) *goGroup {
	ctx, cancel := context.WithCancel(parent)
	return &goGroup{ctx: ctx, cancel: cancel}
}

// Go starts fn in a tracked goroutine bound to the group's context.
//
// fn is expected to return once the context it is passed is cancelled. Every
// goroutine started here is awaited by shutdown.
func (g *goGroup) Go(fn func(context.Context)) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		fn(g.ctx)
	}()
}

// shutdown cancels every goroutine started via Go and blocks until they have all
// returned, or until ctx is done — whichever happens first.
//
// It returns ctx.Err() if ctx is cancelled before the goroutines finish, and nil
// otherwise. Even in the timeout case the goroutines have been signalled to stop
// and will exit shortly; shutdown simply stops waiting for them.
//
// shutdown is safe to call multiple times and from multiple goroutines.
func (g *goGroup) shutdown(ctx context.Context) error {
	g.cancel()

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown performs a clean, deterministic shutdown of the Appender:
//
//  1. it drains in-flight Adds — any future returned by this Appender which
//     resolves to an index will be integrated and committed to by a published
//     checkpoint (the same guarantee as the shutdown function returned by
//     NewAppender), and then
//  2. it stops every background goroutine started by the Appender (here and
//     inside the storage driver) and blocks until they have all returned.
//
// After Shutdown returns successfully, calls to Add fail and no background
// goroutines started by this Appender remain running.
//
// The caller does NOT need to cancel the context passed to NewAppender. Doing so
// before Shutdown is both unnecessary and harmful: the checkpoint publisher is
// one of the goroutines bound to that context, so cancelling it first would
// prevent the drain in step 1 from completing. The correct pattern is simply:
//
//	if err := appender.Shutdown(ctx); err != nil { ... }
//
// Consumers that want the Appender's background work to survive cancellation of
// the request/signal context that triggered shutdown (so the drain can complete)
// should pass a context decoupled from that signal (e.g. context.WithoutCancel)
// to NewAppender, and rely solely on Shutdown for teardown.
//
// ctx bounds how long Shutdown will wait. If it is cancelled first, Shutdown
// returns its error; the background goroutines have still been signalled to stop.
//
// Shutdown is additive to the upstream NewAppender contract: the shutdown
// function returned by NewAppender continues to work as before for callers that
// do not use Shutdown. Shutdown is safe to call multiple times.
func (a *Appender) Shutdown(ctx context.Context) error {
	var errs []error
	if a.drain != nil {
		if err := a.drain(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	// Always stop and join the background goroutines, even if the drain failed
	// (e.g. ctx deadline): we must not leave them running.
	if a.lc != nil {
		if err := a.lc.shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
