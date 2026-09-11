//go:build integration

package tmux_test

import (
	"context"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

const observationInterval = 5 * time.Millisecond

func integrationContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)

	return ctx
}

func apiFixture(t *testing.T) (*tmux.Server, tmux.Session, context.Context) {
	t.Helper()

	server := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	session, err := server.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	return server, session, ctx
}

func apiControl(t *testing.T, server *tmux.Server, session tmux.Session, ctx context.Context) *tmux.Connection {
	t.Helper()

	var options tmux.ControlOptions

	connection, err := server.OpenControl(ctx, session, options)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})

	return connection
}

func firstPane(t *testing.T, s *tmux.Server, ctx context.Context) tmux.Pane {
	t.Helper()

	panes, e := s.Panes(ctx)
	if e != nil || len(panes) == 0 {
		t.Fatalf("panes: %v (%d)", e, len(panes))
	}

	return panes[0].Handle()
}

func awaitObservation(t *testing.T, ctx context.Context, description string, observe func() bool) {
	t.Helper()

	ticker := time.NewTicker(observationInterval)
	defer ticker.Stop()

	for {
		if observe() {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %s: %v", description, ctx.Err())
		case <-ticker.C:
		}
	}
}
