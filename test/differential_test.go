//go:build integration

package test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func TestDifferentialCommandQuoting(t *testing.T) {
	_ = os.Remove(injectionMarker)

	t.Cleanup(func() { _ = os.Remove(injectionMarker) })

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
				"$(touch " + injectionMarker + ")",
				"`date`",
				"$1",
				"~",
				"~/file",
				"~root",
			},
		},
		{
			name: "non-ASCII and UTF-8 bytes",
			inputs: []string{
				"日本語", //nolint:gosmopolitan // UTF-8 quoting corpus intentionally contains Han script
				"emoji 🚀",
				"\xff\xfe",
			},
		},
	}

	for _, p := range partitions {
		t.Run(p.name, func(t *testing.T) {
			for _, input := range p.inputs {
				t.Run(input, func(t *testing.T) {
					_ = os.Remove(injectionMarker)

					t.Cleanup(func() { _ = os.Remove(injectionMarker) })

					// -l prints format expressions literally.
					assertDisplayEchoes(t, ctx, server, input, "-l")

					if !strings.Contains(input, "#") {
						assertDisplayEchoes(t, ctx, server, input)
					}
				})
			}
		})
	}

	assertNoInjection(t, "at end of differential quoting tests")
}

func assertDisplayEchoes(t *testing.T, ctx context.Context, server *tmux.Server, input string, flags ...string) {
	t.Helper()

	args := append(append([]string{"display-message", "-p"}, flags...), "--", input)

	cmd, err := tmux.NewCommand(args[0], args[1:]...)
	if err != nil {
		t.Fatalf("NewCommand(%q) failed: %v", args, err)
	}

	res, err := server.Run(ctx, cmd)
	if err != nil {
		t.Fatalf("server.Run(%q) failed: %v, res: %+v", args, err, res)
	}

	if res.ExitCode != 0 {
		t.Errorf("%q: exit code %d, want 0 (stderr: %q)", args, res.ExitCode, string(res.Stderr))
	}

	if want := input + "\n"; string(res.Stdout) != want {
		t.Errorf("%q: stdout mismatch:\n got: %q\nwant: %q", args, string(res.Stdout), want)
	}

	assertNoInjection(t, fmt.Sprintf("by %q", args))
}

func assertNoInjection(t *testing.T, when string) {
	t.Helper()

	if _, err := os.Stat(injectionMarker); err == nil {
		t.Errorf("%s was created %s", injectionMarker, when)
		_ = os.Remove(injectionMarker)
	}
}
