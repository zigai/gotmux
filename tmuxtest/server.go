// Package tmuxtest provides private, real-tmux fixtures for library consumers.
// Each fixture owns one short absolute socket path and an explicit empty config.
// The core package never imports this package.
package tmuxtest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

const (
	cleanupTimeout = 5 * time.Second
	startupTimeout = 10 * time.Second
	defaultWidth   = 100
	defaultHeight  = 30
)

// NewServer starts an isolated, real tmux daemon fixture with an initial session named "fixture".
//
// Test isolation & environment:
// Uses a dedicated temporary directory with a short absolute path (avoiding UNIX domain socket
// path length limits, typically 104-108 bytes). Creates an empty tmux.conf to prevent loading
// user configuration, and sanitizes HOME, SHELL, TERM, and LC_ALL.
//
// Cleanup ordering (LIFO):
// Server cleanup is registered immediately via [testing.TB.Cleanup]. If the test creates any
// control connections, their cleanup MUST be registered AFTER calling NewServer so that testing's
// LIFO cleanup order gracefully closes control clients before killing the underlying daemon.
//
// Environment variables:
//   - TMUX_TEST_BINARY: path or name of the tmux executable to test (defaults to "tmux").
//   - TMUX_INTEGRATION_REQUIRED: if "1", missing tmux executable fails the test instead of skipping.
//
// Startup, compatibility, and cleanup failures are always treated as hard failures, never skipped.
func NewServer(tb testing.TB) *tmux.Server {
	tb.Helper()

	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary = "tmux"
	}

	binary, err := exec.LookPath(binary)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) && os.Getenv("TMUX_INTEGRATION_REQUIRED") != "1" {
			tb.Skip("tmux executable missing; set TMUX_INTEGRATION_REQUIRED=1 in integration CI")
		}

		tb.Fatalf("tmux fixture executable: %v", err)
	}

	dir := fixtureDirectory(tb)

	var (
		server   *tmux.Server
		identity tmux.Value[tmux.ServerIdentity]
	)

	tb.Cleanup(func() {
		cleanupDaemon(tb, server, identity, dir)
	})

	config := filepath.Join(dir, "tmux.conf")
	if err = os.WriteFile(config, nil, 0o600); err != nil {
		tb.Fatal(err)
	}

	locale := "C.UTF-8"
	if runtime.GOOS == "darwin" {
		locale = "en_US.UTF-8"
	}

	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "SHELL=/bin/sh", "TERM=xterm-256color", "LC_ALL=" + locale}

	server, err = tmux.New(tmux.Config{
		Binary:     binary,
		SocketPath: filepath.Join(dir, "s"),
		SocketName: "",
		ConfigFile: config,
		Env:        env,
		Dir:        dir,
		Limits:     tmux.Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
	})
	if err != nil {
		tb.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	session, err := server.NewSession(ctx, tmux.NewSessionOptions{
		Name:    "fixture",
		Dir:     "",
		Window:  "",
		Program: tmux.Exec("/bin/sh"),
		Env:     nil,
		Size:    tmux.Size{Width: defaultWidth, Height: defaultHeight},
		Start:   tmux.AllowStart,
		Group:   "",
	})
	if session.Valid() {
		identity = tmux.PresentValue(session.Identity())
	}

	if err != nil {
		tb.Fatalf("start isolated tmux at %s: %v", dir, err)
	}

	if !session.Valid() {
		tb.Fatal("startup returned a handle without provenance")
	}

	return server
}

func cleanupDaemon(tb testing.TB, server *tmux.Server, identity tmux.Value[tmux.ServerIdentity], dir string) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	if server == nil {
		return
	}

	id, known := identity.Get()
	if !known {
		info, e := server.Probe(ctx)
		if e == nil {
			id = info.Identity
			known = true
		} else if !errors.Is(e, tmux.ErrNoServer) {
			tb.Errorf("fixture cleanup could not verify daemon at %s: %v", dir, e)
		}
	}

	if known {
		err := server.KillIfIdentity(ctx, id)
		if err != nil && !errors.Is(err, tmux.ErrNoServer) {
			tb.Errorf("fixture cleanup failed at %s: %v", dir, err)
		}
	}
}

func fixtureDirectory(tb testing.TB) string {
	tb.Helper()
	// A caller's TMPDIR and test name can exceed the Unix socket path limit.
	dir, err := os.MkdirTemp("/tmp", "tg-") //nolint:usetesting // Unix sockets require a short path independent of TMPDIR; Cleanup removes it; tested on the Go 1.27 baseline.
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			tb.Errorf("remove fixture directory %s: %v", dir, err)
		}
	})

	return dir
}
