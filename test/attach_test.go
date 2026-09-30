//go:build integration && (linux || darwin)

package tmux_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"golang.org/x/term"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func TestIntegrationAPIAttach(t *testing.T) {
	for _, finish := range []string{"detach", "cancel", "connection-close"} {
		t.Run(finish, func(t *testing.T) { testAttachmentLifecycle(t, finish) })
	}
}

func attachmentSession(t *testing.T, server *tmux.Server, session tmux.Session, ctx context.Context, finish string) (tmux.Session, *tmux.Connection) {
	t.Helper()

	if finish != "connection-close" {
		return session, nil
	}

	connection := apiControl(t, server, session, ctx)

	bound, err := connection.Server().Session(ctx, session.ID())
	if err != nil {
		t.Fatal(err)
	}

	subprocess, err := bound.ViaSubprocess()
	if err != nil {
		t.Fatal(err)
	}

	return subprocess, connection
}

func waitTerminalClient(t *testing.T, ctx context.Context, server *tmux.Server, terminal *os.File, done chan error) tmux.Client {
	t.Helper()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			done <- err

			t.Fatalf("attachment exited before connecting: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			clients, err := server.Clients(ctx)
			if err != nil {
				t.Fatal(err)
			}

			for _, client := range clients {
				if string(client.Name) == terminal.Name() {
					return client.Handle()
				}
			}
		}
	}
}

func finishAttachment(t *testing.T, ctx context.Context, finish string, client tmux.Client, connection *tmux.Connection, cancel context.CancelFunc) {
	t.Helper()

	switch finish {
	case "cancel":
		cancel()
	case "connection-close":
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		if err := client.Detach(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func testAttachmentLifecycle(t *testing.T, finish string) {
	t.Helper()
	server, session, ctx := apiFixture(t)
	session, connection := attachmentSession(t, server, session, ctx, finish)
	terminal := attachmentTerminal(t)

	initial, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	attachCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		var options tmux.AttachOptions
		done <- session.Attach(attachCtx, tmux.Streams{In: terminal, Out: terminal, Err: terminal}, options)
	}()

	t.Cleanup(func() {
		cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("attachment failed to stop")
		}
	})
	attached := waitTerminalClient(t, ctx, server, terminal, done)
	finishAttachment(t, ctx, finish, attached, connection, cancel)

	select {
	case err := <-done:
		done <- err

		if finish == "detach" && err != nil {
			t.Fatal(err)
		}

		if finish != "detach" && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	restored, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	if *initial != *restored {
		t.Fatal("terminal state not restored")
	}
}

func TestIntegrationAPIAttachRejectsReplacementDaemon(t *testing.T) {
	server := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	stale, err := server.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	socket := server.Endpoint().SocketPath
	// Keep the original daemon reachable for fixture cleanup while starting a
	// replacement at the same endpoint with the same numeric session ID.
	saved := socket + ".original"
	if err := os.Rename(socket, saved); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := os.Rename(saved, socket); err != nil {
			t.Error(err)
		}
	})

	var create tmux.NewSessionOptions

	create.Name = "replacement"

	replacement, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.KillMatching(cleanup, replacement.ServerIdentity()); err != nil {
			t.Error(err)
		}
	})

	if replacement.ID() != stale.ID() {
		t.Fatalf("expected ID reuse: %v, %v", replacement.ID(), stale.ID())
	}

	terminal := attachmentTerminal(t)

	var options tmux.AttachOptions

	err = stale.Attach(ctx, tmux.Streams{In: terminal, Out: terminal, Err: terminal}, options)
	if !errors.Is(err, tmux.ErrServerChanged) {
		t.Fatalf("stale attach: %v", err)
	}

	clients, err := server.Clients(ctx)
	if err != nil || len(clients) != 0 {
		t.Fatalf("stale attach reached replacement: %+v, %v", clients, err)
	}
}

func TestIntegrationAttachExtendedFlags(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// Trap SIGHUP for the parent process so DetachParentSignal (-x) does not terminate the test runner
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)
	t.Cleanup(func() {
		signal.Stop(sigChan)
		signal.Reset(syscall.SIGHUP)
	})

	// 1. Attach with working directory (-c) and client flags (-f)
	dir := t.TempDir()
	client1, done1 := attachWith(t, ctx, server, session, attachmentTerminal(t), tmux.AttachOptions{
		PreserveEnvironment: true,
		Detach:              tmux.DetachNone,
		Dir:                 dir,
		Flags:               []tmux.ClientFlag{tmux.ClientFlagIgnoreSize, tmux.ClientFlagReadOnly},
	})

	assertReadOnly(t, ctx, client1, true)

	// Verify session path was updated by -c
	pathBytes, err := session.Format(ctx, tmux.Format("#{session_path}"))
	if err != nil {
		t.Fatalf("session.Format failed: %v", err)
	}

	if string(pathBytes) != dir {
		t.Errorf("expected session path %q, got %q", dir, string(pathBytes))
	}

	// 2. Client 2 with DetachOtherClients (-d) detaches client 1.
	client2, done2 := attachWith(t, ctx, server, session, attachmentTerminal(t), tmux.AttachOptions{Detach: tmux.DetachOtherClients})
	if err := awaitReturn(t, done1, "client 1 after DetachOtherClients"); !endedByDetach(err) {
		t.Fatalf("client 1 unexpected error on detach: %v", err)
	}

	detachAndAwait(t, ctx, client2, done2, "client 2")

	// 3. Client 3 read-only through AttachOptions.ReadOnly (without Flags)
	client3, done3 := attachWith(t, ctx, server, session, attachmentTerminal(t), tmux.AttachOptions{ReadOnly: true, Detach: tmux.DetachNone})
	assertReadOnly(t, ctx, client3, true)

	// 4. Client 4 with DetachParentSignal (-x) detaches client 3.
	client4, done4 := attachWith(t, ctx, server, session, attachmentTerminal(t), tmux.AttachOptions{Detach: tmux.DetachParentSignal})
	if err := awaitReturn(t, done3, "client 3 after DetachParentSignal"); !endedByDetach(err) {
		t.Fatalf("client 3 unexpected error on detach by signal: %v", err)
	}

	detachAndAwait(t, ctx, client4, done4, "client 4")
}

// awaitReturn puts the result back for cleanups that also wait on done.
func awaitReturn(t *testing.T, done chan error, call string) error {
	t.Helper()

	select {
	case err := <-done:
		done <- err

		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return", call)

		return nil
	}
}

func endedByDetach(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}

func detachAndAwait(t *testing.T, ctx context.Context, client tmux.Client, done chan error, name string) {
	t.Helper()

	if err := client.Detach(ctx); err != nil {
		t.Fatalf("%s detach failed: %v", name, err)
	}

	if err := awaitReturn(t, done, name+" after Detach"); !endedByDetach(err) {
		t.Fatalf("%s unexpected error on detach: %v", name, err)
	}
}

func startPrepared(t *testing.T, cmd *exec.Cmd) chan error {
	t.Helper()

	if err := cmd.Start(); err != nil {
		t.Fatalf("start prepared command: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("prepared command failed to stop")
		}
	})

	return done
}

func detachAndAwaitExit(t *testing.T, ctx context.Context, client tmux.Client, done chan error, name string) {
	t.Helper()

	if err := client.Detach(ctx); err != nil {
		t.Fatalf("%s detach failed: %v", name, err)
	}

	if err := awaitReturn(t, done, name+" process after Detach"); err != nil {
		t.Fatalf("%s process exited with %v after detach", name, err)
	}
}

func TestIntegrationPrepareTerminalAttachedSession(t *testing.T) {
	server, _, ctx := apiFixture(t)
	sessName := "attached_terminal_sess"

	cmd, err := tmux.NewCommand("new-session", "-A", "-s", sessName)
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}

	term := attachmentTerminal(t)

	termCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	execCmd, err := server.PrepareTerminal(termCtx, cmd, tmux.Streams{In: term, Out: term, Err: term}, tmux.TerminalOptions{Start: tmux.StartPolicyAllowStart})
	if err != nil {
		t.Fatalf("PrepareTerminal failed: %v", err)
	}

	done := startPrepared(t, execCmd)
	client := waitTerminalClient(t, ctx, server, term, done)

	sess, err := server.FindSession(ctx, sessName)
	if err != nil {
		t.Fatalf("FindSession failed: %v", err)
	}

	if sid := clientSession(t, ctx, client); sid != sess.ID() {
		t.Errorf("expected session ID %v, got %v", sess.ID(), sid)
	}

	detachAndAwaitExit(t, ctx, client, done, "client")
}

func TestIntegrationPrepareDefaultTerminal(t *testing.T) {
	server, _, ctx := apiFixture(t)

	term := attachmentTerminal(t)

	termCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	execCmd, err := server.PrepareDefaultTerminal(termCtx, tmux.Streams{In: term, Out: term, Err: term}, tmux.TerminalOptions{Start: tmux.StartPolicyAllowStart})
	if err != nil {
		t.Fatalf("PrepareDefaultTerminal failed: %v", err)
	}

	if err := execCmd.Start(); err != nil {
		t.Fatalf("execCmd.Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- execCmd.Wait()
	}()

	t.Cleanup(func() {
		cancel()

		_ = execCmd.Process.Kill()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("process failed to stop")
		}
	})

	client := waitTerminalClient(t, ctx, server, term, done)
	if !client.Valid() {
		t.Fatal("expected valid client from default terminal")
	}

	if err := client.Detach(ctx); err != nil {
		t.Fatalf("client.Detach failed: %v", err)
	}

	select {
	case err := <-done:
		done <- err

		if err != nil {
			t.Fatalf("execCmd.Wait error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after detach")
	}
}

func TestIntegrationPrepareTerminalSequence(t *testing.T) {
	server, _, ctx := apiFixture(t)

	sessName := "term_seq_sess"

	cmd1, err := tmux.NewCommand("new-session", "-d", "-s", sessName)
	if err != nil {
		t.Fatal(err)
	}

	cmd2, err := tmux.NewCommand("attach-session", "-t", sessName)
	if err != nil {
		t.Fatal(err)
	}

	seq, err := tmux.Sequence(cmd1, cmd2)
	if err != nil {
		t.Fatal(err)
	}

	term := attachmentTerminal(t)

	termCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	execCmd, err := server.PrepareTerminalSequence(termCtx, seq, tmux.Streams{In: term, Out: term, Err: term}, tmux.TerminalOptions{Start: tmux.StartPolicyAllowStart})
	if err != nil {
		t.Fatalf("PrepareTerminalSequence failed: %v", err)
	}

	if err := execCmd.Start(); err != nil {
		t.Fatalf("execCmd.Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- execCmd.Wait()
	}()

	t.Cleanup(func() {
		cancel()

		_ = execCmd.Process.Kill()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	client := waitTerminalClient(t, ctx, server, term, done)
	if err := client.Detach(ctx); err != nil {
		t.Fatalf("client.Detach failed: %v", err)
	}

	select {
	case err := <-done:
		done <- err

		if err != nil {
			t.Fatalf("execCmd.Wait error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after detach")
	}
}

func TestIntegrationSessionPrepareAttach(t *testing.T) {
	server, session, ctx := apiFixture(t)

	term := attachmentTerminal(t)

	termCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	execCmd, err := session.PrepareAttach(termCtx, tmux.Streams{In: term, Out: term, Err: term}, tmux.AttachOptions{
		ReadOnly:            false,
		PreserveEnvironment: false,
		Detach:              tmux.DetachNone,
		Dir:                 "",
		Flags:               nil,
	})
	if err != nil {
		t.Fatalf("session.PrepareAttach failed: %v", err)
	}

	if err := execCmd.Start(); err != nil {
		t.Fatalf("execCmd.Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- execCmd.Wait()
	}()

	t.Cleanup(func() {
		cancel()

		_ = execCmd.Process.Kill()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	client := waitTerminalClient(t, ctx, server, term, done)
	if err := client.Detach(ctx); err != nil {
		t.Fatalf("client.Detach failed: %v", err)
	}

	select {
	case err := <-done:
		done <- err

		if err != nil {
			t.Fatalf("execCmd.Wait error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after detach")
	}
}

func TestIntegrationServerPrepareAttach(t *testing.T) {
	server, session, ctx := apiFixture(t)

	var options tmux.AttachOptions

	// 1. server.PrepareAttach with valid session ID
	term1 := attachmentTerminal(t)

	termCtx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()

	execCmd1, err := server.PrepareAttach(termCtx1, session.ID(), tmux.Streams{In: term1, Out: term1, Err: term1}, options)
	if err != nil {
		t.Fatalf("server.PrepareAttach failed: %v", err)
	}

	done1 := startPrepared(t, execCmd1)
	detachAndAwaitExit(t, ctx, waitTerminalClient(t, ctx, server, term1, done1), done1, "client 1")

	// 2. server.PrepareAttachTarget with empty target (attaching to default session)
	term2 := attachmentTerminal(t)

	termCtx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()

	execCmd2, err := server.PrepareAttachTarget(termCtx2, "", tmux.Streams{In: term2, Out: term2, Err: term2}, options)
	if err != nil {
		t.Fatalf("server.PrepareAttachTarget with empty target failed: %v", err)
	}

	done2 := startPrepared(t, execCmd2)
	detachAndAwaitExit(t, ctx, waitTerminalClient(t, ctx, server, term2, done2), done2, "client 2")
}

func TestIntegrationAttach_MasterHangupTeardown(t *testing.T) {
	server, session, ctx := apiFixture(t)

	master, slave := openPTY(t)

	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- session.Attach(attachCtx, tmux.Streams{In: slave, Out: slave, Err: slave}, tmux.AttachOptions{})
	}()

	client := waitTerminalClient(t, ctx, server, slave, done)
	if !client.Valid() {
		t.Fatal("expected valid client attached")
	}

	// Abruptly close master PTY to trigger kernel hangup (SIGHUP)
	if err := master.Close(); err != nil {
		t.Fatalf("master.Close failed: %v", err)
	}

	// Verify Attach unblocks and finishes within timeout
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session.Attach did not unblock within 5 seconds of master PTY hangup")
	}

	// Verify the client terminal is cleaned up on the server
	awaitObservation(t, ctx, "client terminal disconnected after hangup", func() bool {
		clients, err := server.Clients(ctx)
		if err != nil {
			return false
		}

		for _, c := range clients {
			if c.Name == client.Name() {
				return false
			}
		}

		return true
	})
}
