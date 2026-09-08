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

	tmux "example.com/tmux"
)

// NewServer starts one isolated daemon with an initial session named fixture.
// Cleanup is registered immediately. Register control-client cleanup AFTER this
// call so testing's LIFO cleanup order closes clients before killing the daemon.
//
// A missing executable skips local tests; TMUX_INTEGRATION_REQUIRED=1 makes it a
// hard failure. TMUX_TEST_BINARY selects a particular release's executable.
// Startup, compatibility, and cleanup failures are never converted to skips.
func NewServer(t testing.TB) *tmux.Server {
	t.Helper()
	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary = "tmux"
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) && os.Getenv("TMUX_INTEGRATION_REQUIRED") != "1" {
			t.Skip("tmux executable missing; set TMUX_INTEGRATION_REQUIRED=1 in integration CI")
		}
		t.Fatalf("tmux fixture executable: %v", err)
	}
	dir, err := os.MkdirTemp("/tmp", "tg-")
	if err != nil {
		t.Fatal(err)
	}
	var server *tmux.Server
	var identity tmux.Value[tmux.ServerIdentity]
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		retain := false
		if server != nil {
			id, known := identity.Get()
			if !known {
				// Startup may have created the private daemon before returning a decode
				// error. Observe it, then use a guarded kill; never signal a recorded PID.
				info, e := server.Probe(ctx)
				if e == nil {
					id = info.Identity
					known = true
				} else if !errors.Is(e, tmux.ErrNoServer) {
					t.Errorf("fixture cleanup could not verify daemon; retaining %s: %v", dir, e)
					retain = true
				}
			}
			if known {
				err := server.KillIfIdentity(ctx, id)
				if err != nil && !errors.Is(err, tmux.ErrNoServer) {
					t.Errorf("fixture cleanup failed; retaining %s: %v", dir, err)
					retain = true
				}
			}
		}
		if !retain {
			if e := os.RemoveAll(dir); e != nil {
				t.Errorf("remove fixture %s: %v", dir, e)
			}
		}
	})
	config := filepath.Join(dir, "tmux.conf")
	if err = os.WriteFile(config, nil, 0600); err != nil {
		t.Fatal(err)
	}
	locale := "C.UTF-8"
	if runtime.GOOS == "darwin" {
		locale = "en_US.UTF-8"
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "SHELL=/bin/sh", "TERM=xterm-256color", "LC_ALL=" + locale}
	server, err = tmux.New(tmux.Config{Binary: binary, SocketPath: filepath.Join(dir, "s"), ConfigFile: config, Env: env, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "fixture", Program: tmux.Exec("/bin/sh"), Size: tmux.Size{Width: 100, Height: 30}})
	if session.Valid() {
		identity = tmux.PresentValue(session.Identity())
	}
	if err != nil {
		t.Fatalf("start isolated tmux at %s: %v", dir, err)
	}
	if !session.Valid() {
		t.Fatal("startup returned a handle without provenance")
	}
	return server
}
