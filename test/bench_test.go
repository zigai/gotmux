//go:build integration && go1.24

package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func BenchmarkIntegrationCapture(b *testing.B) {
	s := tmuxtest.NewServer(b)
	ctx := context.Background()

	panes, err := s.Panes(ctx)
	if err != nil || len(panes) == 0 {
		b.Fatal("no pane", err)
	}

	p := panes[0].Handle()
	if _, err = p.Capture(ctx, tmux.CaptureOptions{}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err = p.Capture(ctx, tmux.CaptureOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIntegrationControlRequests(b *testing.B) {
	s := tmuxtest.NewServer(b)

	ctx := b.Context()

	session, err := s.FindSession(ctx, "fixture")
	if err != nil {
		b.Fatal(err)
	}

	conn, err := s.OpenControl(ctx, session.ID(), tmux.ControlOptions{})
	if err != nil {
		b.Fatal(err)
	}

	closeOnCleanup(b, conn)

	if _, err = conn.Server().Panes(ctx); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err = conn.Server().Panes(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

const (
	benchSessions          = 10
	benchWindowsPerSession = 2
	benchPanesPerWindow    = 3
)

func populatedServer(b *testing.B) (*tmux.Server, tmux.Session, context.Context) {
	b.Helper()

	s := tmuxtest.NewServer(b)
	ctx := b.Context()

	fixture, err := s.FindSession(ctx, "fixture")
	if err != nil {
		b.Fatal(err)
	}

	sessions := []tmux.Session{fixture}

	for i := 1; i < benchSessions; i++ {
		session, err := s.NewSession(ctx, tmux.NewSessionOptions{Name: fmt.Sprintf("bench-%d", i), Size: tmux.Size{Width: 200, Height: 60}})
		if err != nil {
			b.Fatal(err)
		}

		sessions = append(sessions, session)
	}

	for _, session := range sessions {
		populateSession(b, ctx, session)
	}

	panes, err := s.Panes(ctx)
	if err != nil {
		b.Fatal(err)
	}

	if want := benchSessions * benchWindowsPerSession * benchPanesPerWindow; len(panes) != want {
		b.Fatalf("fixture panes = %d, want %d", len(panes), want)
	}

	return s, fixture, ctx
}

func populateSession(b *testing.B, ctx context.Context, session tmux.Session) {
	b.Helper()

	for range benchWindowsPerSession - 1 {
		if _, err := session.NewWindow(ctx, tmux.NewWindowOptions{}); err != nil {
			b.Fatal(err)
		}
	}

	panes, err := session.Panes(ctx)
	if err != nil {
		b.Fatal(err)
	}

	for _, pane := range panes {
		for range benchPanesPerWindow - 1 {
			if _, err := pane.Handle().Split(ctx, tmux.SplitOptions{}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func benchControl(b *testing.B, ctx context.Context, s *tmux.Server, session tmux.Session) *tmux.Connection {
	b.Helper()

	conn, err := s.OpenControl(ctx, session.ID(), tmux.ControlOptions{})
	if err != nil {
		b.Fatal(err)
	}

	closeOnCleanup(b, conn)

	return conn
}

func benchLoop(b *testing.B, op func() error) {
	b.Helper()

	if err := op(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if err := op(); err != nil {
			b.Fatal(err)
		}
	}
}

func benchTransports(b *testing.B, ctx context.Context, s *tmux.Server, session tmux.Session, op func(*tmux.Server) error) {
	b.Helper()

	b.Run("subprocess", func(b *testing.B) { benchLoop(b, func() error { return op(s) }) })
	b.Run("control", func(b *testing.B) {
		conn := benchControl(b, ctx, s, session)
		benchLoop(b, func() error { return op(conn.Server()) })
	})
}

func BenchmarkIntegrationPopulatedPanes(b *testing.B) {
	s, session, ctx := populatedServer(b)

	benchTransports(b, ctx, s, session, func(server *tmux.Server) error {
		if _, err := server.Panes(ctx); err != nil {
			return fmt.Errorf("list panes: %w", err)
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedSessionLookup(b *testing.B) {
	s, session, ctx := populatedServer(b)

	benchTransports(b, ctx, s, session, func(server *tmux.Server) error {
		if _, err := server.Session(ctx, session.ID()); err != nil {
			return fmt.Errorf("look up session: %w", err)
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedSnapshot(b *testing.B) {
	s, session, ctx := populatedServer(b)

	benchTransports(b, ctx, s, session, func(server *tmux.Server) error {
		if _, err := server.Snapshot(ctx); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedWindowLinks(b *testing.B) {
	_, session, ctx := populatedServer(b)

	windows, err := session.Windows(ctx)
	if err != nil || len(windows) == 0 {
		b.Fatal("no window", err)
	}

	window := windows[0].Window()

	benchLoop(b, func() error {
		if _, err := window.Links(ctx); err != nil {
			return fmt.Errorf("window links: %w", err)
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedPaneMembership(b *testing.B) {
	s, session, ctx := populatedServer(b)

	panes, err := session.Panes(ctx)
	if err != nil || len(panes) == 0 {
		b.Fatal("no pane", err)
	}

	paneID := panes[len(panes)-1].ID

	benchLoop(b, func() error {
		pane, err := s.Pane(ctx, paneID)
		if err != nil {
			return fmt.Errorf("look up pane: %w", err)
		}

		window, err := pane.Window(ctx)
		if err != nil {
			return fmt.Errorf("pane window: %w", err)
		}

		links, err := window.Links(ctx)
		if err != nil {
			return fmt.Errorf("window links: %w", err)
		}

		for _, link := range links {
			if link.Session().ID() == session.ID() {
				return nil
			}
		}

		return tmux.ErrNotFound
	})
}

func BenchmarkIntegrationPopulatedStartupReads(b *testing.B) {
	s, session, ctx := populatedServer(b)

	benchTransports(b, ctx, s, session, func(server *tmux.Server) error {
		if _, err := server.SessionsWith(ctx, tmux.QueryOptions{ExtraFields: []string{"pane_id", "pane_current_path"}}); err != nil {
			return fmt.Errorf("list sessions: %w", err)
		}

		if _, err := server.PanesWith(ctx, tmux.QueryOptions{ExtraFields: []string{"window_active"}}); err != nil {
			return fmt.Errorf("list panes: %w", err)
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedOpenControl(b *testing.B) {
	s, session, ctx := populatedServer(b)

	benchLoop(b, func() error {
		conn, err := s.OpenControl(ctx, session.ID(), tmux.ControlOptions{})
		if err != nil {
			return fmt.Errorf("open control: %w", err)
		}

		if err := conn.Close(); err != nil {
			return fmt.Errorf("close control: %w", err)
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedPaneMembershipScoped(b *testing.B) {
	_, session, ctx := populatedServer(b)

	panes, err := session.Panes(ctx)
	if err != nil || len(panes) == 0 {
		b.Fatal("no pane", err)
	}

	filter := tmux.QueryOptions{Filter: tmux.Format("#{==:#{pane_id}," + string(panes[len(panes)-1].ID) + "}")}

	benchLoop(b, func() error {
		found, err := session.PanesWith(ctx, filter)
		if err != nil {
			return fmt.Errorf("session panes: %w", err)
		}

		if len(found) != 1 {
			return tmux.ErrNotFound
		}

		return nil
	})
}

func BenchmarkIntegrationPopulatedStartupReadsBatch(b *testing.B) {
	s, session, ctx := populatedServer(b)

	request := tmux.ReadRequest{
		Sessions: tmux.PresentValue(tmux.QueryOptions{ExtraFields: []string{"pane_id", "pane_current_path"}}),
		Panes:    tmux.PresentValue(tmux.QueryOptions{ExtraFields: []string{"window_active"}}),
	}

	benchTransports(b, ctx, s, session, func(server *tmux.Server) error {
		if _, err := server.Read(ctx, request); err != nil {
			return fmt.Errorf("read: %w", err)
		}

		return nil
	})
}
