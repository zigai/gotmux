package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zigai/gotmux/internal/schema"
	"github.com/zigai/gotmux/internal/wire"
)

// standInServer returns a Server whose tmux binary is a /bin/sh script built by
// script from the directory that holds it, for marker and log files.
func standInServer(t *testing.T, limits Limits, script func(dir string) string) (*Server, string) {
	t.Helper()

	dir := t.TempDir()

	binary := filepath.Join(dir, "tmux")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+script(dir)), 0o700); err != nil { //nolint:gosec // stand-in tmux must be executable
		t.Fatal(err)
	}

	cfg := testConfig(binary, filepath.Join(dir, "socket"))
	cfg.Limits = limits

	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return s, dir
}

func unprobedPane(t *testing.T, s *Server) Pane {
	t.Helper()

	p, err := s.PaneHandle("%1")
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func limitsWith(timeout time.Duration, output, input int64, concurrent int) Limits {
	return Limits{CommandTimeout: timeout, OutputBytes: output, InputBytes: input, Concurrent: concurrent}
}

func assertOutcome(t *testing.T, err error, want Effect) {
	t.Helper()

	var op *OperationError
	if !errors.As(err, &op) {
		t.Fatalf("got %v, want an *OperationError", err)
	}

	if op.Outcome.Effect != want {
		t.Fatalf("effect %v, want %v (err %v)", op.Outcome.Effect, want, err)
	}
}

func timeoutSource(t *testing.T, err error) TimeoutSource {
	t.Helper()

	var cmd *CommandError
	if !errors.As(err, &cmd) {
		t.Fatalf("got %v, want a *CommandError", err)
	}

	return cmd.Timeout
}

func waitForFiles(t *testing.T, pattern string, n int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}

		if len(matches) >= n {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d files match %s, want %d", len(matches), pattern, n)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func TestZeroLimitsTakeDocumentedDefaults(t *testing.T) {
	s, _ := standInServer(t, limitsWith(0, 0, 0, 0), func(string) string { return "" })

	want := Limits{CommandTimeout: 5 * time.Second, OutputBytes: 4 << 20, InputBytes: 1 << 20, Concurrent: 8}
	if got := s.Limits(); got != want {
		t.Fatalf("Limits() = %+v, want %+v", got, want)
	}
}

func TestCommandTimeoutStopsTheCommand(t *testing.T) {
	sleeper := func(string) string { return "exec sleep 10\n" }

	t.Run("library timeout", func(t *testing.T) {
		s, _ := standInServer(t, limitsWith(100*time.Millisecond, 0, 0, 0), sleeper)

		start := time.Now()
		err := unprobedPane(t, s).Kill(context.Background())

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want context.DeadlineExceeded", err)
		}

		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("command ran for %v; CommandTimeout did not stop it", elapsed)
		}

		if got := timeoutSource(t, err); got != LibraryTimeout {
			t.Fatalf("timeout source %v, want LibraryTimeout", got)
		}

		assertOutcome(t, err, Unknown)
	})

	t.Run("caller deadline first", func(t *testing.T) {
		s, _ := standInServer(t, limitsWith(5*time.Second, 0, 0, 0), sleeper)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := unprobedPane(t, s).Kill(ctx)

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want context.DeadlineExceeded", err)
		}

		if got := timeoutSource(t, err); got != CallerTimeout {
			t.Fatalf("timeout source %v, want CallerTimeout", got)
		}

		assertOutcome(t, err, Unknown)
	})
}

func TestOutputLimitAfterStartIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, redirect string
	}{
		{"stdout", ""},
		{"stderr", " >&2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := standInServer(t, limitsWith(0, 1024, 0, 0), func(string) string {
				return "head -c 100000 /dev/zero" + tc.redirect + "\n"
			})

			err := unprobedPane(t, s).Kill(context.Background())
			if !errors.Is(err, ErrOutputLimit) {
				t.Fatalf("got %v, want ErrOutputLimit", err)
			}

			var cmd *CommandError
			if !errors.As(err, &cmd) || len(cmd.Result.Stdout) > 1024 || len(cmd.Result.Stderr) > 1024 {
				t.Fatalf("returned more than OutputBytes: %v", err)
			}

			assertOutcome(t, err, Unknown)
		})
	}
}

func TestOutputLimitIsSharedAcrossSteps(t *testing.T) {
	// The probe answers with an identity record; the guarded command then prints
	// 150 bytes. Each step fits OutputBytes alone, but together they exceed it.
	var probe []byte

	s, dir := standInServer(t, limitsWith(0, 0, 0, 0), func(dir string) string {
		return `case "$*" in
*kill-server*) head -c 150 /dev/zero | tr '\0' x ;;
*) cat '` + filepath.Join(dir, "probe") + `' ;;
esac
`
	})

	m := sessionFixture(s)
	values := make([]string, len(schema.Identity))

	for i, f := range schema.Identity {
		values[i] = m[f]
	}

	probe = wire.EncodeRecord(values)
	if err := os.WriteFile(filepath.Join(dir, "probe"), probe, 0o600); err != nil {
		t.Fatal(err)
	}

	limited, err := New(Config{
		Binary: s.config.Binary, SocketPath: s.endpoint.SocketPath, SocketName: "", ConfigFile: "", Env: nil, Dir: "",
		Limits: limitsWith(0, int64(len(probe))+100, 0, 0), UTF8: UTF8Default, Colors256: false, TerminalFeatures: nil, LogLevel: LogNone, LoginShell: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := limited.Kill(context.Background()); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("probe plus command output exceeded OutputBytes: got %v, want ErrOutputLimit", err)
	}
}

func TestInputLimitSendsNothing(t *testing.T) {
	s, dir := standInServer(t, limitsWith(0, 0, 64, 0), func(dir string) string {
		return "touch '" + filepath.Join(dir, "ran") + "'\n"
	})

	cmd, err := NewCommand("display-message", strings.Repeat("a", 128))
	if err != nil {
		t.Fatal(err)
	}

	small, err := NewCommand("display-message", "hello")
	if err != nil {
		t.Fatal(err)
	}

	seq, err := Sequence(small)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"oversized argument", func() error { _, e := s.RunWith(t.Context(), cmd, RunOptions{Start: AllowStart, Input: nil}); return e }},
		{"oversized stdin", func() error {
			_, e := s.RunWith(t.Context(), small, RunOptions{Start: AllowStart, Input: make([]byte, 128)})
			return e
		}},
		{"oversized sequence stdin", func() error {
			_, e := s.RunSequenceWith(t.Context(), seq, RunOptions{Start: AllowStart, Input: make([]byte, 128)})
			return e
		}},
		{"oversized pane text", func() error { return unprobedPane(t, s).SendText(t.Context(), strings.Repeat("a", 128)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ErrInputLimit) {
				t.Fatalf("got %v, want ErrInputLimit", err)
			}

			if outcomeOf(err).Effect != NotSent {
				t.Fatalf("effect %v, want NotSent", outcomeOf(err).Effect)
			}

			if _, err := os.Stat(filepath.Join(dir, "ran")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("tmux ran for an oversized request (stat: %v)", err)
			}
		})
	}
}

func TestConcurrentCapsRunningSubprocesses(t *testing.T) {
	const concurrent, callers = 2, 5

	s, dir := standInServer(t, limitsWith(10*time.Second, 0, 0, concurrent), func(dir string) string {
		return `touch '` + filepath.Join(dir, "running.") + `'$$
while [ ! -e '` + filepath.Join(dir, "gate") + `' ]; do sleep 0.01; done
`
	})
	p := unprobedPane(t, s)
	running := filepath.Join(dir, "running.*")

	var wg sync.WaitGroup

	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() { errs <- p.Kill(context.Background()) })
	}

	waitForFiles(t, running, concurrent)
	// Give processes beyond the cap time to start if admission were not enforced.
	time.Sleep(200 * time.Millisecond)

	if matches, _ := filepath.Glob(running); len(matches) != concurrent {
		t.Fatalf("%d subprocesses started while the gate was closed, want %d", len(matches), concurrent)
	}

	waiting, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := p.Kill(waiting)
	if !errors.Is(err, context.DeadlineExceeded) || outcomeOf(err).Effect != NotSent {
		t.Fatalf("caller waiting for admission: got %v (effect %v), want DeadlineExceeded and NotSent", err, outcomeOf(err).Effect)
	}

	if err := os.WriteFile(filepath.Join(dir, "gate"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if matches, _ := filepath.Glob(running); len(matches) != callers {
		t.Fatalf("%d subprocesses ran, want %d (the timed-out caller must not run)", len(matches), callers)
	}
}

func TestPreparedCommandOutlivesCommandTimeout(t *testing.T) {
	const commandTimeout = 100 * time.Millisecond

	s, dir := standInServer(t, limitsWith(commandTimeout, 0, 0, 0), func(dir string) string {
		return `touch '` + filepath.Join(dir, "started") + `'
while [ ! -e '` + filepath.Join(dir, "gate") + `' ]; do sleep 0.01; done
`
	})

	c, err := NewCommand("wait-for", "channel")
	if err != nil {
		t.Fatal(err)
	}

	cmd, err := s.PrepareCommand(t.Context(), c, Streams{}, CommandOptions{}) //nolint:exhaustruct_v5 // default streams and options
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	waitForFiles(t, filepath.Join(dir, "started"), 1)
	// Outlive CommandTimeout; if it applied, the process would be killed now.
	time.Sleep(3 * commandTimeout)

	if err := os.WriteFile(filepath.Join(dir, "gate"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("prepared command did not outlive CommandTimeout: %v", err)
	}
}

func TestDecodeFailureAfterCreationIsConfirmed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture func(*Server) map[string]string
		kind    ObjectKind
		field   string
		rawID   string
		create  func(*Server) error
	}{
		{"Split", paneFixture, PaneKind, "pane_width", "%7", func(s *Server) error {
			_, e := unprobedPane(t, s).Split(context.Background(), SplitOptions{}) //nolint:exhaustruct_v5 // default split
			return e
		}},
		{"NewWindow", windowFixture, WindowKind, "window_width", "@2", func(s *Server) error {
			session, e := s.SessionHandle("$0")
			if e != nil {
				return e
			}

			_, e = session.NewWindow(context.Background(), NewWindowOptions{}) //nolint:exhaustruct_v5 // default window

			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, response, _ := mockScriptServer(t)

			m := tc.fixture(s)
			m[tc.field] = "wide"

			fields := fieldsFor(tc.kind)
			values := make([]string, len(fields))

			for i, f := range fields {
				values[i] = m[f]
			}

			writeMockResponse(t, response, wire.EncodeRecord(values))

			err := tc.create(s)

			var op *OperationError
			if !errors.As(err, &op) || op.Outcome.Effect != Confirmed {
				t.Fatalf("got %v, want an OperationError with effect Confirmed", err)
			}

			if len(op.Outcome.Created) != 1 || op.Outcome.Created[0].Kind != tc.kind || op.Outcome.Created[0].RawID != tc.rawID {
				t.Fatalf("Created = %+v, want one %s %s", op.Outcome.Created, tc.kind, tc.rawID)
			}
		})
	}
}

func TestOperationErrorRedactsArgumentsAndOutput(t *testing.T) {
	const argMarker, errMarker = "ARG-MARKER-7f3a", "STDERR-MARKER-9c1e"

	s, _ := standInServer(t, limitsWith(0, 0, 0, 0), func(string) string {
		return "echo \"" + errMarker + " $*\" >&2\nexit 1\n"
	})

	err := unprobedPane(t, s).SendText(context.Background(), argMarker)
	if err == nil {
		t.Fatal("failing stand-in reported success")
	}

	if msg := err.Error(); strings.Contains(msg, argMarker) || strings.Contains(msg, errMarker) {
		t.Fatalf("error text leaks an argument or captured output: %q", msg)
	}

	var cmd *CommandError
	if !errors.As(err, &cmd) || !strings.Contains(string(cmd.Result.Stderr), errMarker) {
		t.Fatalf("captured stderr is no longer available through CommandError: %v", err)
	}
}

func TestStartRefusesBinaryOlderThan36(t *testing.T) {
	for _, tc := range []struct {
		version string
		refused bool
	}{
		{"tmux 3.5a", true},
		{"tmux 3.6", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			s, dir := standInServer(t, limitsWith(0, 0, 0, 0), func(dir string) string {
				return `case "$*" in
*-V*) echo '` + tc.version + `' ;;
*new-session*) touch '` + filepath.Join(dir, "started") + `' ;;
*) echo "no server running on ` + filepath.Join(dir, "socket") + `" >&2; exit 1 ;;
esac
`
			})

			var opts NewSessionOptions

			opts.Start = AllowStart
			_, err := s.NewSession(context.Background(), opts)

			_, statErr := os.Stat(filepath.Join(dir, "started"))
			started := statErr == nil

			if tc.refused {
				if !errors.Is(err, ErrUnsupported) || outcomeOf(err).Effect != NotSent {
					t.Fatalf("got %v (effect %v), want ErrUnsupported and NotSent", err, outcomeOf(err).Effect)
				}

				if started {
					t.Fatal("new-session ran with a tmux older than 3.6")
				}

				return
			}

			if !started {
				t.Fatalf("the stand-in never reached new-session with a supported version (err %v)", err)
			}
		})
	}
}

func TestNamesTmuxWouldAlterAreRejected(t *testing.T) {
	s, dir := standInServer(t, limitsWith(0, 0, 0, 0), func(dir string) string {
		return "touch '" + filepath.Join(dir, "ran") + "'\n"
	})

	session, err := s.SessionHandle("$0")
	if err != nil {
		t.Fatal(err)
	}

	window, err := s.WindowHandle("@1")
	if err != nil {
		t.Fatal(err)
	}

	pane := unprobedPane(t, s)
	altered := []string{`a\b`, "a\tb", "a\x01b", "a\x1bb", "a\x7fb", "a\nb", "a\xffb"}

	for _, tc := range []struct {
		entry string
		names []string
		call  func(name string) error
	}{
		{"NewSession name", append([]string{"a:b", "a.b"}, altered...), func(name string) error {
			var opts NewSessionOptions

			opts.Name = name
			_, e := s.NewSession(context.Background(), opts)

			return e
		}},
		{"Session.Rename", append([]string{"a:b", "a.b", ""}, altered...), func(name string) error {
			return session.Rename(context.Background(), name)
		}},
		{"NewSession window", altered, func(name string) error {
			var opts NewSessionOptions

			opts.Window = name
			_, e := s.NewSession(context.Background(), opts)

			return e
		}},
		{"NewWindow", altered, func(name string) error {
			var opts NewWindowOptions

			opts.Name = name
			_, e := session.NewWindow(context.Background(), opts)

			return e
		}},
		{"Window.Rename", altered, func(name string) error { return window.Rename(context.Background(), name) }},
		{"Pane.Break", altered, func(name string) error {
			var opts BreakOptions

			opts.Name = name
			_, e := pane.Break(context.Background(), session, opts)

			return e
		}},
	} {
		for _, name := range tc.names {
			t.Run(tc.entry+"/"+strconv.Quote(name), func(t *testing.T) {
				err := tc.call(name)
				if !errors.Is(err, ErrInvalidArgument) || outcomeOf(err).Effect != NotSent {
					t.Fatalf("got %v (effect %v), want ErrInvalidArgument and NotSent", err, outcomeOf(err).Effect)
				}

				if _, err := os.Stat(filepath.Join(dir, "ran")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("tmux ran for name %q", name)
				}
			})
		}
	}
}

func TestPopupDismissalIsNotAnError(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		wantErr      bool
	}{
		{"closed before its program ended", "exit 129\n", false},
		{"tmux diagnostic", "echo 'no current client' >&2\nexit 129\n", true},
		{"tmux failure", "echo 'invalid popup' >&2\nexit 1\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := standInServer(t, limitsWith(0, 0, 0, 0), func(string) string { return tc.script })

			client, err := s.ClientHandle("/dev/pts/1")
			if err != nil {
				t.Fatal(err)
			}

			var opts PopupOptions

			err = client.Popup(context.Background(), opts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Popup = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
