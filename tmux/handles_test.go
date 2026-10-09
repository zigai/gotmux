package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// dispatchedCommand returns the stand-in's argv after the socket flags.
func dispatchedCommand(t *testing.T, s *Server, log string) []string {
	t.Helper()

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")

	i := slices.Index(args, s.Endpoint().SocketPath)
	if i < 0 {
		t.Fatalf("socket path missing from argv %q", args)
	}

	return args[i+1:]
}

func writeMockResponse(t *testing.T, name string, data []byte) {
	t.Helper()

	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func encodeTestPaneRecord(s *Server, id string) []byte {
	fields := fieldsFor(ObjectKindPane)

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
	bound := Pane{h: s.newHandle("%7", ObjectKindPane, mockServerIdentity(s))}
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

func TestUnprobedPaneWindowKeepsVerifiedOrigin(t *testing.T) {
	server, response, _ := mockScriptServer(t)

	pane, err := server.PaneHandle("%7")
	if err != nil {
		t.Fatal(err)
	}

	writeMockResponse(t, response, encodeTestPaneRecord(server, "%7"))

	window, err := pane.Window(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if window.ID() != "@2" || !window.ServerIdentity().Equal(mockServerIdentity(server)) {
		t.Fatalf("parent window lost verified identity: %+v", window.ServerIdentity())
	}
}

func TestUnprobedHandleEqualityUsesEndpoint(t *testing.T) {
	server := localServer(t)

	other, err := New(testConfig("/bin/echo", server.Endpoint().SocketPath+"-other"))
	if err != nil {
		t.Fatal(err)
	}

	pane, err := server.PaneHandle("%7")
	if err != nil {
		t.Fatal(err)
	}

	same, err := server.PaneHandle("%7")
	if err != nil {
		t.Fatal(err)
	}

	different, err := other.PaneHandle("%7")
	if err != nil {
		t.Fatal(err)
	}

	if !pane.Equal(same) || pane.Equal(different) {
		t.Fatal("unprobed handle equality ignored endpoint")
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

	if got, want := dispatchedCommand(t, s, log), []string{"select-window", "-t", "@1"}; !slices.Equal(got, want) {
		t.Fatalf("dispatched %q, want %q", got, want)
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

	if got, want := dispatchedCommand(t, s, log), []string{"paste-buffer", "-t", "%1"}; !slices.Equal(got, want) {
		t.Fatalf("dispatched %q, want %q", got, want)
	}
}

func TestEqualRequiresEveryIdentityField(t *testing.T) {
	s := localServer(t)
	base := mockServerIdentity(s)
	other := base
	other.Endpoint.SocketPath += "-other"

	cases := []struct {
		name   string
		change func(ServerIdentity) ServerIdentity
		equal  bool
	}{
		{"identical", func(i ServerIdentity) ServerIdentity { return i }, true},
		{"same start instant in another location", func(i ServerIdentity) ServerIdentity { i.Started = i.Started.In(time.FixedZone("x", 3600)); return i }, true},
		{"endpoint", func(i ServerIdentity) ServerIdentity { i.Endpoint = other.Endpoint; return i }, false},
		{"reported socket", func(i ServerIdentity) ServerIdentity { i.ReportedSocket += "-other"; return i }, false},
		{"pid", func(i ServerIdentity) ServerIdentity { i.PID++; return i }, false},
		{"start time", func(i ServerIdentity) ServerIdentity { i.Started = i.Started.Add(time.Second); return i }, false},
		{"control generation", func(i ServerIdentity) ServerIdentity { i.Generation++; return i }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changed := tc.change(base)
			if got := base.Equal(changed); got != tc.equal {
				t.Fatalf("ServerIdentity.Equal = %v, want %v", got, tc.equal)
			}

			a := Pane{h: s.newHandle("%7", ObjectKindPane, base)}
			if got := a.Equal(Pane{h: s.newHandle("%7", ObjectKindPane, changed)}); got != tc.equal {
				t.Fatalf("Pane.Equal = %v, want %v", got, tc.equal)
			}
		})
	}
}

func TestPaneMethodsRejectInvalidHandle(t *testing.T) {
	ctx := t.Context()

	var (
		invalidPane Pane
		invalidWin  Window
	)

	if err := invalidPane.ClockMode(ctx); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidPane.ClockMode, got: %v", err)
	}

	if err := invalidPane.SendPrefix(ctx); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidPane.SendPrefix, got: %v", err)
	}

	defaultNewPaneOpts := NewPaneOptions{
		Width:               "",
		Height:              "",
		X:                   "",
		Y:                   "",
		Modal:               false,
		Dir:                 "",
		Program:             Program{kind: 0, name: "", args: nil},
		Env:                 nil,
		TmuxEnv:             nil,
		Select:              false,
		Zoom:                false,
		Title:               "",
		BorderLines:         "",
		Style:               "",
		ActiveBorderStyle:   "",
		InactiveBorderStyle: "",
		FloatOverZoom:       false,
		CloseOnClick:        false,
		CaptureAllKeys:      false,
	}

	if _, err := invalidPane.NewPane(ctx, defaultNewPaneOpts); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidPane.NewPane, got: %v", err)
	}

	if _, err := invalidWin.NewPane(ctx, defaultNewPaneOpts); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidWin.NewPane, got: %v", err)
	}
}

func TestOperationErrorNamesCalledMethod(t *testing.T) {
	ctx := t.Context()

	var (
		pane    Pane
		window  Window
		session Session
		client  Client
	)

	noQuery := QueryOptions{Filter: "", ExtraFields: nil}

	tests := []struct {
		want string
		call func() error
	}{
		{"Pane.Kill", func() error { return pane.Kill(ctx) }},
		{"Window.Kill", func() error { return window.Kill(ctx) }},
		{"Session.Rename", func() error { return session.Rename(ctx, "renamed") }},
		{"Pane.Info", func() error { _, err := pane.InfoWith(ctx, noQuery); return err }},
		{"Client.Info", func() error { _, err := client.Info(ctx); return err }},
		{"SessionOptions.SetBaseIndex", func() error { return session.Options().SetBaseIndex(ctx, 1) }},
		{"SessionOptions.SetHistoryLimit", func() error { return session.Options().SetHistoryLimit(ctx, -1) }},
		{"EnvironmentScope.Unset", func() error { return session.Environment().Unset(ctx, "invalid=name") }},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			err := tt.call()

			opErr, ok := errors.AsType[*OperationError](err)
			if !ok {
				t.Fatalf("got %v, want *OperationError", err)
			}

			if opErr.Operation != tt.want {
				t.Errorf("Operation = %q, want %q", opErr.Operation, tt.want)
			}

			if msg := err.Error(); strings.Contains(msg, tt.want+": "+tt.want) {
				t.Errorf("error repeats the operation: %q", msg)
			}
		})
	}
}
