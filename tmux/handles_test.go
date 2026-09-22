package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/gotmux/internal/wire"
)

func mockScriptServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	binary, response, log := filepath.Join(dir, "tmux"), filepath.Join(dir, "response"), filepath.Join(dir, "argv")

	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > '%s'\nexec /bin/cat '%s'\n", log, response)
	if err := os.WriteFile(binary, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(response, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := New(testConfig(binary, filepath.Join(dir, "socket")))
	if err != nil {
		t.Fatal(err)
	}

	return s, response, log
}

func testConfig(binary, socketPath string) Config {
	return Config{
		Binary:           binary,
		SocketPath:       socketPath,
		SocketName:       "",
		ConfigFile:       "",
		Env:              nil,
		Dir:              "",
		Limits:           Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
		UTF8:             UTF8Default,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         0,
		LoginShell:       false,
	}
}

func writeMockResponse(t *testing.T, name string, data []byte) {
	t.Helper()

	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func encodeTestPaneRecord(s *Server, id string) []byte {
	fields := fieldsFor(PaneKind)

	m := make(map[string]string, len(fields))
	for _, f := range fields {
		m[f] = "0"
	}

	for _, f := range []string{"pane_title", "pane_current_path", "pane_current_command", "pane_tty", "pane_dead_status", "pane_mode", "selection_present"} {
		m[f] = ""
	}

	maps.Copy(m, map[string]string{
		"pid": "42", "start_time": "100", "socket_path": s.endpoint.String(), "version": "3.6",
		"pane_id": id, "window_id": "@2", "session_id": "$0", "session_name": "test", "window_name": "win",
		"pane_active": "1", "pane_width": "80", "pane_height": "24",
	})

	values := make([]string, len(fields))
	for i, f := range fields {
		values[i] = m[f]
	}

	return wire.EncodeRecord(values)
}

func mockServerIdentity(s *Server) ServerIdentity {
	return ServerIdentity{
		Endpoint:       s.endpoint,
		ReportedSocket: s.endpoint.String(),
		PID:            42,
		Started:        time.Unix(100, 0),
		Generation:     0,
	}
}

func TestUnprobedHandleInfo(t *testing.T) {
	s, response, _ := mockScriptServer(t)
	bound := Pane{h: s.newHandle("%7", PaneKind, mockServerIdentity(s))}
	writeMockResponse(t, response, append([]byte(guardOK), encodeTestPaneRecord(s, "%7")...))

	if _, err := bound.Info(context.Background()); err != nil {
		t.Fatalf("bound control fixture: %v", err)
	}

	p, err := s.PaneHandle("%7")
	if err != nil {
		t.Fatal(err)
	}

	writeMockResponse(t, response, encodeTestPaneRecord(s, "%7"))

	if _, err = p.Info(context.Background()); err != nil {
		t.Fatalf("valid unprobed handle Info failed: %v (ErrServerChanged=%t)", err, errors.Is(err, ErrServerChanged))
	}
}

func TestUnprobedHandleSplit(t *testing.T) {
	s, response, log := mockScriptServer(t)

	p, err := s.PaneHandle("%7")
	if err != nil {
		t.Fatal(err)
	}

	writeMockResponse(t, response, encodeTestPaneRecord(s, "%8"))

	child, err := p.Split(context.Background(), SplitOptions{
		Direction:           Vertical,
		Size:                SplitSize{Cells: 0, Percent: 0},
		Dir:                 "",
		Program:             Program{kind: 0, name: "", args: nil},
		Env:                 nil,
		TmuxEnv:             nil,
		Select:              false,
		Before:              false,
		FullSize:            false,
		KillTarget:          false,
		Zoom:                false,
		Title:               "",
		BorderLines:         "",
		Style:               "",
		ActiveBorderStyle:   "",
		InactiveBorderStyle: "",
		Message:             "",
	})

	args, _ := os.ReadFile(log)
	if !bytes.Contains(args, []byte("split-window")) {
		t.Fatalf("mutation was not dispatched: %q", args)
	}

	if err != nil {
		t.Fatalf("split dispatched and valid creation reply supplied, but returned error: %v (ErrServerChanged=%t)", err, errors.Is(err, ErrServerChanged))
	}

	if child.ID() != "%8" {
		t.Fatalf("child=%q", child.ID())
	}
}

func TestClientHandle(t *testing.T) {
	s, _, _ := mockScriptServer(t)

	c, err := s.ClientHandle("/dev/pts/1")
	if err != nil {
		t.Fatalf("expected valid ClientHandle, got: %v", err)
	}

	if c.Name() != "/dev/pts/1" {
		t.Fatalf("expected /dev/pts/1, got %q", c.Name())
	}

	if _, err := s.ClientHandle(""); err == nil {
		t.Fatal("expected error on empty client name")
	}

	if _, err := s.ClientHandle("/dev/pts/\x00"); err == nil {
		t.Fatal("expected error on client name with NUL")
	}

	var nilServer *Server
	if _, err := nilServer.ClientHandle("/dev/pts/1"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("expected ErrInvalidHandle for nil server, got: %v", err)
	}
}

func TestWindowSelect(t *testing.T) {
	s, response, log := mockScriptServer(t)

	w, err := s.WindowHandle("@1")
	if err != nil {
		t.Fatal(err)
	}

	writeMockResponse(t, response, []byte("\n"))

	if err := w.Select(context.Background()); err != nil {
		t.Fatalf("Window.Select failed: %v", err)
	}

	args, _ := os.ReadFile(log)
	if !bytes.Contains(args, []byte("select-window")) {
		t.Fatalf("expected select-window in dispatched args: %q", args)
	}
}

func TestBufferHelpers(t *testing.T) {
	s, response, log := mockScriptServer(t)

	if err := s.SetBuffer(context.Background(), "", []byte("hello")); err == nil {
		t.Fatal("expected error on empty buffer name")
	}

	if err := s.SetBuffer(context.Background(), "b1", nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported on zero-length replacement, got: %v", err)
	}

	p, err := s.PaneHandle("%1")
	if err != nil {
		t.Fatal(err)
	}

	writeMockResponse(t, response, []byte("\n"))

	if err := p.Paste(context.Background()); err != nil {
		t.Fatalf("Pane.Paste failed: %v", err)
	}

	args, _ := os.ReadFile(log)
	if !bytes.Contains(args, []byte("paste-buffer")) {
		t.Fatalf("expected paste-buffer in dispatched args: %q", args)
	}
}
