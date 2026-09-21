package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

	if sockets == nil {
		t.Fatal("expected non-nil sockets slice")
	}

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

	if servers == nil {
		t.Fatal("expected non-nil servers slice")
	}

	if len(servers) != len(sockets) {
		t.Fatalf("expected len(servers) == len(sockets) (%d != %d)", len(servers), len(sockets))
	}
}

func TestPrepareAttachTargetValidation(t *testing.T) {
	s := localServer(t)

	// Valid target string (session name)
	streams := TerminalStreams{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	}
	// Invalid streams (non-terminal) should return ErrInvalidArgument
	_, err := s.PrepareAttachTarget(t.Context(), "dev", streams, AttachOptions{ReadOnly: false, PreserveEnvironment: false, Detach: DetachNone, Dir: "", Flags: nil})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for non-terminal streams, got: %v", err)
	}

	// Target containing NUL byte should fail immediately
	if _, err := s.PrepareAttachTarget(t.Context(), "bad\x00target", streams, AttachOptions{ReadOnly: false, PreserveEnvironment: false, Detach: DetachNone, Dir: "", Flags: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for target with NUL, got: %v", err)
	}
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
	if _, err := s.RunWith(t.Context(), badCmd, RunOptions{Start: AllowStart, Input: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid command, got %v", err)
	}

	// Invalid StartPolicy
	validCmd, _ := NewCommand("display-message", "hello")
	if _, err := s.RunWith(t.Context(), validCmd, RunOptions{Start: StartPolicy(99), Input: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy, got %v", err)
	}
}

func TestRunSequenceWithValidation(t *testing.T) {
	s := localServer(t)

	// Empty sequence short-circuit
	emptySeq := CommandSequence{commands: nil}

	res, err := s.RunSequenceWith(t.Context(), emptySeq, RunOptions{Start: AllowStart, Input: nil})
	if err != nil {
		t.Fatalf("expected empty sequence to succeed, got %v", err)
	}

	if len(res.Stdout) != 0 || res.ExitCode != -1 {
		t.Errorf("unexpected result for empty sequence: %+v", res)
	}

	// Invalid StartPolicy on non-empty sequence
	cmd, _ := NewCommand("display-message", "hello")

	seq, _ := Sequence(cmd)
	if _, err := s.RunSequenceWith(t.Context(), seq, RunOptions{Start: StartPolicy(99), Input: nil}); !errors.Is(err, ErrInvalidArgument) {
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

	if _, err := s.RunWith(t.Context(), cmd, RunOptions{Start: AllowStart, Input: nil}); !errors.Is(err, ErrTransportUnsupported) {
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

	if _, err := s.RunSequenceWith(t.Context(), emptySeq, RunOptions{Start: AllowStart, Input: nil}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for empty RunSequenceWith on control server, got %v", err)
	}

	if _, err := s.SourceText(t.Context(), "set -g status off", SourceOptions{QuietMissing: false, ParseOnly: false, Verbose: false}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for SourceText on control server, got %v", err)
	}
}

func TestRunWithInputLimits(t *testing.T) {
	s, err := New(Config{
		Binary:           "/bin/sh",
		SocketPath:       filepath.Join(t.TempDir(), "s"),
		SocketName:       "",
		ConfigFile:       "",
		Env:              []string{},
		Dir:              t.TempDir(),
		Limits:           Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 64, Concurrent: 0},
		UTF8:             UTF8Default,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         LogNone,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	cmd, _ := NewCommand("display-message", "hello")

	hugeInput := make([]byte, 128)
	if _, err := s.RunWith(t.Context(), cmd, RunOptions{Start: AllowStart, Input: hugeInput}); !errors.Is(err, ErrInputLimit) {
		t.Fatalf("expected ErrInputLimit for RunWith with oversized input, got %v", err)
	}

	seq, _ := Sequence(cmd)
	if _, err := s.RunSequenceWith(t.Context(), seq, RunOptions{Start: AllowStart, Input: hugeInput}); !errors.Is(err, ErrInputLimit) {
		t.Fatalf("expected ErrInputLimit for RunSequenceWith with oversized input, got %v", err)
	}
}

func TestSourceTextValidation(t *testing.T) {
	s := localServer(t)

	// Invalid string with NUL byte
	if _, err := s.SourceText(t.Context(), "invalid\x00text", SourceOptions{QuietMissing: false, ParseOnly: false, Verbose: false}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for SourceText with NUL, got %v", err)
	}

	// String exceeding InputBytes limit
	sLimits, err := New(Config{
		Binary:           "/bin/sh",
		SocketPath:       filepath.Join(t.TempDir(), "s"),
		SocketName:       "",
		ConfigFile:       "",
		Env:              []string{},
		Dir:              t.TempDir(),
		Limits:           Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 64, Concurrent: 0},
		UTF8:             UTF8Default,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         LogNone,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if _, err := sLimits.SourceText(t.Context(), strings.Repeat("x", 128), SourceOptions{QuietMissing: false, ParseOnly: false, Verbose: false}); !errors.Is(err, ErrInputLimit) {
		t.Fatalf("expected ErrInputLimit for SourceText with oversized text, got %v", err)
	}
}

func TestPrepareControlModeRejection(t *testing.T) {
	sControl := localServer(t)
	//nolint:exhaustruct_v5 // mock control connection for upfront rejection testing
	sControl.conn = &Connection{server: sControl}

	cmd, _ := NewCommand("display-message", "hello")
	seq, _ := Sequence(cmd)
	streams := Streams{In: nil, Out: nil, Err: nil}
	termStreams := TerminalStreams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	if _, err := sControl.PrepareCommand(t.Context(), cmd, streams, CommandOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareCommand on control server, got %v", err)
	}

	if _, err := sControl.PrepareSequence(t.Context(), seq, streams, CommandOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareSequence on control server, got %v", err)
	}

	if _, err := sControl.PrepareTerminal(t.Context(), cmd, termStreams, TerminalOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareTerminal on control server, got %v", err)
	}

	if _, err := sControl.PrepareTerminalSequence(t.Context(), seq, termStreams, TerminalOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareTerminalSequence on control server, got %v", err)
	}

	if _, err := sControl.PrepareDefaultTerminal(t.Context(), termStreams, TerminalOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareDefaultTerminal on control server, got %v", err)
	}

	if _, err := sControl.PrepareForegroundServer(t.Context()); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareForegroundServer on control server, got %v", err)
	}

	if _, err := sControl.PrepareRootShell(t.Context(), "echo hi", streams, RootShellOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for PrepareRootShell on control server, got %v", err)
	}

	if _, err := sControl.RunRootShell(t.Context(), "echo hi", RootShellOptions{Start: AllowStart}); !errors.Is(err, ErrTransportUnsupported) {
		t.Fatalf("expected ErrTransportUnsupported for RunRootShell on control server, got %v", err)
	}
}

func TestPrepareCommandAndSequenceValidation(t *testing.T) {
	s := localServer(t)
	cmd, _ := NewCommand("display-message", "hello")
	streams := Streams{In: nil, Out: nil, Err: nil}
	termStreams := TerminalStreams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	prepCmd, err := s.PrepareCommand(t.Context(), cmd, streams, CommandOptions{Start: AllowStart})
	if err != nil {
		t.Fatalf("PrepareCommand failed for valid command: %v", err)
	}

	if prepCmd == nil || prepCmd.Process != nil {
		t.Fatal("expected non-nil unstarted exec.Cmd")
	}

	badCmd := Command{name: "invalid command", args: nil}
	if _, err := s.PrepareCommand(t.Context(), badCmd, streams, CommandOptions{Start: AllowStart}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid command, got %v", err)
	}

	emptySeq := CommandSequence{commands: nil}
	if _, err := s.PrepareSequence(t.Context(), emptySeq, streams, CommandOptions{Start: AllowStart}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for empty sequence, got %v", err)
	}

	if _, err := s.PrepareTerminalSequence(t.Context(), emptySeq, termStreams, TerminalOptions{Start: AllowStart}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for empty terminal sequence, got %v", err)
	}
}

func TestPrepareStartPolicyValidation(t *testing.T) {
	s := localServer(t)
	cmd, _ := NewCommand("display-message", "hello")
	seq, _ := Sequence(cmd)
	streams := Streams{In: nil, Out: nil, Err: nil}
	termStreams := TerminalStreams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	if _, err := s.PrepareCommand(t.Context(), cmd, streams, CommandOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on PrepareCommand, got %v", err)
	}

	if _, err := s.PrepareSequence(t.Context(), seq, streams, CommandOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on PrepareSequence, got %v", err)
	}

	if _, err := s.PrepareTerminal(t.Context(), cmd, termStreams, TerminalOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on PrepareTerminal, got %v", err)
	}

	if _, err := s.PrepareTerminalSequence(t.Context(), seq, termStreams, TerminalOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on PrepareTerminalSequence, got %v", err)
	}

	if _, err := s.PrepareDefaultTerminal(t.Context(), termStreams, TerminalOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on PrepareDefaultTerminal, got %v", err)
	}

	prepExisting, err := s.PrepareCommand(t.Context(), cmd, streams, CommandOptions{Start: ExistingOnly})
	if err != nil {
		t.Fatalf("PrepareCommand failed: %v", err)
	}

	if !slices.Contains(prepExisting.Args, "-N") {
		t.Errorf("expected -N in PrepareCommand args for ExistingOnly, got %v", prepExisting.Args)
	}
}

func TestPrepareForegroundValidation(t *testing.T) {
	s := localServer(t)

	fgCmd, err := s.PrepareForegroundServer(t.Context())
	if err != nil {
		t.Fatalf("PrepareForegroundServer failed: %v", err)
	}

	if !slices.Contains(fgCmd.Args, "-D") {
		t.Fatalf("expected -D flag in PrepareForegroundServer args, got %v", fgCmd.Args)
	}
}

func TestPrepareRootShellValidation(t *testing.T) {
	s := localServer(t)
	streams := Streams{In: nil, Out: nil, Err: nil}

	shCmd, err := s.PrepareRootShell(t.Context(), "echo root", streams, RootShellOptions{Start: AllowStart})
	if err != nil {
		t.Fatalf("PrepareRootShell failed: %v", err)
	}

	foundC := false

	for i, arg := range shCmd.Args {
		if arg == "-c" && i+1 < len(shCmd.Args) && shCmd.Args[i+1] == "echo root" {
			foundC = true
			break
		}
	}

	if !foundC {
		t.Fatalf("expected -c 'echo root' in PrepareRootShell args, got %v", shCmd.Args)
	}

	if _, err := s.PrepareRootShell(t.Context(), "bad\x00cmd", streams, RootShellOptions{Start: AllowStart}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for PrepareRootShell with NUL, got %v", err)
	}

	if _, err := s.PrepareRootShell(t.Context(), "echo hi", streams, RootShellOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on PrepareRootShell, got %v", err)
	}

	shExisting, err := s.PrepareRootShell(t.Context(), "echo hi", streams, RootShellOptions{Start: ExistingOnly})
	if err != nil {
		t.Fatalf("PrepareRootShell failed: %v", err)
	}

	if !slices.Contains(shExisting.Args, "-N") {
		t.Errorf("expected -N in PrepareRootShell args for ExistingOnly, got %v", shExisting.Args)
	}
}

func TestRunRootShellValidation(t *testing.T) {
	s := localServer(t)

	if _, err := s.RunRootShell(t.Context(), "bad\x00cmd", RootShellOptions{Start: AllowStart}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for RunRootShell with NUL, got %v", err)
	}

	if _, err := s.RunRootShell(t.Context(), "echo hi", RootShellOptions{Start: StartPolicy(99)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid StartPolicy on RunRootShell, got %v", err)
	}
}

func TestAttachOptionsValidation(t *testing.T) {
	s := localServer(t)
	termStreams := TerminalStreams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}

	if _, err := s.PrepareAttachTarget(t.Context(), "dev", termStreams, AttachOptions{ReadOnly: false, PreserveEnvironment: false, Detach: DetachMode(99), Dir: "", Flags: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid DetachMode, got %v", err)
	}

	if _, err := s.PrepareAttachTarget(t.Context(), "dev", termStreams, AttachOptions{ReadOnly: false, PreserveEnvironment: false, Detach: DetachNone, Dir: "bad\x00dir", Flags: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid Dir, got %v", err)
	}

	if _, err := s.PrepareAttachTarget(t.Context(), "dev", termStreams, AttachOptions{ReadOnly: false, PreserveEnvironment: false, Detach: DetachNone, Dir: "", Flags: []ClientFlag{ClientFlag("unknown")}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid ClientFlag, got %v", err)
	}

	if _, err := s.PrepareAttach(t.Context(), SessionID("invalid session"), termStreams, AttachOptions{ReadOnly: false, PreserveEnvironment: false, Detach: DetachNone, Dir: "", Flags: nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid SessionID, got %v", err)
	}
}

func TestAttachArgsFlagEmission(t *testing.T) {
	args, err := attachArgs("test-session", AttachOptions{
		ReadOnly:            true,
		PreserveEnvironment: true,
		Detach:              DetachParentSignal,
		Dir:                 "/tmp",
		Flags:               []ClientFlag{ClientReadOnly, ClientIgnoreSize},
	})
	if err != nil {
		t.Fatalf("attachArgs failed: %v", err)
	}

	if !slices.Contains(args, "-x") || !slices.Contains(args, "-r") || !slices.Contains(args, "-E") || !slices.Contains(args, "-c") || !slices.Contains(args, "-f") {
		t.Fatalf("attachArgs missing expected flags: %v", args)
	}

	emptyTargetArgs, err := attachArgs("", AttachOptions{
		ReadOnly:            false,
		PreserveEnvironment: false,
		Detach:              DetachOtherClients,
		Dir:                 "",
		Flags:               nil,
	})
	if err != nil {
		t.Fatalf("attachArgs with empty target failed: %v", err)
	}

	if slices.Contains(emptyTargetArgs, "-t") {
		t.Fatalf("expected no -t in attachArgs for empty target, got %v", emptyTargetArgs)
	}

	if !slices.Contains(emptyTargetArgs, "-d") {
		t.Fatalf("expected -d in attachArgs for DetachOtherClients, got %v", emptyTargetArgs)
	}
}

func TestClientFlagValidation(t *testing.T) {
	for _, flag := range []ClientFlag{
		ClientFlagActivePane, ClientFlagIgnoreSize, ClientFlagNoDetachOnDestroy,
		ClientFlagNoOutput, ClientFlagReadOnly, ClientFlagWaitExit,
	} {
		if !flag.Valid() {
			t.Errorf("expected flag %q to be valid", flag)
		}
	}

	if ClientFlag("invalid_flag").Valid() {
		t.Errorf("expected invalid flag to be invalid")
	}
}

func TestNativeEscapedSemicolon(t *testing.T) {
	p, err := ParseCommandLine([]string{"display-message", "-p", `\;`})
	if err != nil {
		t.Fatal(err)
	}

	cmds := p.Commands.Commands()
	if len(cmds) != 1 || !reflect.DeepEqual(cmds[0].Args(), []string{"-p", ";"}) {
		t.Fatalf("literal semicolon was consumed as a separator: %+v", cmds)
	}
}

func TestNativeTrailingSemicolon(t *testing.T) {
	p, err := ParseCommandLine([]string{"display-message", "-p", "first;", "display-message", "-p", "second"})
	if err != nil {
		t.Fatal(err)
	}

	cmds := p.Commands.Commands()
	if len(cmds) != 2 {
		t.Fatalf("native trailing separator did not split commands: %+v", cmds)
	}
}

func TestNilServerMethods(t *testing.T) {
	var s *Server

	c, _ := NewCommand("display-message", "test")
	seq, _ := Sequence(c)

	cases := []struct {
		name string
		run  func() error
	}{
		{"Run", func() error { _, e := s.Run(context.Background(), c); return e }},
		{"RunWith", func() error {
			_, e := s.RunWith(context.Background(), c, RunOptions{Start: AllowStart, Input: nil})
			return e
		}},
		{"RunSequence", func() error { _, e := s.RunSequence(context.Background(), seq); return e }},
		{"RunSequenceWith", func() error {
			_, e := s.RunSequenceWith(context.Background(), seq, RunOptions{Start: AllowStart, Input: nil})
			return e
		}},
		{"SourceText", func() error {
			_, e := s.SourceText(context.Background(), "", SourceOptions{QuietMissing: false, ParseOnly: false, Verbose: false})
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("panicked instead of returning ErrInvalidHandle: %v", p)
				}
			}()

			if err := tc.run(); err == nil {
				t.Error("expected invalid-handle error")
			}
		})
	}
}
