//go:build integration

package tmux_test

import (
	"context"
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

	entries := accessEntries(t, ctx, server)
	t.Logf("AccessList entries (%d): %+v", len(entries), entries)

	if len(entries) == 0 {
		t.Fatal("expected at least one access entry for current user/owner")
	}

	// Grant read-only access to a user
	const testUser = "nobody"
	if err := server.GrantAccess(ctx, testUser, tmux.AccessOptions{Group: false, ReadOnly: true}); err != nil {
		t.Fatalf("GrantAccess(RO) failed: %v", err)
	}

	entry := requireAccessEntry(t, ctx, server, testUser)
	if !entry.ReadOnly || entry.IsGroup {
		t.Errorf("expected user %q with ReadOnly=true and IsGroup=false, got: %+v", testUser, entry)
	}

	// Grant read-write access to the same user
	if err := server.GrantAccess(ctx, testUser, tmux.AccessOptions{Group: false, ReadOnly: false}); err != nil {
		t.Fatalf("GrantAccess(RW) failed: %v", err)
	}

	if entry := requireAccessEntry(t, ctx, server, testUser); entry.ReadOnly {
		t.Errorf("expected user %q to have ReadOnly=false, got: %+v", testUser, entry)
	}

	// Revoke user access
	if err := server.RevokeAccess(ctx, testUser, false); err != nil {
		t.Fatalf("RevokeAccess failed: %v", err)
	}

	if entries := accessEntries(t, ctx, server); slices.ContainsFunc(entries, func(e tmux.AccessEntry) bool { return e.Name == testUser }) {
		t.Fatalf("expected user %q to be removed from access list, but still present: %+v", testUser, entries)
	}

	exerciseGroupAccess(t, ctx, server)
}

func accessEntries(t *testing.T, ctx context.Context, server *tmux.Server) []tmux.AccessEntry {
	t.Helper()

	entries, err := server.AccessList(ctx)
	if err != nil {
		fatalCommand(t, "AccessList", err)
	}

	return entries
}

func requireAccessEntry(t *testing.T, ctx context.Context, server *tmux.Server, name string) tmux.AccessEntry {
	t.Helper()

	entries := accessEntries(t, ctx, server)

	idx := slices.IndexFunc(entries, func(e tmux.AccessEntry) bool { return e.Name == name })
	if idx < 0 {
		t.Fatalf("expected user %q in access list, got: %+v", name, entries)
	}

	return entries[idx]
}

// exerciseGroupAccess is best effort: it only logs a missing group entry.
func exerciseGroupAccess(t *testing.T, ctx context.Context, server *tmux.Server) {
	t.Helper()

	group, ok := currentGroupName()
	if !ok {
		return
	}

	if err := server.GrantAccess(ctx, group, tmux.AccessOptions{Group: true, ReadOnly: true}); err != nil {
		return
	}

	defer func() { _ = server.RevokeAccess(ctx, group, true) }()

	entries, err := server.AccessList(ctx)
	if err != nil {
		return
	}

	if !slices.ContainsFunc(entries, func(e tmux.AccessEntry) bool { return e.Name == group && e.IsGroup }) {
		t.Logf("group %q not listed as distinct group entry", group)
	}
}

func currentGroupName() (string, bool) {
	current, err := user.Current()
	if err != nil || current.Gid == "" {
		return "", false
	}

	group, err := user.LookupGroupId(current.Gid)
	if err != nil || group.Name == "" || strings.Contains(group.Name, " ") {
		return "", false
	}

	return group.Name, true
}

func fatalCommand(t *testing.T, call string, err error) {
	t.Helper()

	if cmdErr, ok := errors.AsType[*tmux.CommandError](err); ok {
		t.Fatalf("%s failed: %v; stderr: %q, stdout: %q", call, err, cmdErr.Result.Stderr, cmdErr.Result.Stdout)
	}

	t.Fatalf("%s failed: %v", call, err)
}

func requireTmux38(t *testing.T, ctx context.Context, server *tmux.Server) {
	t.Helper()

	probeInfo, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !probeInfo.Version.AtLeast(3, 8) {
		t.Skip("skipping test requiring tmux 3.8+")
	}
}

func assertPaneTitle(t *testing.T, ctx context.Context, pane tmux.Pane, want string) {
	t.Helper()

	info, err := pane.Info(ctx)
	if err != nil {
		t.Fatalf("pane %s Info failed: %v", pane.ID(), err)
	}

	if info.Title != want {
		t.Errorf("expected pane title %q, got %q", want, info.Title)
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
	requireTmux38(t, ctx, server)

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
		fatalCommand(t, "Split with enhanced options", err)
	}

	if !newPane.Valid() {
		t.Fatal("expected valid pane handle")
	}

	assertPaneTitle(t, ctx, newPane, testTitle)

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
	requireTmux38(t, ctx, server)

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
		fatalCommand(t, "Window.NewPane", err)
	}

	if !floatPane.Valid() {
		t.Fatal("expected valid floating pane handle")
	}

	assertPaneTitle(t, ctx, floatPane, "FloatingPane")

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

	assertPaneTitle(t, ctx, childPane, "ChildFloatPane")

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
	requireTmux38(t, ctx, server)

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
		if cmdErr, ok := errors.AsType[*tmux.CommandError](err); ok {
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
		if cmdErr, ok := errors.AsType[*tmux.CommandError](err); ok {
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
