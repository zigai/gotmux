//go:build integration

package tmux_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationCoordinationWait(t *testing.T) {
	server, _, ctx := apiFixture(t)
	if err := server.WaitFor(context.Background(), "channel"); !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("deadline-free wait: %v", err)
	}
	// tmux retains a signal sent before a waiter, eliminating a timing race.
	if err := server.Signal(ctx, "channel"); err != nil {
		t.Fatal(err)
	}

	if err := server.WaitFor(ctx, "channel"); err != nil {
		t.Fatal(err)
	}

	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	if err := server.WaitFor(short, "unsignaled"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unsignaled wait: %v", err)
	}
	// Release any server-side waiter left by cancellation, then prove readmission.
	if err := server.Signal(ctx, "unsignaled"); err != nil {
		t.Fatal(err)
	}

	if _, err := server.Panes(ctx); err != nil {
		t.Fatalf("readmission: %v", err)
	}
}

func TestIntegrationCoordinationLock(t *testing.T) {
	server, _, ctx := apiFixture(t)
	if err := server.Lock(context.Background(), "lock"); !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("deadline-free lock: %v", err)
	}
	if err := server.Lock(ctx, "lock"); err != nil {
		t.Fatal(err)
	}

	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	if err := server.Lock(short, "lock"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock acquired: %v", err)
	}
	if err := server.Unlock(ctx, "lock"); err != nil {
		t.Fatal(err)
	}
	// A separate channel avoids inferring retraction of a canceled server waiter.
	if err := server.Lock(ctx, "fresh"); err != nil {
		t.Fatal(err)
	}
	if err := server.Unlock(ctx, "fresh"); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationSourceParseOnly(t *testing.T) {
	server, session, ctx := apiFixture(t)
	path := filepath.Join(t.TempDir(), "literal config.conf")

	data := []byte("set-option -t " + string(session.ID()) + " @source-effect applied\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	var options tmux.SourceOptions

	options.ParseOnly = true
	if _, err := server.SourceFile(ctx, path, options); err != nil {
		t.Fatal(err)
	}

	value, err := session.Options().User(ctx, "@source-effect")
	if err != nil || value.Local.State() != tmux.Unavailable {
		t.Fatalf("parse-only had effects: %+v, %v", value, err)
	}

	options.ParseOnly = false
	if _, err := server.SourceFile(ctx, path, options); err != nil {
		t.Fatal(err)
	}

	assertUserOption(t, ctx, session, "@source-effect", "applied")

	if err := os.WriteFile(path, []byte("not-a-tmux-command\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	options.ParseOnly = true
	if _, err := server.SourceFile(ctx, path, options); err == nil {
		t.Fatal("invalid source accepted")
	}

	assertUserOption(t, ctx, session, "@source-effect", "applied")
}

func TestIntegrationArrayInterruption(t *testing.T) {
	server, session, ctx := apiFixture(t)

	var create tmux.NewSessionOptions

	create.Name = "survivor"

	survivor, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	sequence := testSequence(
		t,
		testCommand(t, "set-hook", "-u", "-t", string(session.ID()), "after-set-option[0]"),
		testCommand(t, "set-option", "-t", string(survivor.ID()), "@first-step", "confirmed"),
		testCommand(t, "kill-session", "-t", string(session.ID())),
	)
	if err := session.Hooks().Set(ctx, "after-set-option", 0, sequence); err != nil {
		t.Fatal(err)
	}

	updates := []tmux.ArrayUpdate{{Index: 2, Value: "first", Unset: false}, {Index: 7, Value: "second", Unset: false}, {Index: 8, Value: "third", Unset: false}}

	result, err := session.Options().UpdateArray(ctx, "update-environment", updates)
	if err == nil {
		t.Fatal("batch succeeded after target was removed")
	}

	if len(result.Applied) != 1 || result.Applied[0] != 0 {
		t.Fatalf("confirmed prefix: %+v, %v", result, err)
	}

	failure, ok := errors.AsType[*tmux.OperationError](err)
	if !ok || len(failure.Outcome.Steps) != len(updates) {
		t.Fatalf("missing steps: %v", err)
	}

	steps := failure.Outcome.Steps
	if steps[0].Effect != tmux.Confirmed || steps[1].Effect == tmux.Confirmed || steps[2].Effect != tmux.NotSent {
		t.Fatalf("partial effects: %+v", steps)
	}

	assertUserOption(t, ctx, survivor, "@first-step", "confirmed")

	if _, err := session.Info(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("target survived hook: %v", err)
	}
}
