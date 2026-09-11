//go:build integration

package tmux_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

var metamorphicCandidateKeys = []string{
	"@tgo_meta_1",
	"@tgo_meta_2",
	"@tgo_meta_3",
	"@tgo_meta_4",
}

var metamorphicValuePartitions = []string{
	// Semicolons
	";",
	`\;`,
	"trailing;",
	";leading",
	"mid;dle",
	";;",
	"x; y; z",
	`\\;`,

	// Quotes
	"'single'",
	`"double"`,
	`'"mixed"'`,
	"unmatched ' quote",
	`unmatched " quote`,

	// Whitespace & newlines
	"",
	"  leading space",
	"trailing space  ",
	"\twith\ttabs\t",
	"line1\nline2",

	// Expansions & formats
	"$HOME",
	"$(touch /tmp/bad)",
	"`date`",
	"#{pane_id}",
	"#{==:1,1}",

	// Backslashes & escapes
	`\`,
	`\\`,
	`\n`,
	`\t`,

	// Non-ASCII & UTF-8
	"日本語", //nolint:gosmopolitan // metamorphic UTF-8 corpus intentionally contains Han script
	"emoji 🚀",
	"àáâãäå",
}

func drawCommandPair(rt *rapid.T, sessA, sessB tmux.Session) (tmux.Command, tmux.Command) {
	key := rapid.SampledFrom(metamorphicCandidateKeys).Draw(rt, "key")
	action := rapid.SampledFrom([]string{"set", "set", "set", "unset"}).Draw(rt, "action")

	genValue := rapid.OneOf(
		rapid.SampledFrom(metamorphicValuePartitions),
		rapid.StringMatching(`^[ -~]{0,32}$`),
	)

	var (
		cmdA tmux.Command
		cmdB tmux.Command
		errA error
		errB error
	)

	if action == "set" {
		val := genValue.Draw(rt, "val")
		cmdA, errA = tmux.NewCommand("set-option", "-t", string(sessA.ID()), "--", key, val)
		cmdB, errB = tmux.NewCommand("set-option", "-t", string(sessB.ID()), "--", key, val)
	} else {
		cmdA, errA = tmux.NewCommand("set-option", "-t", string(sessA.ID()), "-u", "--", key)
		cmdB, errB = tmux.NewCommand("set-option", "-t", string(sessB.ID()), "-u", "--", key)
	}

	if errA != nil {
		rt.Fatalf("NewCommand A failed: %v", errA)
	}

	if errB != nil {
		rt.Fatalf("NewCommand B failed: %v", errB)
	}

	return cmdA, cmdB
}

func assertMetamorphicEquivalence(ctx context.Context, rt *rapid.T, server *tmux.Server, sessA, sessB tmux.Session) {
	for _, key := range metamorphicCandidateKeys {
		optA, errA := sessA.Options().User(ctx, key)

		optB, errB := sessB.Options().User(ctx, key)
		if errA != nil || errB != nil {
			rt.Fatalf("User option lookup failed for %s: errA=%v, errB=%v", key, errA, errB)
		}

		valA, okA := optA.Local.Get()

		valB, okB := optB.Local.Get()
		if okA != okB {
			rt.Fatalf("option presence mismatch for key %s: A=%v, B=%v", key, okA, okB)
		}

		if valA != valB {
			rt.Fatalf("option value mismatch for key %s:\n%s", key, cmp.Diff(valA, valB))
		}

		showCmdA, errA := tmux.NewCommand("show-options", "-v", "-t", string(sessA.ID()), "--", key)
		if errA != nil {
			rt.Fatalf("NewCommand showCmdA failed: %v", errA)
		}

		showCmdB, errB := tmux.NewCommand("show-options", "-v", "-t", string(sessB.ID()), "--", key)
		if errB != nil {
			rt.Fatalf("NewCommand showCmdB failed: %v", errB)
		}

		resA, errA := server.Run(ctx, showCmdA)
		resB, errB := server.Run(ctx, showCmdB)

		if (errA == nil) != (errB == nil) {
			rt.Fatalf("raw option error mismatch for %s: errA=%v, errB=%v", key, errA, errB)
		}

		if resA.ExitCode != resB.ExitCode {
			rt.Fatalf("raw option exit code mismatch for %s: A=%d, B=%d", key, resA.ExitCode, resB.ExitCode)
		}

		if !bytes.Equal(resA.Stdout, resB.Stdout) {
			rt.Fatalf("raw option stdout mismatch for %s:\n%s", key, cmp.Diff(string(resA.Stdout), string(resB.Stdout)))
		}
	}

	if _, err := os.Stat("/tmp/bad"); err == nil {
		_ = os.Remove("/tmp/bad")

		rt.Fatalf("/tmp/bad was created during metamorphic test")
	}
}

// TestMetamorphicSequenceEquivalence validates that executing an atomic command batch via
// RunSequence produces the exact same terminal daemon state as executing each command sequentially via Run.
func TestMetamorphicSequenceEquivalence(t *testing.T) {
	_ = os.Remove("/tmp/bad")

	t.Cleanup(func() { _ = os.Remove("/tmp/bad") })

	server := tmuxtest.NewServer(t)

	info, err := server.Probe(t.Context())
	if err != nil || !info.Version.AtLeast(3, 6) {
		t.Fatalf("probe failed or tmux version < 3.6: %v (info: %+v)", err, info)
	}

	var trialCounter atomic.Uint64

	rapid.Check(t, func(rt *rapid.T) {
		trialID := trialCounter.Add(1)

		trialCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var createA tmux.NewSessionOptions

		createA.Name = fmt.Sprintf("meta-a-%d", trialID)

		sessA, err := server.NewSession(trialCtx, createA)
		if err != nil {
			rt.Fatalf("create sessA: %v", err)
		}

		defer func() {
			_ = sessA.Kill(context.WithoutCancel(trialCtx))
		}()

		var createB tmux.NewSessionOptions

		createB.Name = fmt.Sprintf("meta-b-%d", trialID)

		sessB, err := server.NewSession(trialCtx, createB)
		if err != nil {
			rt.Fatalf("create sessB: %v", err)
		}

		defer func() {
			_ = sessB.Kill(context.WithoutCancel(trialCtx))
		}()

		cmdCount := rapid.IntRange(2, 6).Draw(rt, "cmdCount")
		cmdsA := make([]tmux.Command, 0, cmdCount)
		cmdsB := make([]tmux.Command, 0, cmdCount)

		for range cmdCount {
			cmdA, cmdB := drawCommandPair(rt, sessA, sessB)
			cmdsA = append(cmdsA, cmdA)
			cmdsB = append(cmdsB, cmdB)
		}

		// Branch A: Atomic Batch
		seq, err := tmux.Sequence(cmdsA...)
		if err != nil {
			rt.Fatalf("tmux.Sequence failed: %v", err)
		}

		resBatch, err := server.RunSequence(trialCtx, seq)
		if err != nil {
			rt.Fatalf("server.RunSequence failed: %v, stderr: %s", err, resBatch.Stderr)
		}

		// Branch B: Sequential Loop
		for idx, cmd := range cmdsB {
			resSeq, err := server.Run(trialCtx, cmd)
			if err != nil {
				rt.Fatalf("server.Run command %d (%v) failed: %v, stderr: %s", idx, cmd, err, resSeq.Stderr)
			}
		}

		assertMetamorphicEquivalence(trialCtx, rt, server, sessA, sessB)
	})
}

// TestMetamorphicSequenceAssociativity validates sequence composition associativity:
// RunSequence(seq1 o seq2) produces identical state to RunSequence(seq1) followed by RunSequence(seq2).
//
//nolint:cyclop,gocognit // metamorphic property verification loop exercises multiple execution branches
func TestMetamorphicSequenceAssociativity(t *testing.T) {
	_ = os.Remove("/tmp/bad")

	t.Cleanup(func() { _ = os.Remove("/tmp/bad") })

	server := tmuxtest.NewServer(t)

	info, err := server.Probe(t.Context())
	if err != nil || !info.Version.AtLeast(3, 6) {
		t.Fatalf("probe failed or tmux version < 3.6: %v (info: %+v)", err, info)
	}

	var trialCounter atomic.Uint64

	rapid.Check(t, func(rt *rapid.T) {
		trialID := trialCounter.Add(1)

		trialCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		var createA tmux.NewSessionOptions

		createA.Name = fmt.Sprintf("meta-assoc-a-%d", trialID)

		sessA, err := server.NewSession(trialCtx, createA)
		if err != nil {
			rt.Fatalf("create sessA: %v", err)
		}

		defer func() {
			_ = sessA.Kill(context.WithoutCancel(trialCtx))
		}()

		var createB tmux.NewSessionOptions

		createB.Name = fmt.Sprintf("meta-assoc-b-%d", trialID)

		sessB, err := server.NewSession(trialCtx, createB)
		if err != nil {
			rt.Fatalf("create sessB: %v", err)
		}

		defer func() {
			_ = sessB.Kill(context.WithoutCancel(trialCtx))
		}()

		seqCount1 := rapid.IntRange(1, 4).Draw(rt, "seqCount1")
		seqCount2 := rapid.IntRange(1, 4).Draw(rt, "seqCount2")

		cmds1A := make([]tmux.Command, 0, seqCount1)

		cmds1B := make([]tmux.Command, 0, seqCount1)
		for range seqCount1 {
			c1A, c1B := drawCommandPair(rt, sessA, sessB)
			cmds1A = append(cmds1A, c1A)
			cmds1B = append(cmds1B, c1B)
		}

		cmds2A := make([]tmux.Command, 0, seqCount2)

		cmds2B := make([]tmux.Command, 0, seqCount2)
		for range seqCount2 {
			c2A, c2B := drawCommandPair(rt, sessA, sessB)
			cmds2A = append(cmds2A, c2A)
			cmds2B = append(cmds2B, c2B)
		}

		// Session A: Single combined sequence
		allCmdsA := append(append([]tmux.Command{}, cmds1A...), cmds2A...)

		seqCombinedA, err := tmux.Sequence(allCmdsA...)
		if err != nil {
			rt.Fatalf("tmux.Sequence combined A failed: %v", err)
		}

		resCombinedA, err := server.RunSequence(trialCtx, seqCombinedA)
		if err != nil {
			rt.Fatalf("server.RunSequence combined A failed: %v, stderr: %s", err, resCombinedA.Stderr)
		}

		// Session B: Sequential execution of seq1B then seq2B
		seq1B, err := tmux.Sequence(cmds1B...)
		if err != nil {
			rt.Fatalf("tmux.Sequence 1B failed: %v", err)
		}

		res1B, err := server.RunSequence(trialCtx, seq1B)
		if err != nil {
			rt.Fatalf("server.RunSequence 1B failed: %v, stderr: %s", err, res1B.Stderr)
		}

		seq2B, err := tmux.Sequence(cmds2B...)
		if err != nil {
			rt.Fatalf("tmux.Sequence 2B failed: %v", err)
		}

		res2B, err := server.RunSequence(trialCtx, seq2B)
		if err != nil {
			rt.Fatalf("server.RunSequence 2B failed: %v, stderr: %s", err, res2B.Stderr)
		}

		assertMetamorphicEquivalence(trialCtx, rt, server, sessA, sessB)
	})
}
