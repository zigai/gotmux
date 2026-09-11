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
