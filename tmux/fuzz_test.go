package tmux

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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
			parsed, err := ParseCommandLine(args)
			if err == nil {
				for _, cmd := range parsed.Commands.Commands() {
					if !cmd.Valid() {
						t.Errorf("ParseCommandLine(%q) returned invalid command: %+v", args, cmd)
					}
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

func FuzzKeyValid(f *testing.F) {
	seeds := []string{
		"Enter", "Escape", "Space", "Tab", "BSpace",
		"C-a", "M-x", "S-Up", "C-M-x", "C-S-Down", "^a", "^^",
		"F1", "F12", "F63",
		"User0", "User9", "User63", "User1024",
		"MouseDown1Pane", "MouseUp2Border", "MouseDrag1Status", "MouseDragEnd1StatusLeft",
		"WheelUpPane", "WheelDownStatus", "DoubleClick1Pane", "TripleClick1Pane", "SecondClick1Pane",
		"MouseDown1Control7", "M-MouseDown1Pane", "C-MouseDown1Status",
		"Any", "None",
		"", "C-", "M-", "User", "User-1", "MouseDown1", "InvalidKey", "\x00", "\x01", "\x7f",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		k := Key(s)
		valid := k.Valid()

		if valid {
			if strings.ContainsRune(s, '\x00') {
				t.Fatalf("Key(%q).Valid() = true but contains NUL byte", s)
			}

			if !utf8.ValidString(s) {
				t.Fatalf("Key(%q).Valid() = true but invalid UTF-8", s)
			}

			if s == "" {
				t.Fatalf("Key(%q).Valid() = true but empty", s)
			}
		}
	})
}

func FuzzParseOptionName(f *testing.F) {
	seeds := []string{
		"escape-time",
		"status-format[0]",
		"status-format[10]",
		"codepoint-widths[500]",
		"@my_opt",
		"@my_opt[42]",
		"user-keys[100]",
		"",
		"@",
		"[0]",
		"opt[",
		"opt[-1]",
		"opt[--1]",
		"opt[abc]",
		"opt[0x10]",
		"opt[ 1 ]",
		"opt[1073741824]",
		"opt[1073741825]",
		"opt[99999999999999999999999]",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		base, idx, indexed, err := parseOptionName(s)
		if err == nil {
			if !validFormatName(base) || base == "@" {
				t.Fatalf("parseOptionName(%q) returned invalid base %q", s, base)
			}

			if idx < 0 || idx > 1<<30 {
				t.Fatalf("parseOptionName(%q) returned out-of-range index %d", s, idx)
			}

			if indexed && (!strings.HasSuffix(s, "]") || !strings.ContainsRune(s, '[')) {
				t.Fatalf("parseOptionName(%q) marked indexed without proper brackets", s)
			}

			if !validOptionName(s) {
				t.Fatalf("parseOptionName succeeded for %q but validOptionName returned false", s)
			}
		}
	})
}

func FuzzDecodeEvent(f *testing.F) {
	seeds := []string{
		"%output %1 hello\\012world\n",
		"%extended-output %1 1500 : hello\n",
		"%layout-change @0 bbc3,80x24,0,0,0 bbc3,80x24,0,0,1 \n",
		"%session-changed $0 my-session\n",
		"%session-renamed new-name\n",
		"%sessions-changed\n",
		"%window-add @1\n",
		"%window-close @1\n",
		"%window-renamed @1 win-name\n",
		"%client-session-changed /dev/pts/1 $0 my-session\n",
		"%client-detached /dev/pts/1\n",
		"%client-flags-changed /dev/pts/1 read-only\n",
		"%pause %0\n",
		"%continue %0\n",
		"%config-error syntax error in config line 10\n",
		"%message message text\n",
		"%pane-mode-changed %2\n",
		"%paste-buffer-changed mybuf\n",
		"%paste-buffer-deleted mybuf\n",
		"%window-pane-changed @1 %2\n",
		"%subscription-changed sub1 $0 @1 0 %2 : sub_data\n",
		"%unknown-future-event arg1 arg2\n",
		"%exit\n",
		"%\n",
		"not-an-event\n",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 {
			return
		}

		ev, err := decodeEvent([]byte(s), 4096)
		if err == nil {
			name := ev.RawName()
			if name == "" || !isValidEventName(name) {
				t.Fatalf("decodeEvent(%q) returned event with invalid RawName %q", s, name)
			}

			if ev.Received().IsZero() {
				t.Fatalf("decodeEvent(%q) returned zero Received timestamp", s)
			}

			if ev.eventBytes() <= 0 {
				t.Fatalf("decodeEvent(%q) returned non-positive eventBytes: %d", s, ev.eventBytes())
			}

			cloned := ev.cloneEvent()
			if cloned.RawName() != name {
				t.Fatalf("cloneEvent changed RawName: got %q, want %q", cloned.RawName(), name)
			}
		}
	})
}
