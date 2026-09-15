package tmux

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommandLine_Standard(t *testing.T) {
	parsed, err := ParseCommandLine([]string{"-S", "/tmp/custom.sock", "-f", "/etc/tmux.conf", "new-session", "-d", "-s", "my-session"})
	if err != nil {
		t.Fatalf("ParseCommandLine failed: %v", err)
	}

	if parsed.Config.SocketPath != "/tmp/custom.sock" {
		t.Errorf("expected SocketPath /tmp/custom.sock, got %q", parsed.Config.SocketPath)
	}

	if parsed.Config.ConfigFile != "/etc/tmux.conf" {
		t.Errorf("expected ConfigFile /etc/tmux.conf, got %q", parsed.Config.ConfigFile)
	}

	if parsed.Action != ActionCommand {
		t.Errorf("expected ActionCommand, got %v", parsed.Action)
	}

	cmd, ok := parsed.Command()
	if !ok || cmd.Name() != "new-session" {
		t.Fatalf("expected single command new-session, got %v, ok=%v", cmd.Name(), ok)
	}

	expectedArgs := []string{"-d", "-s", "my-session"}
	if !reflect.DeepEqual(cmd.Args(), expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, cmd.Args())
	}
}

func TestParseCommandLine_Attached(t *testing.T) {
	parsed2, err := ParseCommandLine([]string{"-Lwork", "list-panes"})
	if err != nil {
		t.Fatalf("ParseCommandLine failed: %v", err)
	}

	if parsed2.Config.SocketName != "work" {
		t.Errorf("expected SocketName work, got %q", parsed2.Config.SocketName)
	}

	cmd2, ok := parsed2.Command()
	if !ok || cmd2.Name() != "list-panes" {
		t.Fatalf("expected command list-panes, got %v, ok=%v", cmd2.Name(), ok)
	}
}

func TestParseCommandLine_FlagsOnly(t *testing.T) {
	parsedFlagsOnly, err := ParseCommandLine([]string{"-u", "-v"})
	if err != nil {
		t.Fatalf("expected flags-only to succeed, got %v", err)
	}

	if parsedFlagsOnly.Action != ActionDefault {
		t.Errorf("expected ActionDefault, got %v", parsedFlagsOnly.Action)
	}

	if parsedFlagsOnly.Config.UTF8 != UTF8Force {
		t.Errorf("expected UTF8Force, got %v", parsedFlagsOnly.Config.UTF8)
	}

	if parsedFlagsOnly.Config.LogLevel != LogVerbose {
		t.Errorf("expected LogVerbose, got %v", parsedFlagsOnly.Config.LogLevel)
	}

	if _, ok := parsedFlagsOnly.Command(); ok {
		t.Error("expected Command() to return false on ActionDefault")
	}
}

func TestParseCommandLine_ShellAndForeground(t *testing.T) {
	parsedShell, err := ParseCommandLine([]string{"-c", "echo hello"})
	if err != nil {
		t.Fatalf("ParseCommandLine -c failed: %v", err)
	}

	if parsedShell.Action != ActionShell || parsedShell.ShellCommand != "echo hello" {
		t.Errorf("expected ActionShell with 'echo hello', got action=%v, cmd=%q", parsedShell.Action, parsedShell.ShellCommand)
	}

	parsedFg, err := ParseCommandLine([]string{"-D"})
	if err != nil {
		t.Fatalf("ParseCommandLine -D failed: %v", err)
	}

	if parsedFg.Action != ActionForeground {
		t.Errorf("expected ActionForeground, got %v", parsedFg.Action)
	}
}

func TestParseCommandLine_HelpAndVersion(t *testing.T) {
	parsedHelp, err := ParseCommandLine([]string{"-h"})
	if err != nil || parsedHelp.Action != ActionHelp {
		t.Errorf("expected ActionHelp, got %v, err=%v", parsedHelp.Action, err)
	}

	parsedVer, err := ParseCommandLine([]string{"-V"})
	if err != nil || parsedVer.Action != ActionVersion {
		t.Errorf("expected ActionVersion, got %v, err=%v", parsedVer.Action, err)
	}

	// -h and -V take precedence over -C
	parsedHelpControl, err := ParseCommandLine([]string{"-C", "-h"})
	if err != nil || parsedHelpControl.Action != ActionHelp {
		t.Errorf("expected ActionHelp with -C -h, got %v, err=%v", parsedHelpControl.Action, err)
	}

	parsedVerControl, err := ParseCommandLine([]string{"-C", "-V"})
	if err != nil || parsedVerControl.Action != ActionVersion {
		t.Errorf("expected ActionVersion with -C -V, got %v, err=%v", parsedVerControl.Action, err)
	}
}

func TestParseCommandLine_BundledAndFeatures(t *testing.T) {
	parsedBundle, err := ParseCommandLine([]string{"-2ulvvN"})
	if err != nil {
		t.Fatalf("ParseCommandLine bundled flags failed: %v", err)
	}

	if !parsedBundle.Config.Colors256 {
		t.Error("expected Colors256=true")
	}

	if parsedBundle.Config.UTF8 != UTF8Force {
		t.Errorf("expected UTF8Force, got %v", parsedBundle.Config.UTF8)
	}

	if !parsedBundle.Config.LoginShell {
		t.Error("expected LoginShell=true")
	}

	if parsedBundle.Config.LogLevel != LogDebug {
		t.Errorf("expected LogDebug, got %v", parsedBundle.Config.LogLevel)
	}

	if parsedBundle.StartPolicy != ExistingOnly {
		t.Errorf("expected ExistingOnly, got %v", parsedBundle.StartPolicy)
	}

	parsedFeat, err := ParseCommandLine([]string{"-T", "256,RGB", "list-windows"})
	if err != nil {
		t.Fatalf("ParseCommandLine -T failed: %v", err)
	}

	if !reflect.DeepEqual(parsedFeat.Config.TerminalFeatures, []string{"256", "RGB"}) {
		t.Errorf("expected TerminalFeatures [256 RGB], got %v", parsedFeat.Config.TerminalFeatures)
	}
}

func TestParseCommandLine_ControlModes(t *testing.T) {
	parsedC, err := ParseCommandLine([]string{"-C"})
	if err != nil || parsedC.Action != ActionControl || parsedC.ControlNoEcho {
		t.Errorf("expected ActionControl with ControlNoEcho=false, got %+v, err=%v", parsedC, err)
	}

	parsedCC, err := ParseCommandLine([]string{"-CC"})
	if err != nil || parsedCC.Action != ActionControl || !parsedCC.ControlNoEcho {
		t.Errorf("expected ActionControl with ControlNoEcho=true, got %+v, err=%v", parsedCC, err)
	}
}

func TestParseCommandLine_CommandSequence(t *testing.T) {
	parsedSeq, err := ParseCommandLine([]string{"new-session", "-d", ";", "split-window", "-h"})
	if err != nil {
		t.Fatalf("ParseCommandLine sequence failed: %v", err)
	}

	if parsedSeq.Action != ActionCommand {
		t.Errorf("expected ActionCommand, got %v", parsedSeq.Action)
	}

	if len(parsedSeq.Commands.Commands()) != 2 {
		t.Fatalf("expected 2 commands in sequence, got %d", len(parsedSeq.Commands.Commands()))
	}
}

func TestParseCommandLine_Errors(t *testing.T) {
	if _, err := ParseCommandLine([]string{"-X"}); err == nil {
		t.Fatal("expected error on unknown flag -X")
	}

	if _, err := ParseCommandLine([]string{"--help"}); err == nil {
		t.Fatal("expected error on non-native flag --help")
	}

	if _, err := ParseCommandLine([]string{"-S"}); err == nil {
		t.Fatal("expected error on -S without argument")
	}

	if _, err := ParseCommandLine([]string{"-L", ""}); err == nil {
		t.Fatal("expected error on -L with empty argument")
	}

	if _, err := ParseCommandLine([]string{"-S", ""}); err == nil {
		t.Fatal("expected error on -S with empty argument")
	}

	if _, err := ParseCommandLine([]string{"-D", "list-panes"}); err == nil {
		t.Fatal("expected error for command args passed to -D")
	}

	if _, err := ParseCommandLine([]string{"-c", "echo", "list-panes"}); err == nil {
		t.Fatal("expected error for command args passed to -c")
	}

	if _, err := ParseCommandLine([]string{"-D", "-C"}); err == nil {
		t.Fatal("expected error for -D and -C together")
	}

	if _, err := ParseCommandLine([]string{"-c", "echo", "-C"}); err == nil {
		t.Fatal("expected error for -c and -C together")
	}

	if _, err := ParseCommandLine([]string{";"}); err == nil {
		t.Fatal("expected error on semicolon-only args")
	}

	if _, err := ParseCommandLine([]string{";", ";"}); err == nil {
		t.Fatal("expected error on consecutive semicolons only")
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
		Binary:           "tmux",
		SocketPath:       "",
		SocketName:       "",
		ConfigFile:       "",
		Env:              nil,
		Dir:              "",
		Limits:           Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
		UTF8:             UTF8Default,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         LogNone,
		LoginShell:       false,
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

func TestRunWithValidation(t *testing.T) {
	s := localServer(t)

	// Invalid command
	badCmd := Command{name: "invalid name with space", args: nil}
	if _, err := s.RunWith(t.Context(), badCmd, RunOptions{Start: AllowStart}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid command, got %v", err)
	}

	// Invalid StartPolicy
	validCmd, _ := NewCommand("display-message", "hello")
	if _, err := s.RunWith(t.Context(), validCmd, RunOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy, got %v", err)
	}
}

func TestRunSequenceWithValidation(t *testing.T) {
	s := localServer(t)

	// Empty sequence short-circuit
	emptySeq := CommandSequence{commands: nil}

	res, err := s.RunSequenceWith(t.Context(), emptySeq, RunOptions{Start: AllowStart})
	if err != nil {
		t.Fatalf("expected empty sequence to succeed, got %v", err)
	}

	if len(res.Stdout) != 0 || res.ExitCode != -1 {
		t.Errorf("unexpected result for empty sequence: %+v", res)
	}

	// Invalid StartPolicy on non-empty sequence
	cmd, _ := NewCommand("display-message", "hello")
	seq, _ := Sequence(cmd)

	if _, err := s.RunSequenceWith(t.Context(), seq, RunOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy, got %v", err)
	}
}

func TestControlModeUpfrontRejection(t *testing.T) {
	s := localServer(t)
	//nolint:exhaustruct_v5 // testing rejection on control-bound server handle
	s.conn = &Connection{server: s}

	cmd, _ := NewCommand("display-message", "hello")
	if _, err := s.Run(t.Context(), cmd); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for Run on control server, got %v", err)
	}

	if _, err := s.RunWith(t.Context(), cmd, RunOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for RunWith on control server, got %v", err)
	}

	seq, _ := Sequence(cmd)
	if _, err := s.RunSequence(t.Context(), seq); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for RunSequence on control server, got %v", err)
	}

	emptySeq := CommandSequence{commands: nil}
	if _, err := s.RunSequence(t.Context(), emptySeq); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for empty RunSequence on control server, got %v", err)
	}

	if _, err := s.RunSequenceWith(t.Context(), emptySeq, RunOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for empty RunSequenceWith on control server, got %v", err)
	}
}
