package tmux

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzParseEnvironment(f *testing.F) {
	seeds := []struct {
		tmux string
		pane string
	}{
		{"/tmp/tmux-1000/default,123,0", "%0"},
		{"/tmp/a,b,c,456,1", "%1"},
		{"/tmp/tmux-1000/default,123", ""},
		{"bad", ""},
		{"", ""},
		{",,", ""},
		{"/tmp/a,b,123,%1", "%1"},
		{"/tmp/a,0,1", ""},
		{"relative,1,1", ""},
		{"/tmp/a,1,01", ""},
		{"/tmp/a,1,no", ""},
		{"/tmp/default,456", ""},
		{"/tmp/a,b,123,0", "%17"},
	}

	for _, s := range seeds {
		f.Add(s.tmux, s.pane)
	}

	f.Fuzz(func(t *testing.T, tmuxEnv string, tmuxPane string) {
		info, err := ParseEnvironment(Environment{TMUX: tmuxEnv, TMUXPane: tmuxPane})
		if err == nil {
			if info.PID <= 0 {
				t.Errorf("PID must be positive, got %d (TMUX=%q)", info.PID, tmuxEnv)
			}

			if !filepath.IsAbs(info.SocketPath) {
				t.Errorf("SocketPath must be absolute, got %q (TMUX=%q)", info.SocketPath, tmuxEnv)
			}
		}
	})
}

func FuzzParseCommandLine(f *testing.F) {
	seeds := [][]string{
		{"-S", "/tmp/s", "new-session", "-d", "-s", "foo"},
		{"-L", "dev", "attach", "-t", "0"},
		{"list-panes"},
		{},
		{"-S/tmp/custom", "list-windows"},
		{"-Lwork", "list-panes"},
		{"-f", "/etc/tmux.conf", "new-session"},
		{"-u", "-v", "display-message", "hello"},
		{"-N", "-C", "kill-server"},
		{"--", "list-sessions"},
		{"new-session", "-d", "-s", "my-session"},
		{"-u", "-v"},
	}

	for _, seed := range seeds {
		f.Add(strings.Join(seed, "\n"))
	}

	f.Fuzz(func(t *testing.T, raw string) {
		testCommandLine := func(args []string) {
			_, cmd, err := ParseCommandLine(args)
			if err == nil {
				if !cmd.Valid() {
					t.Errorf("ParseCommandLine(%q) returned invalid command: %+v", args, cmd)
				}
			}
		}

		// Test newline-delimited args (handles arbitrary strings per arg)
		if raw == "" {
			testCommandLine([]string{})
		} else {
			testCommandLine(strings.Split(raw, "\n"))
		}

		// Test whitespace-separated args
		testCommandLine(strings.Fields(raw))

		// Test null-byte-separated args
		testCommandLine(strings.Split(raw, "\x00"))
	})
}
