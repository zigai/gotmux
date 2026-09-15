//go:build integration

package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationBufferFiles(t *testing.T) {
	server, _, ctx := apiFixture(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source ; #{pane_id}")
	destination := filepath.Join(dir, "saved ; #{pane_id}")

	want := []byte("\x00\xff\r\n;#{pane_id}\"'")
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}

	buffer, err := tmux.NamedBuffer("file-buffer")
	if err != nil {
		t.Fatal(err)
	}

	if err := server.LoadBufferFile(ctx, buffer, source); err != nil {
		t.Fatal(err)
	}

	got, err := server.ReadBuffer(ctx, buffer)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("loaded bytes: %q, %v", got, err)
	}

	if err := server.SaveBufferFile(ctx, buffer, destination, false); err != nil {
		t.Fatal(err)
	}

	assertFileBytes(t, destination, want)

	if err := server.SaveBufferFile(ctx, buffer, destination, true); err != nil {
		t.Fatal(err)
	}

	assertFileBytes(t, destination, bytes.Repeat(want, 2))

	if err := server.SaveBufferFile(ctx, buffer, destination, false); err != nil {
		t.Fatal(err)
	}

	assertFileBytes(t, destination, want)
}

func assertFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("%s bytes = %q, want %q", path, got, want)
	}
}

func TestIntegrationBufferPaste(t *testing.T) {
	server, session, ctx := apiFixture(t)
	dir := t.TempDir()
	script := `: > "$1/ready"
IFS= read -r line
printf '%s' "$line" > "$1/pending"
mv "$1/pending" "$1/result"
IFS= read -r hold`

	var window tmux.NewWindowOptions

	window.Program = tmux.Exec("/bin/sh", "-c", script, "receiver", dir)

	link, err := session.NewWindow(ctx, window)
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	awaitFile(t, ctx, filepath.Join(dir, "ready"))

	buffer, err := tmux.NamedBuffer("paste-buffer")
	if err != nil {
		t.Fatal(err)
	}

	const literal = "paste ; #{pane_id} $() ' \\"
	if err := server.WriteBuffer(ctx, buffer, []byte(literal+"\n")); err != nil {
		t.Fatal(err)
	}

	var options tmux.PasteOptions

	options.DeleteAfter = true
	if err := pane.PasteBuffer(ctx, buffer, options); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "result")
	awaitFile(t, ctx, path)
	assertFileBytes(t, path, []byte(literal))

	if _, err := server.ReadBuffer(ctx, buffer); !errors.Is(err, tmux.ErrNotFound) {
		if failure, ok := errors.AsType[*tmux.CommandError](err); ok {
			t.Logf("tmux stderr: %q", failure.Result.Stderr)
		}

		t.Fatalf("paste did not delete buffer: %v", err)
	}
}

func awaitFile(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	awaitObservation(t, ctx, path, func() bool {
		_, err := os.Stat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}

		return err == nil
	})
}

func TestIntegrationPipeLifecycle(t *testing.T) {
	_, session, ctx := apiFixture(t)
	dir, pane := outputProducer(t, ctx, session)

	path := filepath.Join(dir, "pipe")

	var pipe tmux.PipeOptions

	pipe.Output = true
	if err := pane.Pipe(ctx, "cat > '"+path+"'", pipe); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "before-stop"); err != nil {
		t.Fatal(err)
	}

	awaitObservation(t, ctx, "pipe output", func() bool {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}

		return bytes.Contains(data, []byte("OUTPUT:before-stop"))
	})

	if err := pane.StopPipe(ctx); err != nil {
		t.Fatal(err)
	}

	if value, err := pane.Format(ctx, "#{pane_pipe}"); err != nil || string(value) != "0" {
		t.Fatalf("pipe still active: %q, %v", value, err)
	}

	if err := pane.Submit(ctx, "after-stop"); err != nil {
		t.Fatal(err)
	}

	awaitPaneTitle(t, ctx, pane, "after-stop")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(data, []byte("after-stop")) {
		t.Fatalf("stopped pipe received output: %q", data)
	}
}

// TestIntegrationPipeInputDirection verifies that PipeOptions.Input connects the shell command's stdout
// into the pane as if typed.
func TestIntegrationPipeInputDirection(t *testing.T) {
	server, _, ctx := apiFixture(t)
	dir := t.TempDir()

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("failed to get server panes: %v", err)
	}
	pane := panes[0].Handle()
	targetFile := filepath.Join(dir, "input_received.txt")

	// Start cat > targetFile in the pane
	if err := pane.SendText(ctx, "cat > "+targetFile+"\n"); err != nil {
		t.Fatalf("SendText failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Pipe a script with Input: true. The script prints a test token.
	token := "TGO_PIPE_INPUT_TOKEN_12345"
	script := "echo " + token
	if err := pane.Pipe(ctx, script, tmux.PipeOptions{Input: true}); err != nil {
		t.Fatalf("pane.Pipe with Input:true failed: %v", err)
	}

	// Allow script to run and write to pane
	time.Sleep(300 * time.Millisecond)
	_ = pane.StopPipe(ctx)

	// Close cat in pane with Ctrl-D
	_ = pane.SendKeys(ctx, "C-d")

	awaitFile(t, ctx, targetFile)
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(token)) {
		t.Fatalf("expected %q in %s, got %q", token, targetFile, string(data))
	}
}

func awaitPaneTitle(t *testing.T, ctx context.Context, pane tmux.Pane, title string) {
	t.Helper()
	awaitObservation(t, ctx, "pane title "+title, func() bool {
		info, err := pane.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return info.Title == title
	})
}

func outputProducer(t *testing.T, ctx context.Context, session tmux.Session) (string, tmux.Pane) {
	t.Helper()
	dir := t.TempDir()
	script := `: > "$1/ready"
while IFS= read -r line; do
 printf 'OUTPUT:%s\n\033]2;%s\007' "$line" "$line"
done`

	var options tmux.NewWindowOptions

	options.Program = tmux.Exec("/bin/sh", "-c", script, "producer", dir)

	link, err := session.NewWindow(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	awaitFile(t, ctx, filepath.Join(dir, "ready"))

	return dir, pane
}
