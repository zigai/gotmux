//go:build integration

package tmux_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func outcomeOf(err error) tmux.Outcome {
	if op, ok := errors.AsType[*tmux.OperationError](err); ok {
		return op.Outcome
	}

	if ce, ok := errors.AsType[*tmux.CommandError](err); ok {
		return ce.Outcome
	}

	return tmux.Outcome{Effect: tmux.NotSent}
}

func TestDaemonReplacement(t *testing.T) {
	ctx := integrationContext(t)

	// 1. Start Server A via tmuxtest.NewServer(t).
	serverA := tmuxtest.NewServer(t)

	// 2. Probe Server A: infoA, err := serverA.Probe(ctx).
	// Acquire session handle sessionA, err := serverA.FindSession(ctx, "fixture").
	infoA, err := serverA.Probe(ctx)
	if err != nil {
		t.Fatalf("serverA.Probe failed: %v", err)
	}

	sessionA, err := serverA.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatalf("serverA.FindSession failed: %v", err)
	}

	// 3. Kill Server A process directly:
	// p, _ := os.FindProcess(infoA.Identity.PID); _ = p.Kill(); _, _ = p.Wait(). Give a short grace period (50ms) for socket release.
	p, err := os.FindProcess(infoA.Identity.PID)
	if err != nil {
		t.Fatalf("os.FindProcess failed: %v", err)
	}
	_ = p.Kill()
	_, _ = p.Wait()
	time.Sleep(50 * time.Millisecond)

	// 4. Start Server B on the exact same socket path:
	// Use exec.Command(binary, "-S", infoA.Identity.ReportedSocket, "-f", "/dev/null", "new-session", "-d", "-s", "replacement").
	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary = "tmux"
	}
	binary, err = exec.LookPath(binary)
	if err != nil {
		t.Fatalf("exec.LookPath(%q) failed: %v", binary, err)
	}

	cmd := exec.Command(binary, "-S", infoA.Identity.ReportedSocket, "-f", "/dev/null", "new-session", "-d", "-s", "replacement")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to start Server B: %v, output: %s", err, out)
	}

	serverB, err := tmux.New(tmux.Config{SocketPath: infoA.Identity.ReportedSocket})
	if err != nil {
		t.Fatalf("tmux.New for Server B failed: %v", err)
	}

	// Wait for Server B to respond to probe.
	var infoB tmux.ServerInfo
	deadline := time.Now().Add(5 * time.Second)
	for {
		infoB, err = serverB.Probe(ctx)
		if err == nil && infoB.Identity.PID != infoA.Identity.PID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for Server B probe: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Register t.Cleanup to kill Server B.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = serverB.KillIfIdentity(cleanupCtx, infoB.Identity)
		if pB, err := os.FindProcess(infoB.Identity.PID); err == nil {
			_ = pB.Kill()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if _, probeErr := serverB.Probe(cleanupCtx); errors.Is(probeErr, tmux.ErrNoServer) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	})

	// 5. Attempt mutation using handle from Server A:
	// _, err = sessionA.NewWindow(ctx, tmux.NewWindowOptions{Name: "should-fail"})
	_, err = sessionA.NewWindow(ctx, tmux.NewWindowOptions{Name: "should-fail"})

	// 6. Assert that err is returned, errors.Is(err, tmux.ErrServerChanged) is true, and outcomeOf(err).Effect == tmux.NotSent.
	if err == nil {
		t.Fatal("expected mutation using stale Server A handle to fail, got nil")
	}
	if !errors.Is(err, tmux.ErrServerChanged) {
		t.Fatalf("expected ErrServerChanged, got: %v", err)
	}
	if outcome := outcomeOf(err); outcome.Effect != tmux.NotSent {
		t.Fatalf("expected outcome effect NotSent, got: %v", outcome.Effect)
	}

	// 7. Verify that on Server B, no window named "should-fail" was created.
	windowsB, err := serverB.Windows(ctx)
	if err != nil {
		t.Fatalf("serverB.Windows failed: %v", err)
	}
	for _, w := range windowsB {
		if w.Name == "should-fail" {
			t.Fatalf("window 'should-fail' was created on Server B despite guard rejection")
		}
	}
}
