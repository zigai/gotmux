//go:build integration && (linux || darwin)

package tmux_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

const terminalLogBytes = 64 << 10

type terminalLog struct {
	mu   sync.Mutex
	data []byte
}

func (log *terminalLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()

	n := len(data)
	if len(data) > terminalLogBytes {
		data = data[len(data)-terminalLogBytes:]
	}

	if excess := len(log.data) + len(data) - terminalLogBytes; excess > 0 {
		log.data = log.data[excess:]
	}

	log.data = append(log.data, data...)

	return n, nil
}

func (log *terminalLog) contains(text string) bool {
	log.mu.Lock()
	defer log.mu.Unlock()

	return bytes.Contains(log.data, []byte(text))
}

func uiClient(t *testing.T, ctx context.Context, server *tmux.Server, session tmux.Session) (tmux.Client, *os.File, *terminalLog) {
	t.Helper()
	master, slave := openPTY(t)
	output := &terminalLog{mu: sync.Mutex{}, data: nil}

	readDone := make(chan struct{})
	go func() { defer close(readDone); _, _ = io.Copy(output, master) }()

	t.Cleanup(func() { _ = master.Close(); <-readDone })

	attachCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		var options tmux.AttachOptions
		done <- session.Attach(attachCtx, tmux.TerminalStreams{In: slave, Out: slave, Err: slave}, options)
	}()

	t.Cleanup(func() {
		cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("UI attachment failed to stop")
		}
	})

	return waitTerminalClient(t, ctx, server, slave, done), master, output
}

func waitUIResult(t *testing.T, ctx context.Context, result <-chan error) {
	t.Helper()

	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func awaitUserOption(t *testing.T, ctx context.Context, session tmux.Session, name, value string) {
	t.Helper()
	awaitObservation(t, ctx, "user option "+name, func() bool {
		got, err := session.Options().User(ctx, name)
		if err != nil {
			t.Fatal(err)
		}

		actual, ok := got.Local.Get()

		return ok && actual == value
	})
}

func TestIntegrationClientMenu(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, terminal, output := uiClient(t, ctx, server, session)
	command := testCommand(t, "set-option", "-t", string(session.ID()), "@menu-effect", "selected")
	items := []tmux.MenuItem{{Label: "TGO_MENU_READY", Key: "x", Commands: testSequence(t, command), Separator: false, Disabled: false}}
	result := make(chan error, 1)

	go func() {
		var options tmux.MenuOptions
		result <- client.Menu(ctx, items, options)
	}()

	awaitObservation(t, ctx, "menu rendered", func() bool { return output.contains("TGO_MENU_READY") })

	if _, err := terminal.WriteString("x"); err != nil {
		t.Fatal(err)
	}

	waitUIResult(t, ctx, result)
	awaitUserOption(t, ctx, session, "@menu-effect", "selected")
}

func TestIntegrationClientMenuMouseAndDismissal(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, terminal, output := uiClient(t, ctx, server, session)

	// Subtest 1: Mouse: false (default), dismissed via 'q' key without selection
	t.Run("MouseFalseDismissal", func(t *testing.T) {
		command := testCommand(t, "set-option", "-t", string(session.ID()), "@menu-not-selected", "ran")
		items := []tmux.MenuItem{
			{Label: "TGO_MENU_ITEM_1", Key: "1", Commands: testSequence(t, command), Separator: false, Disabled: false},
		}

		result := make(chan error, 1)
		go func() {
			result <- client.Menu(ctx, items, tmux.MenuOptions{
				Title: "Menu-MouseFalse",
				Mouse: false,
			})
		}()

		awaitObservation(t, ctx, "menu rendered", func() bool {
			return output.contains("TGO_MENU_ITEM_1")
		})

		// Send 'q' to dismiss the menu without executing the item command
		if _, err := terminal.WriteString("q"); err != nil {
			t.Fatal(err)
		}

		waitUIResult(t, ctx, result)

		// Verify menu command was not executed
		val, err := session.Options().User(ctx, "@menu-not-selected")
		if err == nil {
			if str, ok := val.Local.Get(); ok && str == "ran" {
				t.Fatal("expected menu command NOT to run on dismissal via 'q'")
			}
		}
	})

	// Subtest 2: Mouse: true with RequireClick: true, selected via item key
	t.Run("MouseTrueSelection", func(t *testing.T) {
		command := testCommand(t, "set-option", "-t", string(session.ID()), "@menu-mouse-effect", "selected")
		items := []tmux.MenuItem{
			{Label: "TGO_MENU_ITEM_M", Key: "m", Commands: testSequence(t, command), Separator: false, Disabled: false},
			tmux.MenuSeparator(),
		}

		result := make(chan error, 1)
		go func() {
			result <- client.Menu(ctx, items, tmux.MenuOptions{
				Title:        "Menu-MouseTrue",
				Mouse:        true,
				RequireClick: true,
			})
		}()

		awaitObservation(t, ctx, "mouse menu rendered", func() bool {
			return output.contains("TGO_MENU_ITEM_M")
		})

		// Choose the item via key shortcut 'm'
		if _, err := terminal.WriteString("m"); err != nil {
			t.Fatal(err)
		}

		waitUIResult(t, ctx, result)
		awaitUserOption(t, ctx, session, "@menu-mouse-effect", "selected")
	})
}

func TestIntegrationClientPrompt(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, terminal, output := uiClient(t, ctx, server, session)

	var options tmux.PromptOptions

	options.Label = "TGO_PROMPT_READY"
	template := tmux.PromptTemplate("set-option -t " + string(session.ID()) + " @prompt-effect '%%'")

	result := make(chan error, 1)
	go func() { result <- client.Prompt(ctx, template, options) }()

	awaitObservation(t, ctx, "prompt rendered", func() bool { return output.contains("TGO_PROMPT_READY") })

	if _, err := terminal.WriteString("typed-value\r"); err != nil {
		t.Fatal(err)
	}

	waitUIResult(t, ctx, result)
	awaitUserOption(t, ctx, session, "@prompt-effect", "typed-value")
}

func TestIntegrationClientPopup(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, _, _ := uiClient(t, ctx, server, session)
	dir := t.TempDir()

	var options tmux.PopupOptions

	options.Dir = dir
	options.Env = map[string]string{"TGO_POPUP_VALUE": "literal ; #{pane_id}"}
	options.Program = tmux.Exec("/bin/sh", "-c", `printf '%s\n%s' "$PWD" "$TGO_POPUP_VALUE" > result`)

	options.CloseOnExit = true
	if err := client.Popup(ctx, options); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "result")
	awaitFile(t, ctx, path)
	assertFileBytes(t, path, []byte(dir+"\nliteral ; #{pane_id}"))

	if _, err := client.Info(ctx); err != nil {
		t.Fatalf("popup detached client: %v", err)
	}
}

func TestIntegrationClientBinding(t *testing.T) {
	server, session, ctx := apiFixture(t)
	_, terminal, _ := uiClient(t, ctx, server, session)
	command := testCommand(t, "set-option", "-t", string(session.ID()), "@key-effect", "pressed")

	var options tmux.BindOptions
	if err := server.Bind(ctx, "root", "x", testSequence(t, command), options); err != nil {
		t.Fatal(err)
	}

	if _, err := terminal.WriteString("x"); err != nil {
		t.Fatal(err)
	}

	awaitUserOption(t, ctx, session, "@key-effect", "pressed")
}

func TestIntegrationMessage(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, _, output := uiClient(t, ctx, server, session)

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("panes: %v", err)
	}

	pane := panes[0].Handle()

	if err := client.Message(ctx, "CLIENT_MSG_OK"); err != nil {
		t.Fatalf("client.Message: %v", err)
	}

	awaitObservation(t, ctx, "client message rendered", func() bool { return output.contains("CLIENT_MSG_OK") })

	if err := pane.Message(ctx, "PANE_MSG_OK"); err != nil {
		t.Fatalf("pane.Message: %v", err)
	}

	awaitObservation(t, ctx, "pane message rendered", func() bool { return output.contains("PANE_MSG_OK") })

	if err := server.Message(ctx, "SERVER_MSG_OK"); err != nil {
		t.Fatalf("server.Message: %v", err)
	}

	awaitObservation(t, ctx, "server message rendered", func() bool { return output.contains("SERVER_MSG_OK") })
}

func TestIntegrationClientPopupWithoutDeadline(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, _, _ := uiClient(t, ctx, server, session)
	dir := t.TempDir()

	var options tmux.PopupOptions

	options.Dir = dir
	options.Program = tmux.Exec("/bin/sh", "-c", `printf 'ok' > result`)
	options.CloseOnExit = true

	// context.Background() has no deadline; it must succeed without error.
	if err := client.Popup(context.Background(), options); err != nil {
		t.Fatalf("client.Popup without deadline failed: %v", err)
	}

	path := filepath.Join(dir, "result")
	awaitFile(t, ctx, path)
	assertFileBytes(t, path, []byte("ok"))
}

func TestIntegrationClientSwitchToggleReadOnly(t *testing.T) {
	server, session1, ctx := apiFixture(t)

	// Create a second session to switch between
	session2, err := server.NewSession(ctx, tmux.NewSessionOptions{
		Window:  "s2-win",
		Program: tmux.Shell("sleep 60"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: Start client in Read-Write mode (AttachOptions{ReadOnly: false})
	t.Run("StartReadWrite_ToggleToReadOnly_ThenToggleBack", func(t *testing.T) {
		client, _, _ := uiClient(t, ctx, server, session1)

		// Initial state: ReadOnly must be false
		info, err := client.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.ReadOnly {
			t.Fatalf("expected initial client to be read-write, got ReadOnly=%v", info.ReadOnly)
		}

		// First toggle: Switch to session2 with ToggleReadOnly: true -> becomes read-only
		err = client.Switch(ctx, session2, tmux.SwitchOptions{
			ToggleReadOnly: true,
		})
		if err != nil {
			t.Fatalf("client.Switch with ToggleReadOnly failed: %v", err)
		}

		awaitObservation(t, ctx, "client switched to session2 and read-only", func() bool {
			ci, err := client.Info(ctx)
			if err != nil {
				return false
			}
			sid, ok := ci.SessionID.Get()
			return ok && ci.ReadOnly && sid == session2.ID()
		})

		// Switch without toggle (ToggleReadOnly: false) -> remains read-only
		err = client.Switch(ctx, session1, tmux.SwitchOptions{
			ToggleReadOnly: false,
		})
		if err != nil {
			t.Fatalf("client.Switch with ToggleReadOnly:false failed: %v", err)
		}

		awaitObservation(t, ctx, "client switched to session1 and still read-only", func() bool {
			ci, err := client.Info(ctx)
			if err != nil {
				return false
			}
			sid, ok := ci.SessionID.Get()
			return ok && ci.ReadOnly && sid == session1.ID()
		})

		// Second toggle: Switch back to session2 with ToggleReadOnly: true -> becomes read-write
		err = client.Switch(ctx, session2, tmux.SwitchOptions{
			ToggleReadOnly: true,
		})
		if err != nil {
			t.Fatalf("client.Switch with ToggleReadOnly back to read-write failed: %v", err)
		}

		awaitObservation(t, ctx, "client read-write again", func() bool {
			ci, err := client.Info(ctx)
			if err != nil {
				return false
			}
			sid, ok := ci.SessionID.Get()
			return ok && !ci.ReadOnly && sid == session2.ID()
		})
	})

	// Case 2: Start client in Read-Only mode (AttachOptions{ReadOnly: true})
	t.Run("StartReadOnly_ToggleToReadWrite", func(t *testing.T) {
		master, slave := openPTY(t)
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			discard := make([]byte, 1024)
			for {
				if _, err := master.Read(discard); err != nil {
					return
				}
			}
		}()
		t.Cleanup(func() { _ = master.Close(); <-readDone })

		attachCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)

		go func() {
			// Attach with ReadOnly: true
			done <- session1.Attach(attachCtx, tmux.TerminalStreams{In: slave, Out: slave, Err: slave}, tmux.AttachOptions{
				ReadOnly: true,
			})
		}()

		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("UI attachment failed to stop")
			}
		})

		client := waitTerminalClient(t, ctx, server, slave, done)

		// Verify initial state is ReadOnly: true (from AttachOptions.ReadOnly)
		info, err := client.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ReadOnly {
			t.Fatalf("expected client started with AttachOptions.ReadOnly=true to be read-only, got ReadOnly=%v", info.ReadOnly)
		}

		// Toggle read-only using Switch with ToggleReadOnly: true -> becomes read-write
		err = client.Switch(ctx, session2, tmux.SwitchOptions{
			ToggleReadOnly: true,
		})
		if err != nil {
			t.Fatalf("client.Switch with ToggleReadOnly failed: %v", err)
		}

		awaitObservation(t, ctx, "read-only client toggled to read-write", func() bool {
			ci, err := client.Info(ctx)
			if err != nil {
				return false
			}
			sid, ok := ci.SessionID.Get()
			return ok && !ci.ReadOnly && sid == session2.ID()
		})
	})
}
