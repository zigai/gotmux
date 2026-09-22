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

	options.Delete = true
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

// TestIntegrationLoadBufferStdinAndFlags verifies that raw RunWith executing "load-buffer -"
// with flags like -b and -w loads arbitrary binary data including NUL bytes into a paste buffer,
// while preserving WriteBuffer's documented replacement contract (rejecting zero-length buffer data).
func TestIntegrationLoadBufferStdinAndFlags(t *testing.T) {
	server, _, ctx := apiFixture(t)

	// 1. Raw RunWith loading arbitrary binary data including NUL bytes via stdin
	wantBytes := []byte("binary\x00data\x01\x02\xff\xfe\r\n\x00trailing")
	b1, err := tmux.NamedBuffer("r3-bin-buf")
	if err != nil {
		t.Fatalf("NamedBuffer failed: %v", err)
	}

	loadCmd, err := tmux.NewCommand("load-buffer", "-b", "r3-bin-buf", "-")
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}

	if _, err := server.RunWith(ctx, loadCmd, tmux.RunOptions{Input: wantBytes}); err != nil {
		t.Fatalf("RunWith load-buffer - failed: %v", err)
	}

	gotBytes, err := server.ReadBuffer(ctx, b1)
	if err != nil {
		t.Fatalf("ReadBuffer failed: %v", err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("ReadBuffer bytes mismatch: got %q, want %q", gotBytes, wantBytes)
	}

	// 2. Raw RunWith with -w flag (sending to clipboard)
	b2, err := tmux.NamedBuffer("r3-clip-buf")
	if err != nil {
		t.Fatalf("NamedBuffer failed: %v", err)
	}

	loadClipCmd, err := tmux.NewCommand("load-buffer", "-w", "-b", "r3-clip-buf", "-")
	if err != nil {
		t.Fatalf("NewCommand with -w failed: %v", err)
	}

	if _, err := server.RunWith(ctx, loadClipCmd, tmux.RunOptions{Input: wantBytes}); err != nil {
		t.Fatalf("RunWith load-buffer -w failed: %v", err)
	}

	gotClipBytes, err := server.ReadBuffer(ctx, b2)
	if err != nil {
		t.Fatalf("ReadBuffer failed: %v", err)
	}
	if !bytes.Equal(gotClipBytes, wantBytes) {
		t.Fatalf("ReadBuffer -w bytes mismatch: got %q, want %q", gotClipBytes, wantBytes)
	}

	// 3. Verify WriteBuffer's documented contract: zero-length data is rejected with ErrUnsupported
	// and outcome is NotSent (do not change empty WriteBuffer into a misleading success).
	bEmpty, err := tmux.NamedBuffer("r3-empty-buf")
	if err != nil {
		t.Fatalf("NamedBuffer failed: %v", err)
	}

	err = server.WriteBuffer(ctx, bEmpty, nil)
	if !errors.Is(err, tmux.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for WriteBuffer with nil data, got %v", err)
	}

	err = server.WriteBuffer(ctx, bEmpty, []byte{})
	if !errors.Is(err, tmux.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for WriteBuffer with empty data, got %v", err)
	}
}

func TestIntegrationBufferRenameAndPaste(t *testing.T) {
	server, session, ctx := apiFixture(t)
	panes, err := session.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("failed to query panes for session: %v", err)
	}
	pane := panes[0]

	// 1. RenameBuffer
	bufOriginal := "test-buf-original"
	bufRenamed := "test-buf-renamed"
	if err := server.SetBufferWith(ctx, bufOriginal, []byte("rename test content"), tmux.SetBufferOptions{Append: false}); err != nil {
		t.Fatalf("SetBufferWith failed: %v", err)
	}

	if err := server.RenameBuffer(ctx, bufOriginal, bufRenamed); err != nil {
		t.Fatalf("RenameBuffer failed: %v", err)
	}

	bRenamed, err := tmux.NamedBuffer(bufRenamed)
	if err != nil {
		t.Fatalf("NamedBuffer failed: %v", err)
	}
	data, err := server.ReadBuffer(ctx, bRenamed)
	if err != nil || string(data) != "rename test content" {
		t.Fatalf("unexpected content after RenameBuffer: %q, err: %v", data, err)
	}

	bOrig, _ := tmux.NamedBuffer(bufOriginal)
	if _, err := server.ReadBuffer(ctx, bOrig); err == nil {
		t.Fatal("original buffer should not exist after rename")
	}

	// 2. SetBufferWith with Append: true
	bufAppend := "test-buf-append"
	if err := server.SetBufferWith(ctx, bufAppend, []byte("part1"), tmux.SetBufferOptions{Append: false}); err != nil {
		t.Fatalf("SetBufferWith initial failed: %v", err)
	}
	if err := server.SetBufferWith(ctx, bufAppend, []byte("part2"), tmux.SetBufferOptions{Append: true}); err != nil {
		t.Fatalf("SetBufferWith append failed: %v", err)
	}
	bApp, _ := tmux.NamedBuffer(bufAppend)
	appData, err := server.ReadBuffer(ctx, bApp)
	if err != nil || string(appData) != "part1part2" {
		t.Fatalf("unexpected content after append: %q, err: %v", appData, err)
	}

	// 3. PasteWith with Delete: true
	bufPaste := "test-buf-paste"
	if err := server.SetBufferWith(ctx, bufPaste, []byte("echo paste_success\n"), tmux.SetBufferOptions{Append: false}); err != nil {
		t.Fatalf("SetBufferWith for paste failed: %v", err)
	}

	if err := pane.Handle().PasteWith(ctx, tmux.PasteOptions{
		Buffer:         bufPaste,
		Delete:         true,
		BracketedPaste: false,
		StripNewlines:  false,
		Separator:      "",
	}); err != nil {
		t.Fatalf("PasteWith failed: %v", err)
	}

	// Verify buffer was deleted after paste (-d)
	bPast, _ := tmux.NamedBuffer(bufPaste)
	if _, err := server.ReadBuffer(ctx, bPast); err == nil {
		t.Fatal("buffer should have been deleted after paste with Delete: true")
	}
}
