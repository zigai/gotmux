//go:build integration

package tmux_test

import (
	"os"
	"strings"
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func TestDifferentialCommandQuoting(t *testing.T) {
	_ = os.Remove("/tmp/bad")
	t.Cleanup(func() { _ = os.Remove("/tmp/bad") })

	server, _, ctx := apiFixture(t)

	partitions := []struct {
		name   string
		inputs []string
	}{
		{
			name:   "empty",
			inputs: []string{""},
		},
		{
			name: "whitespace",
			inputs: []string{
				"hello world",
				"  leading and trailing  ",
				"multi\nline\nstring",
				"\twith\ttabs\t",
			},
		},
		{
			name: "quotes",
			inputs: []string{
				"'single'",
				"\"double\"",
				"'\"mixed\"'",
				"unmatched ' quote",
				"unmatched \" quote",
			},
		},
		{
			name: "semicolons and backslashes",
			inputs: []string{
				";",
				`\;`,
				`\\;`,
				"a;b",
				`a\;b`,
				`a\\;b`,
				";;",
				"x; y; z",
			},
		},
		{
			name: "format expressions",
			inputs: []string{
				"#{pane_id}",
				"#{==:#{session_id},1}",
				"#{unknown_format}",
				"#{#",
			},
		},
		{
			name: "shell expansions and command substitution",
			inputs: []string{
				"$HOME",
				"$(touch /tmp/bad)",
				"`date`",
				"$1",
			},
		},
		{
			name: "non-ASCII and UTF-8 bytes",
			inputs: []string{
				"日本語",
				"emoji 🚀",
				"\xff\xfe",
			},
		},
	}

	for _, p := range partitions {
		t.Run(p.name, func(t *testing.T) {
			for _, input := range p.inputs {
				t.Run(input, func(t *testing.T) {
					_ = os.Remove("/tmp/bad")
					t.Cleanup(func() { _ = os.Remove("/tmp/bad") })

					// Test with "-p", "-l", "--", input so format expressions (#{pane_id}, etc.)
					// are printed literally without format expansion, verifying differential
					// command quoting through the tmux C parser verbatim.
					cmd, err := tmux.NewCommand("display-message", "-p", "-l", "--", input)
					if err != nil {
						t.Fatalf("NewCommand(%q) failed: %v", input, err)
					}

					res, err := server.Run(ctx, cmd)
					if err != nil {
						t.Fatalf("server.Run(%q) failed: %v, res: %+v", input, err, res)
					}

					if res.ExitCode != 0 {
						t.Errorf("expected exit code 0 for input %q, got %d (stderr: %q)", input, res.ExitCode, string(res.Stderr))
					}

					wantStdout := input + "\n"
					if string(res.Stdout) != wantStdout {
						t.Errorf("stdout mismatch for input %q:\n got: %q\nwant: %q", input, string(res.Stdout), wantStdout)
					}

					if _, err := os.Stat("/tmp/bad"); err == nil {
						t.Errorf("/tmp/bad was created during test of %q", input)
						_ = os.Remove("/tmp/bad")
					}

					// For inputs without format characters, also verify with display-message -p -- <arg>
					if !strings.Contains(input, "#") {
						cmdNoL, err := tmux.NewCommand("display-message", "-p", "--", input)
						if err != nil {
							t.Fatalf("NewCommand without -l (%q) failed: %v", input, err)
						}
						resNoL, err := server.Run(ctx, cmdNoL)
						if err != nil {
							t.Fatalf("server.Run without -l (%q) failed: %v", input, err)
						}
						if resNoL.ExitCode != 0 || string(resNoL.Stdout) != wantStdout {
							t.Errorf("display-message -p -- %q failed: exit=%d stdout=%q", input, resNoL.ExitCode, string(resNoL.Stdout))
						}
						if _, err := os.Stat("/tmp/bad"); err == nil {
							t.Errorf("/tmp/bad was created during test without -l of %q", input)
							_ = os.Remove("/tmp/bad")
						}
					}
				})
			}
		})
	}

	if _, err := os.Stat("/tmp/bad"); err == nil {
		t.Errorf("/tmp/bad exists at end of differential quoting tests")
		_ = os.Remove("/tmp/bad")
	}
}
