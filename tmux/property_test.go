package tmux_test

import (
	"fmt"
	"slices"
	"testing"

	"pgregory.net/rapid"

	tmux "github.com/zigai/gotmux/tmux"
)

// TestRapidVersionProperties validates algebraic ordering properties of version comparisons.
//
//nolint:cyclop,gocognit // property testing loop exercises multiple algebraic relations
func TestRapidVersionProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		major := rapid.IntRange(0, 50).Draw(rt, "major")
		minor := rapid.IntRange(0, 50).Draw(rt, "minor")
		isCanonical := rapid.Bool().Draw(rt, "isCanonical")

		var raw string

		if isCanonical {
			patch := rapid.SampledFrom([]string{"", "a", "b", "c", "z"}).Draw(rt, "patch")
			raw = fmt.Sprintf("%d.%d%s", major, minor, patch)
		} else {
			vendor := rapid.SampledFrom([]string{"-rc1", "-git", "-vendor", "~beta1"}).Draw(rt, "vendor")
			raw = fmt.Sprintf("%d.%d%s", major, minor, vendor)
		}

		v := tmux.ParseVersion(raw)

		if v.Major != major || v.Minor != minor {
			rt.Fatalf("parsed version mismatch for %q: got %d.%d, want %d.%d", raw, v.Major, v.Minor, major, minor)
		}

		if !isCanonical {
			// Documented contract: Unrecognized versions (development/vendor) always return false for AtLeast
			if v.Recognized {
				rt.Fatalf("vendor version %s should not be recognized", raw)
			}

			if v.AtLeast(major, minor) {
				rt.Fatalf("unrecognized version %s must return false for AtLeast", raw)
			}

			return
		}

		if !v.Recognized {
			rt.Fatalf("canonical version %s should be recognized", raw)
		}

		// Reflexivity
		if !v.AtLeast(major, minor) {
			rt.Fatalf("version %s is not at least itself (%d, %d)", raw, major, minor)
		}

		// Strictly greater
		if major > 0 && !v.AtLeast(major-1, minor) {
			rt.Fatalf("version %s should be at least (%d, %d)", raw, major-1, minor)
		}

		if minor > 0 && !v.AtLeast(major, minor-1) {
			rt.Fatalf("version %s should be at least (%d, %d)", raw, major, minor-1)
		}

		// Strictly smaller
		if v.AtLeast(major+1, minor) {
			rt.Fatalf("version %s should not be at least (%d, %d)", raw, major+1, minor)
		}

		if v.AtLeast(major, minor+1) {
			rt.Fatalf("version %s should not be at least (%d, %d)", raw, major, minor+1)
		}

		if v.String() != raw {
			rt.Fatalf("version String() mismatch: got %q, want %q", v.String(), raw)
		}
	})
}

// TestRapidCommandAndSequenceProperties validates argument ownership and immutability.
//
//nolint:cyclop,gocognit // property testing loop exercises multiple command and sequence properties
func TestRapidCommandAndSequenceProperties(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		cmdCount := rapid.IntRange(1, 5).Draw(rt, "cmdCount")
		cmds := make([]tmux.Command, 0, cmdCount)

		for i := range cmdCount {
			name := rapid.StringMatching(`^[a-z][a-z0-9-]{0,12}$`).Draw(rt, fmt.Sprintf("name-%d", i))
			argCount := rapid.IntRange(0, 6).Draw(rt, fmt.Sprintf("argCount-%d", i))
			args := make([]string, 0, argCount)

			for j := range argCount {
				arg := rapid.StringMatching(`^[^\x00]{0,32}$`).Draw(rt, fmt.Sprintf("arg-%d-%d", i, j))
				args = append(args, arg)
			}

			cmd, err := tmux.NewCommand(name, args...)
			if err != nil {
				rt.Fatalf("NewCommand failed: %v", err)
			}

			if !cmd.Valid() {
				rt.Fatalf("Command %v should be valid", cmd)
			}

			if cmd.Name() != name {
				rt.Fatalf("Name mismatch: got %q, want %q", cmd.Name(), name)
			}

			// Defensive copy check: mutating the returned slice must not affect cmd
			retArgs := cmd.Args()
			if !slices.Equal(retArgs, args) {
				rt.Fatalf("Args mismatch: got %v, want %v", retArgs, args)
			}

			if len(retArgs) > 0 {
				retArgs[0] = "mutated-should-not-persist"
				if slices.Equal(cmd.Args(), retArgs) {
					rt.Fatalf("Command.Args() leaked internal storage to caller")
				}
			}

			cmds = append(cmds, cmd)
		}

		seq, err := tmux.Sequence(cmds...)
		if err != nil {
			rt.Fatalf("Sequence failed: %v", err)
		}

		seqCmds := seq.Commands()
		if len(seqCmds) != len(cmds) {
			rt.Fatalf("Sequence command count mismatch: got %d, want %d", len(seqCmds), len(cmds))
		}

		for i := range cmds {
			if seqCmds[i].Name() != cmds[i].Name() {
				rt.Fatalf("Sequence command %d name mismatch", i)
			}

			if !slices.Equal(seqCmds[i].Args(), cmds[i].Args()) {
				rt.Fatalf("Sequence command %d args mismatch", i)
			}
		}
	})
}
