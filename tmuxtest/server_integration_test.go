//go:build integration

package tmuxtest_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func TestLongNameAndCleanup(t *testing.T) {
	longRoot := filepath.Join(t.TempDir(), strings.Repeat("long-temp-root-", 10))
	if err := os.Mkdir(longRoot, 0o750); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TMPDIR", longRoot)

	var socket string

	t.Run(strings.Repeat("long-fixture-name-", 10), func(t *testing.T) {
		server := tmuxtest.NewServer(t)

		socket = server.Endpoint().SocketPath
		if !filepath.IsAbs(socket) || len(socket) >= 100 {
			t.Fatalf("fixture socket is not short and absolute: %s", socket)
		}

		if _, err := server.Probe(t.Context()); err != nil {
			t.Fatal(err)
		}
	})

	if socket == "" {
		return // The subtest already failed or skipped during fixture startup.
	}

	if _, err := os.Stat(filepath.Dir(socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture directory survived cleanup: %v", err)
	}
}

func TestCleanupWaitsForPaneProgramsToExit(t *testing.T) {
	var pid int

	t.Run("fixture", func(t *testing.T) {
		server := tmuxtest.NewServer(t)

		session, err := server.NewSession(t.Context(), tmux.NewSessionOptions{
			Name: "slow-exit",
			Program: tmux.Exec(
				"/bin/sh",
				"-c",
				`trap 'sleep 0.5; exit 0' HUP; while :; do sleep 0.05; done`,
			),
		})
		if err != nil {
			t.Fatal(err)
		}

		panes, err := session.Panes(t.Context())
		if err != nil || len(panes) != 1 {
			t.Fatalf("panes = %v, %v", panes, err)
		}

		pid = panes[0].PID
	})

	if pid == 0 {
		return
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("pane process %d outlived fixture cleanup: %v", pid, err)
	}
}
