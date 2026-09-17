//go:build integration && (linux || darwin)

package tmux_test

import (
	"context"
	"errors"
	"os"
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

	auxiliary, err := bound.UsingSubprocess()
	if err != nil {
		t.Fatal(err)
	}

	return auxiliary, connection
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
		done <- session.Attach(attachCtx, tmux.TerminalStreams{In: terminal, Out: terminal, Err: terminal}, options)
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

		if err := server.KillIfIdentity(cleanup, replacement.Identity()); err != nil {
			t.Error(err)
		}
	})

	if replacement.ID() != stale.ID() {
		t.Fatalf("expected ID reuse: %v, %v", replacement.ID(), stale.ID())
	}

	terminal := attachmentTerminal(t)

	var options tmux.AttachOptions

	err = stale.Attach(ctx, tmux.TerminalStreams{In: terminal, Out: terminal, Err: terminal}, options)
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
	term1 := attachmentTerminal(t)
	attachCtx1, cancel1 := context.WithCancel(ctx)
	done1 := make(chan error, 1)

	go func() {
		opts := tmux.AttachOptions{
			ReadOnly:            false,
			PreserveEnvironment: true,
			Detach:              tmux.DetachNone,
			Dir:                 dir,
			Flags:               []tmux.ClientFlag{tmux.ClientFlagIgnoreSize, tmux.ClientFlagReadOnly},
		}
		done1 <- session.Attach(attachCtx1, tmux.TerminalStreams{In: term1, Out: term1, Err: term1}, opts)
	}()

	t.Cleanup(func() {
		cancel1()
		select {
		case <-done1:
		case <-time.After(5 * time.Second):
			t.Error("client 1 failed to stop")
		}
	})

	client1 := waitTerminalClient(t, ctx, server, term1, done1)
	info1, err := client1.Info(ctx)
	if err != nil {
		t.Fatalf("client1.Info failed: %v", err)
	}
	if !info1.ReadOnly {
		t.Errorf("expected client1 to be read-only from Flags, got false")
	}

	// Verify session path was updated by -c
	pathBytes, err := session.Format(ctx, tmux.Format("#{session_path}"))
	if err != nil {
		t.Fatalf("session.Format failed: %v", err)
	}
	if string(pathBytes) != dir {
		t.Errorf("expected session path %q, got %q", dir, string(pathBytes))
	}

	// 2. Attach client 2 with Detach: DetachOtherClients (-d)
	term2 := attachmentTerminal(t)
	attachCtx2, cancel2 := context.WithCancel(ctx)
	done2 := make(chan error, 1)

	go func() {
		opts := tmux.AttachOptions{
			ReadOnly:            false,
			PreserveEnvironment: false,
			Detach:              tmux.DetachOtherClients,
			Dir:                 "",
			Flags:               nil,
		}
		done2 <- session.Attach(attachCtx2, tmux.TerminalStreams{In: term2, Out: term2, Err: term2}, opts)
	}()

	t.Cleanup(func() {
		cancel2()
		select {
		case <-done2:
		case <-time.After(5 * time.Second):
			t.Error("client 2 failed to stop")
		}
	})

	client2 := waitTerminalClient(t, ctx, server, term2, done2)

	// Client 1 should now be detached and done1 should finish
	select {
	case err := <-done1:
		done1 <- err
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("client 1 unexpected error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client 1 was not detached by client 2 with DetachOtherClients")
	}

	// Detach client 2 cleanly
	if err := client2.Detach(ctx); err != nil {
		t.Fatalf("client 2 detach failed: %v", err)
	}

	select {
	case err := <-done2:
		done2 <- err
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("client 2 unexpected error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client 2 failed to detach")
	}

	// 3. Attach client 3 with ReadOnly: true directly (without Flags)
	term3 := attachmentTerminal(t)
	attachCtx3, cancel3 := context.WithCancel(ctx)
	done3 := make(chan error, 1)

	go func() {
		opts := tmux.AttachOptions{
			ReadOnly:            true,
			PreserveEnvironment: false,
			Detach:              tmux.DetachNone,
			Dir:                 "",
			Flags:               nil,
		}
		done3 <- session.Attach(attachCtx3, tmux.TerminalStreams{In: term3, Out: term3, Err: term3}, opts)
	}()

	t.Cleanup(func() {
		cancel3()
		select {
		case <-done3:
		case <-time.After(5 * time.Second):
		}
	})

	client3 := waitTerminalClient(t, ctx, server, term3, done3)
	info3, err := client3.Info(ctx)
	if err != nil {
		t.Fatalf("client3.Info failed: %v", err)
	}
	if !info3.ReadOnly {
		t.Errorf("expected client3 to be read-only from AttachOptions.ReadOnly, got false")
	}

	// 4. Attach client 4 with Detach: DetachParentSignal (-x)
	term4 := attachmentTerminal(t)
	attachCtx4, cancel4 := context.WithCancel(ctx)
	done4 := make(chan error, 1)

	go func() {
		opts := tmux.AttachOptions{
			ReadOnly:            false,
			PreserveEnvironment: false,
			Detach:              tmux.DetachParentSignal,
			Dir:                 "",
			Flags:               nil,
		}
		done4 <- session.Attach(attachCtx4, tmux.TerminalStreams{In: term4, Out: term4, Err: term4}, opts)
	}()

	t.Cleanup(func() {
		cancel4()
		select {
		case <-done4:
		case <-time.After(5 * time.Second):
		}
	})

	client4 := waitTerminalClient(t, ctx, server, term4, done4)

	// Client 3 should now be detached by client 4's DetachParentSignal
	select {
	case err := <-done3:
		done3 <- err
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("client 3 unexpected error on detach by signal: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client 3 was not detached by client 4 with DetachParentSignal")
	}

	if err := client4.Detach(ctx); err != nil {
		t.Fatalf("client 4 detach failed: %v", err)
	}
	select {
	case err := <-done4:
		done4 <- err
	case <-time.After(5 * time.Second):
		t.Fatal("client 4 failed to detach")
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

	execCmd, err := server.PrepareTerminal(termCtx, cmd, tmux.TerminalStreams{In: term, Out: term, Err: term}, tmux.TerminalOptions{Start: tmux.AllowStart})
	if err != nil {
		t.Fatalf("PrepareTerminal failed: %v", err)
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
	info, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("client.Info failed: %v", err)
	}

	sess, err := server.FindSession(ctx, sessName)
	if err != nil {
		t.Fatalf("FindSession failed: %v", err)
	}
	sid, ok := info.SessionID.Get()
	if !ok || sid != sess.ID() {
		t.Errorf("expected session ID %v, got %v (ok=%v)", sess.ID(), sid, ok)
	}

	// Detach client gracefully
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

func TestIntegrationPrepareDefaultTerminal(t *testing.T) {
	server, _, ctx := apiFixture(t)

	term := attachmentTerminal(t)
	termCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	execCmd, err := server.PrepareDefaultTerminal(termCtx, tmux.TerminalStreams{In: term, Out: term, Err: term}, tmux.TerminalOptions{Start: tmux.AllowStart})
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

	execCmd, err := server.PrepareTerminalSequence(termCtx, seq, tmux.TerminalStreams{In: term, Out: term, Err: term}, tmux.TerminalOptions{Start: tmux.AllowStart})
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

	execCmd, err := session.PrepareAttach(termCtx, tmux.TerminalStreams{In: term, Out: term, Err: term}, tmux.AttachOptions{
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

	// 1. server.PrepareAttach with valid session ID
	term1 := attachmentTerminal(t)
	termCtx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()

	execCmd1, err := server.PrepareAttach(termCtx1, session.ID(), tmux.TerminalStreams{In: term1, Out: term1, Err: term1}, tmux.AttachOptions{
		ReadOnly:            false,
		PreserveEnvironment: false,
		Detach:              tmux.DetachNone,
		Dir:                 "",
		Flags:               nil,
	})
	if err != nil {
		t.Fatalf("server.PrepareAttach failed: %v", err)
	}

	if err := execCmd1.Start(); err != nil {
		t.Fatalf("execCmd1.Start failed: %v", err)
	}

	done1 := make(chan error, 1)
	go func() {
		done1 <- execCmd1.Wait()
	}()

	t.Cleanup(func() {
		cancel1()
		_ = execCmd1.Process.Kill()
		select {
		case <-done1:
		case <-time.After(5 * time.Second):
		}
	})

	client1 := waitTerminalClient(t, ctx, server, term1, done1)
	if err := client1.Detach(ctx); err != nil {
		t.Fatalf("client1.Detach failed: %v", err)
	}

	select {
	case err := <-done1:
		done1 <- err
		if err != nil {
			t.Fatalf("execCmd1.Wait error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client 1 did not exit after detach")
	}

	// 2. server.PrepareAttachTarget with empty target (attaching to default session)
	term2 := attachmentTerminal(t)
	termCtx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()

	execCmd2, err := server.PrepareAttachTarget(termCtx2, "", tmux.TerminalStreams{In: term2, Out: term2, Err: term2}, tmux.AttachOptions{
		ReadOnly:            false,
		PreserveEnvironment: false,
		Detach:              tmux.DetachNone,
		Dir:                 "",
		Flags:               nil,
	})
	if err != nil {
		t.Fatalf("server.PrepareAttachTarget with empty target failed: %v", err)
	}

	if err := execCmd2.Start(); err != nil {
		t.Fatalf("execCmd2.Start failed: %v", err)
	}

	done2 := make(chan error, 1)
	go func() {
		done2 <- execCmd2.Wait()
	}()

	t.Cleanup(func() {
		cancel2()
		_ = execCmd2.Process.Kill()
		select {
		case <-done2:
		case <-time.After(5 * time.Second):
		}
	})

	client2 := waitTerminalClient(t, ctx, server, term2, done2)
	if err := client2.Detach(ctx); err != nil {
		t.Fatalf("client2.Detach failed: %v", err)
	}

	select {
	case err := <-done2:
		done2 <- err
		if err != nil {
			t.Fatalf("execCmd2.Wait error on detach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client 2 did not exit after detach")
	}
}

func TestIntegrationAttach_MasterHangupTeardown(t *testing.T) {
	server, session, ctx := apiFixture(t)

	master, slave := openPTY(t)
	attachCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- session.Attach(attachCtx, tmux.TerminalStreams{In: slave, Out: slave, Err: slave}, tmux.AttachOptions{})
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
