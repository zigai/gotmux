//go:build integration

package test

import (
	"context"
	"slices"
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func firstWindowPane(
	t *testing.T,
	ctx context.Context,
	session tmux.Session,
) (tmux.Window, tmux.Pane) {
	t.Helper()

	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatalf("session.Windows = %d links, %v", len(links), err)
	}

	window := links[0].Window()

	pane, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	return window, pane
}

func assertFormatValues(t *testing.T, call string, got [][]byte, err error, want ...string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", call, err)
	}

	values := make([]string, len(got))
	for i, value := range got {
		values[i] = string(value)
	}

	if !slices.Equal(values, want) {
		t.Fatalf("%s = %q, want %q", call, values, want)
	}
}
