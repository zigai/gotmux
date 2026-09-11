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

func isContextError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var cmdErr *tmux.CommandError
	if errors.As(err, &cmdErr) {
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

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()

			// A subset of goroutines (idx % 5 == 0) use an early-canceled context
			// to exercise request cancellation under concurrent dispatch.
			var reqCtx context.Context
			var cancel context.CancelFunc

			isCanceledSubset := (idx%5 == 0)
			if isCanceledSubset {
				reqCtx, cancel = context.WithTimeout(ctx, 100*time.Microsecond)
			} else {
				reqCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
			}
			defer cancel()

			switch idx % 4 {
			case 0:
				// Query bound panes
				panes, err := bound.Panes(reqCtx)
				if err != nil {
					if isCanceledSubset && isContextError(err) {
						return
					}
					errCh <- fmt.Errorf("goroutine %d bound.Panes: %w", idx, err)
					return
				}
				if len(panes) == 0 {
					errCh <- fmt.Errorf("goroutine %d expected at least 1 pane", idx)
				}

			case 1:
				// Query session windows
				links, err := session.Windows(reqCtx)
				if err != nil {
					if isCanceledSubset && isContextError(err) {
						return
					}
					errCh <- fmt.Errorf("goroutine %d session.Windows: %w", idx, err)
					return
				}
				if len(links) == 0 {
					errCh <- fmt.Errorf("goroutine %d expected at least 1 window", idx)
				}

			case 2:
				// Query options
				titles, err := session.Options().Titles(reqCtx)
				if err != nil {
					if isCanceledSubset && isContextError(err) {
						return
					}
					errCh <- fmt.Errorf("goroutine %d session.Options.Titles: %w", idx, err)
					return
				}
				if _, ok := titles.Effective.Get(); !ok && !isCanceledSubset {
					errCh <- fmt.Errorf("goroutine %d expected titles option value", idx)
				}

			case 3:
				// Create and kill a scratch window
				var opts tmux.NewWindowOptions
				opts.Name = fmt.Sprintf("scratch-%d", idx)
				link, err := session.NewWindow(reqCtx, opts)
				if err != nil {
					if isCanceledSubset && isContextError(err) {
						return
					}
					errCh <- fmt.Errorf("goroutine %d session.NewWindow: %w", idx, err)
					return
				}
				if err := link.Window().Kill(ctx); err != nil {
					errCh <- fmt.Errorf("goroutine %d kill scratch window: %w", idx, err)
				}
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
