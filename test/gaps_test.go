//go:build integration

package tmux_test

import (
	"errors"
	"os/user"
	"slices"
	"strings"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationServerAccess(t *testing.T) {
	server, _, ctx := apiFixture(t)
	info, _ := server.Probe(ctx)
	t.Logf("Probe version: %+v, raw: %q", info.Version, info.Version.Raw)

	// List access entries
	entries, err := server.AccessList(ctx)
	if err != nil {
		var cmdErr *tmux.CommandError
		if errors.As(err, &cmdErr) {
			t.Fatalf("AccessList stderr: %q, stdout: %q", string(cmdErr.Result.Stderr), string(cmdErr.Result.Stdout))
		}
		t.Fatalf("AccessList failed: %v", err)
	}
	t.Logf("AccessList entries (%d): %+v", len(entries), entries)
	if len(entries) == 0 {
		t.Fatal("expected at least one access entry for current user/owner")
	}

	// Grant read-only access to a user
	const testUser = "nobody"
	if err := server.GrantAccess(ctx, testUser, tmux.AccessOptions{Group: false, ReadOnly: true}); err != nil {
		t.Fatalf("GrantAccess(RO) failed: %v", err)
	}

	// Check that nobody is in the list with ReadOnly = true
	entries, err = server.AccessList(ctx)
	if err != nil {
		t.Fatalf("AccessList failed after grant RO: %v", err)
	}
	idx := slices.IndexFunc(entries, func(e tmux.AccessEntry) bool {
		return e.Name == testUser
	})
	if idx < 0 {
		t.Fatalf("expected user %q in access list, got: %+v", testUser, entries)
	}
	if !entries[idx].ReadOnly {
		t.Errorf("expected user %q to have ReadOnly=true, got: %+v", testUser, entries[idx])
	}
	if entries[idx].IsGroup {
		t.Errorf("expected user %q to have IsGroup=false, got: %+v", testUser, entries[idx])
	}

	// Grant read-write access to the same user
	if err := server.GrantAccess(ctx, testUser, tmux.AccessOptions{Group: false, ReadOnly: false}); err != nil {
		t.Fatalf("GrantAccess(RW) failed: %v", err)
	}

	entries, err = server.AccessList(ctx)
	if err != nil {
		t.Fatalf("AccessList failed after grant RW: %v", err)
	}
	idx = slices.IndexFunc(entries, func(e tmux.AccessEntry) bool {
		return e.Name == testUser
	})
	if idx < 0 {
		t.Fatalf("expected user %q in access list, got: %+v", testUser, entries)
	}
	if entries[idx].ReadOnly {
		t.Errorf("expected user %q to have ReadOnly=false, got: %+v", testUser, entries[idx])
	}

	// Revoke user access
	if err := server.RevokeAccess(ctx, testUser, false); err != nil {
		t.Fatalf("RevokeAccess failed: %v", err)
	}

	// Verify user is gone
	entries, err = server.AccessList(ctx)
	if err != nil {
		t.Fatalf("AccessList failed after revoke: %v", err)
	}
	idx = slices.IndexFunc(entries, func(e tmux.AccessEntry) bool {
		return e.Name == testUser
	})
	if idx >= 0 {
		t.Fatalf("expected user %q to be removed from access list, but still present: %+v", testUser, entries)
	}

	// Test Group access if a valid group is discoverable
	currUser, err := user.Current()
	if err == nil && currUser.Gid != "" {
		grp, grpErr := user.LookupGroupId(currUser.Gid)
		if grpErr == nil && grp.Name != "" && !strings.Contains(grp.Name, " ") {
			if err := server.GrantAccess(ctx, grp.Name, tmux.AccessOptions{Group: true, ReadOnly: true}); err == nil {
				entries, err = server.AccessList(ctx)
				if err == nil {
					idx = slices.IndexFunc(entries, func(e tmux.AccessEntry) bool {
						return e.Name == grp.Name && e.IsGroup
					})
					if idx < 0 {
						t.Logf("group %q not listed as distinct group entry", grp.Name)
					}
				}
				_ = server.RevokeAccess(ctx, grp.Name, true)
			}
		}
	}
}

func TestIntegrationClockModeAndSendPrefix(t *testing.T) {
	server, session, ctx := apiFixture(t)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "clock-test"})
	if err != nil {
		t.Fatal(err)
	}
	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Send primary prefix
	if err := pane.SendPrefix(ctx, false); err != nil {
		t.Fatalf("SendPrefix(false) failed: %v", err)
	}

	// Send secondary prefix (-2)
	if err := pane.SendPrefix(ctx, true); err != nil {
		t.Fatalf("SendPrefix(true) failed: %v", err)
	}

	// Clock mode
	if err := pane.ClockMode(ctx); err != nil {
		t.Fatalf("ClockMode failed: %v", err)
	}

	// Send key to exit clock mode
	_ = server
	_ = pane.SendKeys(ctx, "q")
}

func TestIntegrationMessagesAndPromptHistory(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// Server Messages
	msgs, err := server.Messages(ctx, tmux.MessagesOptions{})
	if err != nil {
		t.Fatalf("server.Messages failed: %v", err)
	}
	if len(msgs) == 0 {
		t.Log("no messages returned (acceptable on fresh server)")
	}

	// Terminal capabilities
	_, err = server.Messages(ctx, tmux.MessagesOptions{Terminal: true})
	if err != nil {
		t.Fatalf("server.Messages(Terminal) failed: %v", err)
	}

	// Jobs
	_, err = server.Messages(ctx, tmux.MessagesOptions{Jobs: true})
	if err != nil {
		t.Fatalf("server.Messages(Jobs) failed: %v", err)
	}

	// Client Messages (via attached client)
	client, _, _ := uiClient(t, ctx, server, session)
	clientMsgs, err := client.Messages(ctx, tmux.MessagesOptions{})
	if err != nil {
		t.Fatalf("client.Messages failed: %v", err)
	}
	t.Logf("client.Messages count: %d", len(clientMsgs))

	// Prompt history
	_, err = server.PromptHistory(ctx, "command")
	if err != nil {
		t.Fatalf("PromptHistory failed: %v", err)
	}

	if err := server.ClearPromptHistory(ctx, "command"); err != nil {
		t.Fatalf("ClearPromptHistory failed: %v", err)
	}
}

func TestIntegrationSplitOptionsEnhanced(t *testing.T) {
	server, session, ctx := apiFixture(t)
	probeInfo, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !probeInfo.Version.AtLeast(3, 8) {
		t.Skip("skipping test requiring tmux 3.8+")
	}

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "split-enhanced"})
	if err != nil {
		t.Fatal(err)
	}
	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const testTitle = "CustomPaneTitle"
	newPane, err := pane.Split(ctx, tmux.SplitOptions{
		Direction:   tmux.Vertical,
		Title:       testTitle,
		BorderLines: tmux.PaneBorderSingle,
		Zoom:        false,
	})
	if err != nil {
		var cmdErr *tmux.CommandError
		if errors.As(err, &cmdErr) {
			t.Fatalf("Split stderr: %q", string(cmdErr.Result.Stderr))
		}
		t.Fatalf("Split with enhanced options failed: %v", err)
	}
	if !newPane.Valid() {
		t.Fatal("expected valid pane handle")
	}

	info, err := newPane.Info(ctx)
	if err != nil {
		t.Fatalf("newPane.Info failed: %v", err)
	}
	if info.Title != testTitle {
		t.Errorf("expected pane title %q, got %q", testTitle, info.Title)
	}

	// Test Split with KillTarget (-k) sets remain-on-exit
	paneWithK, err := newPane.Split(ctx, tmux.SplitOptions{
		Direction:  tmux.Horizontal,
		KillTarget: true,
	})
	if err != nil {
		t.Fatalf("Split with KillTarget failed: %v", err)
	}
	if !paneWithK.Valid() {
		t.Fatal("expected valid pane from Split with KillTarget")
	}

	optVal, err := paneWithK.Options().Get(ctx, "remain-on-exit")
	if err != nil {
		t.Fatalf("failed to query remain-on-exit: %v", err)
	}
	if val, ok := optVal.Local.Get(); !ok || val != "key" {
		t.Errorf("expected remain-on-exit to be 'key', got: %q (ok=%v)", val, ok)
	}
}

func TestIntegrationNewPaneFloating(t *testing.T) {
	server, session, ctx := apiFixture(t)
	probeInfo, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !probeInfo.Version.AtLeast(3, 8) {
		t.Skip("skipping test requiring tmux 3.8+")
	}

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "float-win"})
	if err != nil {
		t.Fatal(err)
	}

	window := link.Window()

	// 1. Create a floating pane via Window.NewPane
	floatPane, err := window.NewPane(ctx, tmux.NewPaneOptions{
		Width:       "50",
		Height:      "15",
		X:           "10",
		Y:           "5",
		Title:       "FloatingPane",
		BorderLines: tmux.PaneBorderDouble,
	})
	if err != nil {
		var cmdErr *tmux.CommandError
		if errors.As(err, &cmdErr) {
			t.Fatalf("NewPane stderr: %q", string(cmdErr.Result.Stderr))
		}
		t.Fatalf("Window.NewPane failed: %v", err)
	}
	if !floatPane.Valid() {
		t.Fatal("expected valid floating pane handle")
	}

	info, err := floatPane.Info(ctx)
	if err != nil {
		t.Fatalf("floatPane.Info failed: %v", err)
	}
	if info.Title != "FloatingPane" {
		t.Errorf("expected title FloatingPane, got %q", info.Title)
	}

	// Submit text in floating pane
	if err := floatPane.Submit(ctx, "echo hello-floating"); err != nil {
		t.Fatalf("floatPane.Submit failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	captured, err := floatPane.Capture(ctx, tmux.CaptureOptions{
		JoinWrapped:        true,
		EscapeNonPrintable: true,
	})
	if err != nil {
		t.Fatalf("floatPane.Capture failed: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("expected non-empty captured output")
	}

	// 2. Create another pane via Pane.NewPane targeting the existing pane
	childPane, err := floatPane.NewPane(ctx, tmux.NewPaneOptions{
		Width:       "30",
		Height:      "10",
		X:           "20",
		Y:           "8",
		Title:       "ChildFloatPane",
		BorderLines: tmux.PaneBorderHeavy,
	})
	if err != nil {
		t.Fatalf("Pane.NewPane failed: %v", err)
	}
	if !childPane.Valid() {
		t.Fatal("expected valid child pane handle")
	}

	childInfo, err := childPane.Info(ctx)
	if err != nil {
		t.Fatalf("childPane.Info failed: %v", err)
	}
	if childInfo.Title != "ChildFloatPane" {
		t.Errorf("expected title ChildFloatPane, got %q", childInfo.Title)
	}

	// 3. Create a modal pane with CloseOnCancel
	modalPane, err := window.NewPane(ctx, tmux.NewPaneOptions{
		Width:          "40",
		Height:         "12",
		Modal:          true,
		CloseOnClick:   true,
		CaptureAllKeys: true,
		Title:          "ModalPane",
		BorderLines:    tmux.PaneBorderSingle,
	})
	if err != nil {
		t.Fatalf("Window.NewPane(Modal) failed: %v", err)
	}
	if !modalPane.Valid() {
		t.Fatal("expected valid modal pane handle")
	}
}

func TestIntegrationRespawnPreserveEnvironment(t *testing.T) {
	server, session, ctx := apiFixture(t)
	probeInfo, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !probeInfo.Version.AtLeast(3, 8) {
		t.Skip("skipping test requiring tmux 3.8+")
	}

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "respawn-win"})
	if err != nil {
		t.Fatal(err)
	}
	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Respawn pane with PreserveEnvironment
	if err := pane.Respawn(ctx, tmux.RespawnOptions{
		KillRunning:         true,
		PreserveEnvironment: true,
	}); err != nil {
		var cmdErr *tmux.CommandError
		if errors.As(err, &cmdErr) {
			t.Fatalf("Respawn stderr: %q", string(cmdErr.Result.Stderr))
		}
		t.Fatalf("pane.Respawn with PreserveEnvironment failed: %v", err)
	}

	// Respawn window with PreserveEnvironment on a dedicated window
	link2, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "respawn-win2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := link2.Window().Respawn(ctx, tmux.RespawnOptions{
		KillRunning:         true,
		PreserveEnvironment: true,
	}); err != nil {
		var cmdErr *tmux.CommandError
		if errors.As(err, &cmdErr) {
			t.Fatalf("Respawn window stderr: %q", string(cmdErr.Result.Stderr))
		}
		t.Fatalf("window.Respawn with PreserveEnvironment failed: %v", err)
	}
}

func TestIntegrationCaptureOptionsEnhanced(t *testing.T) {
	_, session, ctx := apiFixture(t)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "capture-opt-test"})
	if err != nil {
		t.Fatal(err)
	}
	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "printf 'Testing escape sequences: \\033[31mred\\033[0m \\001\\002\\n'"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	captured, err := pane.Capture(ctx, tmux.CaptureOptions{
		EscapeNonPrintable:  true,
		AlternateScreenOnly: false,
	})
	if err != nil {
		t.Fatalf("pane.Capture with EscapeNonPrintable failed: %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("expected non-empty capture")
	}
}
