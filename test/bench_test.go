//go:build integration && go1.24

package tmux_test

import (
	"context"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := s.FindSession(ctx, "fixture")
	if err != nil {
		b.Fatal(err)
	}
	conn, err := s.OpenControl(ctx, session, tmux.ControlOptions{})
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
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
