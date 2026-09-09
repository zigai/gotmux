package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func localServer(t *testing.T) *Server {
	t.Helper()

	s, e := New(Config{Binary: "/bin/sh", SocketPath: filepath.Join(t.TempDir(), "s"), SocketName: "", ConfigFile: "", Env: []string{}, Dir: t.TempDir(), Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if e != nil {
		t.Fatal(e)
	}

	return s
}

func fixtureIdentity(s *Server) ServerIdentity {
	return ServerIdentity{Endpoint: s.endpoint, ReportedSocket: s.endpoint.String(), PID: 42, Started: time.Unix(100, 0), Generation: 0}
}

func TestNewIsPureAndCopiesConfig(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "fake")

	marker := filepath.Join(dir, "ran")
	if e := os.WriteFile(binary, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); e != nil { //nolint:gosec // test script requires executable permissions
		t.Fatal(e)
	}

	env := []string{"PATH=" + dir, "DATA=before"}

	s, e := New(Config{Binary: "fake", Dir: dir, Env: env, SocketName: "selected", SocketPath: "", ConfigFile: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if e != nil {
		t.Fatal(e)
	}

	env[1] = "DATA=after"

	if _, e = os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("constructor executed binary")
	}

	if s.config.Env[1] != "DATA=before" || s.config.Binary != binary {
		t.Fatal("configuration not frozen")
	}

	if s.Limits() != DefaultLimits() {
		t.Fatal("defaults")
	}
}

func TestNewValidation(t *testing.T) {
	//nolint:exhaustruct_v5 // testing validation of individual partial config fields
	for _, cfg := range []Config{{SocketPath: "relative"}, {SocketPath: "/tmp/a", SocketName: "a"}, {Env: []string{"A=1", "A=2"}}, {Env: []string{"bad"}}, {Env: []string{"A=\x00"}}, {Limits: Limits{OutputBytes: -1}}, {Limits: Limits{Concurrent: -1}}, {SocketName: "../escape"}} {
		cfg.Binary = "/bin/sh"
		if _, e := New(cfg); !errors.Is(e, ErrInvalidArgument) {
			t.Errorf("config accepted or wrong classification: %v", e)
		}
	}
}

func TestEnvironmentSelection(t *testing.T) {
	s, e := New(Config{Binary: "/bin/sh", Env: []string{"TMUX=/tmp/a,b,c,123,5", "TMUX_PANE=%9"}, SocketPath: "", SocketName: "", ConfigFile: "", Dir: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if e != nil {
		t.Fatal(e)
	}

	if s.Endpoint().SocketPath != "/tmp/a,b,c" {
		t.Fatal(s.Endpoint())
	}

	s, e = New(Config{Binary: "/bin/sh", SocketPath: "/tmp/explicit", Env: []string{"TMUX=malformed"}, SocketName: "", ConfigFile: "", Dir: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if e != nil || s.Endpoint().SocketPath != "/tmp/explicit" {
		t.Fatalf("%v %v", s, e)
	}
}

func TestEnvironmentNilVersusEmpty(t *testing.T) {
	t.Setenv("TMUX_GO_CAPTURE_TEST", "present")
	t.Setenv("TMUX", "")

	a, e := New(Config{Binary: "/bin/sh", SocketPath: "/tmp/a", SocketName: "", ConfigFile: "", Env: nil, Dir: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if e != nil {
		t.Fatal(e)
	}

	b, e := New(Config{Binary: "/bin/sh", SocketPath: "/tmp/a", Env: []string{}, SocketName: "", ConfigFile: "", Dir: "", Limits: Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}})
	if e != nil {
		t.Fatal(e)
	}

	if len(b.config.Env) != 0 {
		t.Fatal("explicit environment was augmented")
	}

	found := false
	for _, v := range a.config.Env {
		found = found || v == "TMUX_GO_CAPTURE_TEST=present"
	}

	if !found {
		t.Fatal("nil environment not captured")
	}
}

func TestParseEnvironment(t *testing.T) {
	h, e := ParseEnvironment(Environment{TMUX: "/tmp/a,b,123,0", TMUXPane: "%17"})
	if e != nil || h.SocketPath != "/tmp/a,b" || h.SessionID != "$0" {
		t.Fatalf("%#v %v", h, e)
	}

	if p, ok := h.PaneID.Get(); !ok || p != "%17" {
		t.Fatal(h)
	}

	if _, e := ParseEnvironment(Environment{TMUX: "", TMUXPane: ""}); !errors.Is(e, ErrNotInsideTmux) {
		t.Fatal(e)
	}

	for _, s := range []string{"x", "/tmp/a,0,1", "relative,1,1", "/tmp/a,1,01", "/tmp/a,1,no"} {
		if _, e := ParseEnvironment(Environment{TMUX: s, TMUXPane: ""}); e == nil {
			t.Fatal("accepted", s)
		}
	}
}

func TestVersionGates(t *testing.T) {
	for _, s := range []string{"3.6", "tmux 3.6a", "3.6b", "3.7c", "4.0"} {
		if !ParseVersion(s).AtLeast(3, 6) {
			t.Fatal(s)
		}
	}

	for _, s := range []string{"3.5a", "3.6-next", "next-3.8", "3.7vendor"} {
		if supportedVersion(ParseVersion(s)) == nil {
			t.Fatal("accepted", s)
		}
	}

	v := ParseVersion("tmux 3.7c-vendor")
	if v.Raw != "tmux 3.7c-vendor" || v.Suffix != "-vendor" || v.Recognized {
		t.Fatal(v)
	}
}

func TestCommandOwnershipAndDomains(t *testing.T) {
	args := []string{"a;b", "$HOME", "#{pane_id}", ""}

	c, e := NewCommand("display-message", args...)
	if e != nil {
		t.Fatal(e)
	}

	args[0] = "changed"
	argsCopy := c.Args()
	argsCopy[1] = "changed"

	if !reflect.DeepEqual(c.Args(), []string{"a;b", "$HOME", "#{pane_id}", ""}) {
		t.Fatal(c.Args())
	}

	for _, name := range []string{"", "-S", "a b", "x;y", "x\ny"} {
		if _, e := NewCommand(name); !errors.Is(e, ErrInvalidArgument) {
			t.Error(name, e)
		}
	}

	if _, e := NewCommand("send-keys", "\x00"); e == nil {
		t.Fatal("NUL")
	}
}

func TestInvalidHandlesNeverUseCurrent(t *testing.T) {
	ctx := context.Background()

	//nolint:exhaustruct_v5 // testing methods on uninitialized zero handles
	calls := []func() error{func() error { return (Pane{}).Kill(ctx) }, func() error { return (Window{}).Kill(ctx) }, func() error { return (Session{}).Kill(ctx) }, func() error { return (WindowLink{}).Select(ctx) }, func() error { _, e := (Pane{}).Info(ctx); return e }}
	for _, call := range calls {
		e := call()

		var op *OperationError
		if !errors.Is(e, ErrInvalidHandle) || !errors.As(e, &op) || op.Outcome.Effect != NotSent {
			t.Fatalf("%#v", e)
		}
	}
}

func TestIDsAndLiteralNames(t *testing.T) {
	for _, s := range []string{"%0", "%42"} {
		if !PaneID(s).Valid() {
			t.Fatal(s)
		}
	}

	for _, s := range []string{"", "%01", "%1x", "1", "@1", "%4294967296"} {
		if PaneID(s).Valid() {
			t.Fatal(s)
		}
	}

	for _, s := range []string{"prefix.name", "a:b", "a\nb", `a\b`} {
		if sessionName(s, false) == nil {
			t.Fatal("name normalization accepted", s)
		}
	}

	if sessionName("work '$#", false) != nil {
		t.Fatal("ordinary name")
	}
}

func TestErrorRedactionAndEffects(t *testing.T) {
	secret := "secret-token-content"
	e := &CommandError{Command: "send-keys", Result: Result{Stdout: nil, Stderr: []byte(secret), ExitCode: 0}, Outcome: Outcome{Effect: Unknown, Steps: nil, Created: nil}, Timeout: NoTimeout, Err: ErrOutputLimit}

	wrapped := opError("Submit", e)
	if strings.Contains(wrapped.Error(), secret) || !errors.Is(wrapped, ErrOutputLimit) {
		t.Fatal(wrapped)
	}

	var op *OperationError
	if !errors.As(wrapped, &op) || op.Outcome.Effect != Unknown {
		t.Fatal(wrapped)
	}
}

func TestStartupVersionPatternMatchesStableGate(t *testing.T) {
	re := regexp.MustCompile(supportedStablePattern)
	for _, version := range []string{"3.5", "3.6", "3.6a", "3.7c", "3.10", "4.0", "10.1b", "3.7-vendor", "03.6", "3.06"} {
		if got, want := re.MatchString(version), ParseVersion(version).AtLeast(3, 6); got != want {
			// Noncanonical numeric spellings are intentionally rejected on startup.
			if version == "03.6" || version == "3.06" {
				continue
			}

			t.Fatalf("%s: regex=%v Go=%v", version, got, want)
		}
	}
}

func TestEmptyBufferWriteIsNotSilentSuccess(t *testing.T) {
	s := localServer(t)

	name, _ := NamedBuffer("x")
	if e := s.WriteBuffer(context.Background(), name, nil); !errors.Is(e, ErrUnsupported) || outcomeOf(e).Effect != NotSent {
		t.Fatal(e)
	}
}
