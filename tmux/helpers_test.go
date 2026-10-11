package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/gotmux/internal/wire"
)

func localServer(t *testing.T) *Server {
	t.Helper()

	s, e := New(Config{Binary: "/bin/sh", SocketPath: filepath.Join(t.TempDir(), "s"), SocketName: "", ConfigFile: "", Env: []string{}, Dir: t.TempDir(), Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}, UTF8: UTF8Default, Colors256: false, TerminalFeatures: nil, LogLevel: LogLevelNone, LoginShell: false})
	if e != nil {
		t.Fatal(e)
	}

	return s
}

func fixtureIdentity(s *Server) ServerIdentity {
	return ServerIdentity{Endpoint: s.endpoint, ReportedSocket: s.endpoint.String(), PID: 42, Started: time.Unix(100, 0), Generation: 0}
}

func fixtureIdentityRecord(s *Server) string {
	return string(wire.EncodeRecord([]string{"42", "100", s.endpoint.String(), "3.8"}))
}

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
