package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(response, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Binary: binary, SocketPath: filepath.Join(dir, "socket")})
	if err != nil {
		t.Fatal(err)
	}
	return s, response, log
}

func writeMockResponse(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0600); err != nil {
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
	for k, v := range map[string]string{
		"pid": "42", "start_time": "100", "socket_path": s.endpoint.String(), "version": "3.6",
		"pane_id": id, "window_id": "@2", "session_id": "$0", "session_name": "test", "window_name": "win",
		"pane_active": "1", "pane_width": "80", "pane_height": "24",
	} {
		m[k] = v
	}
	values := make([]string, len(fields))
	for i, f := range fields {
		values[i] = m[f]
	}
	return wire.EncodeRecord(values)
}

func mockServerIdentity(s *Server) ServerIdentity {
	return ServerIdentity{Endpoint: s.endpoint, ReportedSocket: s.endpoint.String(), PID: 42, Started: time.Unix(100, 0)}
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
	child, err := p.Split(context.Background(), SplitOptions{})
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
