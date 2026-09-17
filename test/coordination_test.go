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

// TestIntegrationRunShellDelay verifies that RunShell with a valid Delay >= 0 succeeds
// and negative Delay returns ErrInvalidArgument.
func TestIntegrationRunShellDelay(t *testing.T) {
	server, _, ctx := apiFixture(t)

	res, err := server.RunShell(ctx, "echo delay_test", tmux.RunShellOptions{
		Delay: 0.05,
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("expected RunShell with Delay 0.05 to succeed, got err=%v, res=%+v", err, res)
	}

	_, err = server.RunShell(ctx, "echo delay_negative", tmux.RunShellOptions{
		Delay: -1,
	})
	if !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for negative Delay, got: %v", err)
	}
}

// TestIntegrationSourceTextAndStdin verifies that Server.SourceText and raw RunWith with
// "source-file -" correctly evaluate multiline configuration via stdin including braces,
// %if conditionals, escaped literals, and options like ParseOnly and Verbose.
func TestIntegrationSourceTextAndStdin(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// 1. SourceText with conditionals (%if / %else / %endif), braces, and escaped literals
	config := `%if 1
set-option -t ` + string(session.ID()) + ` @cond_branch "branch_taken"
%else
set-option -t ` + string(session.ID()) + ` @cond_branch "not_taken"
%endif

# Escaped literals and semicolon in string
set-option -t ` + string(session.ID()) + ` @escaped_literal "literal;with#{special}"
`
	res, err := server.SourceText(ctx, config, tmux.SourceOptions{})
	if err != nil {
		t.Fatalf("SourceText failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", res.ExitCode)
	}

	assertUserOption(t, ctx, session, "@cond_branch", "branch_taken")
	assertUserOption(t, ctx, session, "@escaped_literal", "literal;with#{special}")

	// 2. SourceText with ParseOnly = true (syntax validation without execution)
	parseOnlyConfig := `set-option -t ` + string(session.ID()) + ` @parse_only "should_not_be_set"`
	if _, err := server.SourceText(ctx, parseOnlyConfig, tmux.SourceOptions{ParseOnly: true}); err != nil {
		t.Fatalf("SourceText with ParseOnly failed: %v", err)
	}
	val, err := session.Options().User(ctx, "@parse_only")
	if err != nil || val.Local.State() != tmux.Unavailable {
		t.Fatalf("ParseOnly option was unexpectedly set: %+v, %v", val, err)
	}

	// 3. SourceText with Verbose = true
	verboseConfig := `set-option -t ` + string(session.ID()) + ` @verbose_opt "is_set"`
	verboseRes, err := server.SourceText(ctx, verboseConfig, tmux.SourceOptions{Verbose: true})
	if err != nil {
		t.Fatalf("SourceText with Verbose failed: %v", err)
	}
	assertUserOption(t, ctx, session, "@verbose_opt", "is_set")
	if len(verboseRes.Stdout) == 0 && len(verboseRes.Stderr) == 0 {
		t.Fatalf("expected verbose output, got stdout=%q stderr=%q", verboseRes.Stdout, verboseRes.Stderr)
	}

	// 4. Raw RunWith executing source-file - with RunOptions.Input
	rawCmd, err := tmux.NewCommand("source-file", "-")
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}
	rawConfig := []byte("set-option -t " + string(session.ID()) + " @raw_source \"from_raw_runwith\"\n")
	if _, err := server.RunWith(ctx, rawCmd, tmux.RunOptions{Input: rawConfig}); err != nil {
		t.Fatalf("RunWith source-file - failed: %v", err)
	}
	assertUserOption(t, ctx, session, "@raw_source", "from_raw_runwith")
}

func TestIntegrationCoordinationShellAndLock(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// 1. RunShell with Delay and Background
	shellScript := "sleep 0.05 && tmux set-option -t " + string(session.ID()) + " @shell_ran \"yes\""
	res, err := server.RunShell(ctx, shellScript, tmux.RunShellOptions{
		Background:       false,
		Delay:            0.05,
		Cancel:           false,
		ClearEnvironment: false,
		Dir:              "",
		Target:           "",
	})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("RunShell with Delay failed: %v, res: %+v", err, res)
	}
	assertUserOption(t, ctx, session, "@shell_ran", "yes")

	// 2. Server.IfShell: true branch
	cmdTrue := testCommand(t, "set-option", "-t", string(session.ID()), "@if_shell", "true_branch")
	cmdFalse := testCommand(t, "set-option", "-t", string(session.ID()), "@if_shell", "false_branch")
	if err := server.IfShell(ctx, "true", testSequence(t, cmdTrue), testSequence(t, cmdFalse)); err != nil {
		t.Fatalf("IfShell true branch failed: %v", err)
	}
	assertUserOption(t, ctx, session, "@if_shell", "true_branch")

	// Server.IfShell: false branch
	if err := server.IfShell(ctx, "false", testSequence(t, cmdTrue), testSequence(t, cmdFalse)); err != nil {
		t.Fatalf("IfShell false branch failed: %v", err)
	}
	assertUserOption(t, ctx, session, "@if_shell", "false_branch")
	// 3. Server.LockScreen executes lock-server cleanly
	if err := server.LockScreen(ctx); err != nil {
		t.Fatalf("Server.LockScreen failed: %v", err)
	}
}
