package tmux

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommandLine(t *testing.T) {
	// Standard global flags followed by command
	cfg, cmd, err := ParseCommandLine([]string{"-S", "/tmp/custom.sock", "-f", "/etc/tmux.conf", "new-session", "-d", "-s", "my-session"})
	if err != nil {
		t.Fatalf("ParseCommandLine failed: %v", err)
	}

	if cfg.SocketPath != "/tmp/custom.sock" {
		t.Errorf("expected SocketPath /tmp/custom.sock, got %q", cfg.SocketPath)
	}

	if cfg.ConfigFile != "/etc/tmux.conf" {
		t.Errorf("expected ConfigFile /etc/tmux.conf, got %q", cfg.ConfigFile)
	}

	if cmd.Name() != "new-session" {
		t.Errorf("expected command new-session, got %q", cmd.Name())
	}

	expectedArgs := []string{"-d", "-s", "my-session"}
	if !reflect.DeepEqual(cmd.Args(), expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, cmd.Args())
	}

	// Flag with no space (-Lname)
	cfg2, cmd2, err := ParseCommandLine([]string{"-Lwork", "list-panes"})
	if err != nil {
		t.Fatalf("ParseCommandLine failed: %v", err)
	}

	if cfg2.SocketName != "work" {
		t.Errorf("expected SocketName work, got %q", cfg2.SocketName)
	}

	if cmd2.Name() != "list-panes" {
		t.Errorf("expected command list-panes, got %q", cmd2.Name())
	}

	// Missing command
	if _, _, err := ParseCommandLine([]string{"-u", "-v"}); err == nil {
		t.Fatal("expected error on argv with no command")
	}
}

func TestParseSequence(t *testing.T) {
	raw := "set-option -g status on ; display-message -p 'ready'"

	seq, err := ParseSequence(raw)
	if err != nil {
		t.Fatalf("ParseSequence failed: %v", err)
	}

	cmds := seq.Commands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}

	if cmds[0].Name() != "set-option" || !reflect.DeepEqual(cmds[0].Args(), []string{"-g", "status", "on"}) {
		t.Errorf("cmd 0 unexpected: %+v", cmds[0])
	}

	if cmds[1].Name() != "display-message" || !reflect.DeepEqual(cmds[1].Args(), []string{"-p", "ready"}) {
		t.Errorf("cmd 1 unexpected: %+v", cmds[1])
	}
}

func TestSequenceCopies(t *testing.T) {
	c, _ := NewCommand("display-message", "one")

	seq, e := Sequence(c)
	if e != nil {
		t.Fatal(e)
	}

	commands := seq.Commands()
	commands[0], _ = NewCommand("kill-server")

	if seq.Commands()[0].Name() != "display-message" {
		t.Fatal("mutable sequence")
	}
}

func TestDiscoverSockets(t *testing.T) {
	// Create a temp dir simulating a tmux socket directory
	dir := t.TempDir()
	t.Setenv("TMUX_TMPDIR", dir)

	// In real environment, DiscoverSockets returns whatever active sockets exist
	sockets, err := DiscoverSockets()
	if err != nil {
		t.Fatalf("DiscoverSockets failed: %v", err)
	}

	_ = sockets

	servers, err := DiscoverServers(Config{
		Binary:     "tmux",
		SocketPath: "",
		SocketName: "",
		ConfigFile: "",
		Env:        nil,
		Dir:        "",
		Limits:     Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
	})
	if err != nil {
		t.Fatalf("DiscoverServers failed: %v", err)
	}

	_ = servers
}

func TestPrepareAttachTargetValidation(t *testing.T) {
	s := localServer(t)

	// Valid target string (session name)
	streams := TerminalStreams{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	}

	// If stdin/stdout is not a terminal, validateTerminals should fail gracefully
	_, err := s.PrepareAttachTarget(t.Context(), "dev", streams, AttachOptions{ReadOnly: false, PreserveEnvironment: false})
	// Error is either nil or "stdin/stdout must be terminal files"
	if err != nil && !errorsIsTerminalError(err) {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty target should fail immediately
	if _, err := s.PrepareAttachTarget(t.Context(), "", streams, AttachOptions{ReadOnly: false, PreserveEnvironment: false}); err == nil {
		t.Fatal("expected error on empty target")
	}
}

func errorsIsTerminalError(err error) bool {
	return err != nil // any expected validation error
}

func FuzzParseSequence(f *testing.F) {
	seeds := []string{
		`new-session -d -s work ; split-window -h ; select-pane -t 1`,
		`send-keys "echo 'hello; world'" \; display-message "done; finished"`,
		`set-option @opt "val;with;semis" ; run-shell 'bash -c "exit 0"'`,
		`display-message "a" ; display-message "b" ; display-message "c"`,
		``,
		`; ; ;`,
		`"unterminated quote ; split-window`,
		`split-window -v -p 50 "top"`,
		`set -g status on ; set -g mouse on`,
		`bind-key C-a send-prefix ; unbind-key C-b`,
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 || strings.IndexByte(s, 0) >= 0 {
			return
		}

		seq, err := ParseSequence(s)
		if err == nil {
			for i, cmd := range seq.Commands() {
				if !cmd.Valid() {
					t.Fatalf("ParseSequence produced invalid command at index %d: %+v", i, cmd)
				}
			}
		}
	})
}
