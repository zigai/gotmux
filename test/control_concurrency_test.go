//go:build integration

package tmux_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

// errEmptyResult marks a concurrent query that succeeded but returned nothing.
var errEmptyResult = errors.New("empty result")

func isContextError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	if cmdErr, ok := errors.AsType[*tmux.CommandError](err); ok {
		return errors.Is(cmdErr.Err, context.Canceled) || errors.Is(cmdErr.Err, context.DeadlineExceeded)
	}

	return false
}

func TestControlConcurrentRequests(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection := apiControl(t, server, session, ctx)
	bound := connection.Server()

	const concurrency = 40

	var wg sync.WaitGroup
	wg.Add(concurrency)

	errCh := make(chan error, concurrency)

	for i := range concurrency {
		go func(idx int) {
			defer wg.Done()

			// A subset of goroutines (idx % 5 == 0) use an early-canceled context
			// to exercise request cancellation under concurrent dispatch.
			canceled := idx%5 == 0

			timeout := 15 * time.Second
			if canceled {
				timeout = 100 * time.Microsecond
			}

			reqCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			if err := concurrentRequest(ctx, reqCtx, bound, session, idx, canceled); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// Assert that no race or deadlock occurs, and connection.Close() succeeds cleanly.
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- connection.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("connection.Close() failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("connection.Close() deadlocked or timed out")
	}
}

func concurrentRequest(ctx, reqCtx context.Context, bound *tmux.Server, session tmux.Session, idx int, canceled bool) error {
	switch idx % 4 {
	case 0:
		return concurrentPanes(reqCtx, bound, idx, canceled)
	case 1:
		return concurrentWindows(reqCtx, session, idx, canceled)
	case 2:
		return concurrentTitles(reqCtx, session, idx, canceled)
	default:
		return concurrentScratchWindow(ctx, reqCtx, session, idx, canceled)
	}
}

func concurrentPanes(reqCtx context.Context, bound *tmux.Server, idx int, canceled bool) error {
	panes, err := bound.Panes(reqCtx)
	if err != nil {
		return requestError(err, idx, "bound.Panes", canceled)
	}

	if len(panes) == 0 {
		return fmt.Errorf("goroutine %d panes: %w", idx, errEmptyResult)
	}

	return nil
}

func concurrentWindows(reqCtx context.Context, session tmux.Session, idx int, canceled bool) error {
	links, err := session.Windows(reqCtx)
	if err != nil {
		return requestError(err, idx, "session.Windows", canceled)
	}

	if len(links) == 0 {
		return fmt.Errorf("goroutine %d windows: %w", idx, errEmptyResult)
	}

	return nil
}

func concurrentTitles(reqCtx context.Context, session tmux.Session, idx int, canceled bool) error {
	titles, err := session.Options().Titles(reqCtx)
	if err != nil {
		return requestError(err, idx, "session.Options.Titles", canceled)
	}

	if _, ok := titles.Effective.Get(); !ok && !canceled {
		return fmt.Errorf("goroutine %d titles option: %w", idx, errEmptyResult)
	}

	return nil
}

// The kill uses ctx, so an early-canceled reqCtx cannot skip it.
func concurrentScratchWindow(ctx, reqCtx context.Context, session tmux.Session, idx int, canceled bool) error {
	var opts tmux.NewWindowOptions

	opts.Name = fmt.Sprintf("scratch-%d", idx)

	link, err := session.NewWindow(reqCtx, opts)
	if err != nil {
		return requestError(err, idx, "session.NewWindow", canceled)
	}

	if err := link.Window().Kill(ctx); err != nil {
		return fmt.Errorf("goroutine %d kill scratch window: %w", idx, err)
	}

	return nil
}

// requestError ignores context errors in the early-canceled subset.
func requestError(err error, idx int, request string, canceled bool) error {
	if canceled && isContextError(err) {
		return nil
	}

	return fmt.Errorf("goroutine %d %s: %w", idx, request, err)
}
