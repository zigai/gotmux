package tmux

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zigai/gotmux/internal/wire"
)

const captureTestMarker = captureMarkerPrefix + "TESTTOKEN:"

var errCaptureTestExit = errors.New("exit status 1")

func captureTestBatch(maxBytes int64) captureBatch {
	return captureBatch{marker: captureTestMarker, maxBytes: maxBytes}
}

func capturedBlock(title string, output string) string {
	return captureTestMarker + captureTitle + string(wire.EncodeRecord([]string{title})) +
		output + captureTestMarker + captureEnd
}

func rejectedBlock(title string) string {
	return captureTestMarker + captureTitle + string(wire.EncodeRecord([]string{title}))
}

func missingBlock() string { return captureTestMarker + captureMissing }

func capturePanes(s *Server, ids ...string) []Pane {
	panes := make([]Pane, len(ids))
	for i, id := range ids {
		panes[i] = Pane{h: s.unprobedHandle(id, ObjectKindPane)}
	}

	return panes
}

func TestCaptureDecodeKeepsEachPaneInItsOwnEntry(t *testing.T) {
	s, _, _ := mockScriptServer(t)

	hostile := "first line\n" +
		captureMarkerPrefix + "OTHERTOKEN:" + captureEnd +
		captureMarkerPrefix + "OTHERTOKEN:" + captureMissing +
		wire.RecordPrefix + "0:\n" +
		guardOK +
		"last line\n"
	hostileTitle := "title\n" + captureMarkerPrefix + "OTHERTOKEN:" + captureEnd + "|=more"

	cases := []struct {
		name     string
		data     string
		panes    []string
		outputs  []string
		titles   []string
		missing  []int
		rejected []int
	}{
		{
			name:    "closed_pane_between_live_panes",
			data:    capturedBlock("a", "one\n") + missingBlock() + capturedBlock("c", "three\n"),
			panes:   []string{"%1", "%2", "%3"},
			outputs: []string{"one\n", "", "three\n"},
			titles:  []string{"a", "", "c"},
			missing: []int{1},
		},
		{
			name:    "every_pane_closed",
			data:    missingBlock() + missingBlock(),
			panes:   []string{"%1", "%2"},
			outputs: []string{"", ""},
			titles:  []string{"", ""},
			missing: []int{0, 1},
		},
		{
			name:    "marker_like_text_in_content_and_title",
			data:    capturedBlock(hostileTitle, hostile) + capturedBlock("b", "two\n"),
			panes:   []string{"%1", "%2"},
			outputs: []string{hostile, "two\n"},
			titles:  []string{hostileTitle, "b"},
		},
		{
			name:    "empty_capture",
			data:    capturedBlock("a", "") + capturedBlock("b", "two\n"),
			panes:   []string{"%1", "%2"},
			outputs: []string{"", "two\n"},
			titles:  []string{"a", "b"},
		},
		{
			name:    "output_without_final_newline",
			data:    capturedBlock("a", "\x1b[3") + capturedBlock("b", "two\n"),
			panes:   []string{"%1", "%2"},
			outputs: []string{"\x1b[3", "two\n"},
			titles:  []string{"a", "b"},
		},
		{
			name:     "rejected_capture_before_a_live_pane",
			data:     rejectedBlock("a") + capturedBlock("b", "two\n") + missingBlock(),
			panes:    []string{"%1", "%2", "%3"},
			outputs:  []string{"", "two\n", ""},
			titles:   []string{"a", "b", ""},
			missing:  []int{2},
			rejected: []int{0},
		},
		{
			name:     "rejected_capture_last",
			data:     capturedBlock("a", "one\n") + rejectedBlock("b"),
			panes:    []string{"%1", "%2"},
			outputs:  []string{"one\n", ""},
			titles:   []string{"a", "b"},
			rejected: []int{1},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			panes := capturePanes(s, testCase.panes...)

			got, rejected, ok := captureTestBatch(0).decode([]byte(testCase.data), panes)
			if !ok {
				t.Fatal("decode refused a well-formed reply")
			}

			if !slices.Equal(rejected, testCase.rejected) {
				t.Fatalf("rejected = %v, want %v", rejected, testCase.rejected)
			}

			for i, entry := range got {
				if !entry.Pane.Equal(panes[i]) {
					t.Errorf("entry %d belongs to pane %v, want %v", i, entry.Pane, panes[i])
				}

				if string(entry.Output) != testCase.outputs[i] ||
					entry.Title != testCase.titles[i] {
					t.Errorf(
						"entry %d = title %q output %q, want title %q output %q",
						i,
						entry.Title,
						entry.Output,
						testCase.titles[i],
						testCase.outputs[i],
					)
				}

				if missing := errors.Is(entry.Err, ErrNotFound); missing != slices.Contains(
					testCase.missing,
					i,
				) {
					t.Errorf("entry %d error = %v", i, entry.Err)
				}
			}
		})
	}
}

func TestCaptureDecodeRefusesMalformedReplies(t *testing.T) {
	s, _, _ := mockScriptServer(t)
	panes := capturePanes(s, "%1", "%2")
	valid := capturedBlock("a", "one\n") + capturedBlock("b", "two\n")

	cases := map[string]string{
		"empty":                       "",
		"bytes_before_first_block":    "x" + valid,
		"bytes_after_last_block":      valid + "x",
		"fewer_blocks_than_panes":     capturedBlock("a", "one\n"),
		"more_blocks_than_panes":      valid + missingBlock(),
		"unknown_block_kind":          captureTestMarker + "other\n" + capturedBlock("b", "two\n"),
		"title_is_not_a_record":       captureTestMarker + captureTitle + "a\none\n" + captureTestMarker + captureEnd,
		"output_without_end_marker":   rejectedBlock("a") + "one\n" + capturedBlock("b", "two\n"),
		"cut_inside_the_last_capture": strings.TrimSuffix(valid, captureTestMarker+captureEnd),
	}

	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if got, rejected, ok := captureTestBatch(0).decode([]byte(data), panes); ok {
				t.Fatalf("decode accepted %q as %v (rejected %v)", data, got, rejected)
			}
		})
	}
}

func TestCaptureDecodeBoundsEachPane(t *testing.T) {
	s, _, _ := mockScriptServer(t)
	panes := capturePanes(s, "%1", "%2", "%3")
	data := capturedBlock("a", "0123456789") + capturedBlock("b", "ab") + capturedBlock("c", "tail")

	got, _, ok := captureTestBatch(4).decode([]byte(data), panes)
	if !ok {
		t.Fatal("decode refused a well-formed reply")
	}

	if string(got[0].Output) != "0123" || !errors.Is(got[0].Err, ErrOutputLimit) {
		t.Fatalf("oversized entry = %q, %v", got[0].Output, got[0].Err)
	}

	if string(got[1].Output) != "ab" || got[1].Err != nil {
		t.Fatalf("small entry = %q, %v", got[1].Output, got[1].Err)
	}

	for _, entry := range got[:2] {
		spare := cap(entry.Output) - len(entry.Output)
		_ = append(entry.Output, strings.Repeat("x", spare)...)
	}

	if string(got[1].Output) != "ab" || string(got[2].Output) != "tail" || got[2].Title != "c" {
		t.Fatalf(
			"append to one entry changed another: %q, %q (%q)",
			got[1].Output,
			got[2].Output,
			got[2].Title,
		)
	}
}

func TestCaptureRejectionsNeedANonzeroExit(t *testing.T) {
	s, _, _ := mockScriptServer(t)
	panes := capturePanes(s, "%1", "%2", "%3")
	rejected := rejectedBlock("a") + capturedBlock("b", "two\n") + rejectedBlock("c")
	complete := capturedBlock(
		"a",
		"one\n",
	) + capturedBlock(
		"b",
		"two\n",
	) + capturedBlock(
		"c",
		"x\n",
	)
	stderr := "no alternate screen\nsome other refusal\n"
	exited := &CommandError{
		Command: "sequence",
		Result:  failedResult(),
		Outcome: notSentOutcome(),
		Timeout: TimeoutSourceNone,
		Err:     errCaptureTestExit,
	}

	reply := func(stdout string, exitCode int) Result {
		return Result{Stdout: []byte(stdout), Stderr: []byte(stderr), ExitCode: exitCode}
	}

	got, err := captureTestBatch(0).captures(panes, reply(rejected, 1), exited, nil)
	if err != nil {
		t.Fatalf("rejected captures failed the whole call: %v", err)
	}

	for i, want := range map[int]string{0: "no alternate screen\n", 2: "some other refusal\n"} {
		command, ok := errors.AsType[*CommandError](got[i].Err)
		if !ok || string(command.Result.Stderr) != want {
			t.Errorf("entry %d error = %v, want stderr %q", i, got[i].Err, want)
		}
	}

	if got[1].Err != nil || string(got[1].Output) != "two\n" {
		t.Errorf("live entry = %q, %v", got[1].Output, got[1].Err)
	}

	limited := &CommandError{
		Command: "sequence",
		Result:  failedResult(),
		Outcome: notSentOutcome(),
		Timeout: TimeoutSourceNone,
		Err:     errors.Join(errCaptureTestExit, ErrOutputLimit),
	}

	failures := []struct {
		name   string
		result Result
		err    error
		ctxErr error
		want   error
	}{
		{"clean_exit_with_unfinished_block", reply(rejected, 0), nil, nil, ErrProtocol},
		{"nonzero_exit_with_complete_blocks", reply(complete, 1), exited, nil, exited},
		{"killed_process", reply(rejected, -1), exited, nil, exited},
		{"output_limit", reply(rejected, 1), limited, nil, ErrOutputLimit},
		{"expired_context", reply(rejected, 1), exited, context.DeadlineExceeded, exited},
	}

	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			got, err := captureTestBatch(0).captures(
				panes,
				failure.result,
				failure.err,
				failure.ctxErr,
			)
			if got != nil || !errors.Is(err, failure.want) {
				t.Fatalf("captures = %v, %v; want only %v", got, err, failure.want)
			}
		})
	}
}

func TestCaptureChunksFitTheCommandLimit(t *testing.T) {
	s, _, _ := mockScriptServer(t)

	ids := make([]string, 0, 300)
	for i := range 300 {
		ids = append(ids, "%"+strings.Repeat("9", i%9+1))
	}

	identity := fixtureIdentity(s)
	identity.ReportedSocket = "/tmp/" + strings.Repeat(`a "$b`, 20)

	for name, g := range map[string]*guard{"unguarded": nil, "guarded": newGuard(identity)} {
		t.Run(name, func(t *testing.T) {
			var opts CaptureOptions

			opts.JoinWrapped, opts.IncludeEscapes = true, true

			nodes, err := captureTestBatch(0).nodes(capturePanes(s, ids...), opts)
			if err != nil {
				t.Fatal(err)
			}

			chunks, err := captureChunks(g, nodes)
			if err != nil {
				t.Fatal(err)
			}

			if len(chunks) < 2 {
				t.Fatalf("%d panes fit %d command, want a split", len(ids), len(chunks))
			}

			var joined []wireNode

			for i, chunk := range chunks {
				size, err := commandBytes(g, chunk)
				if err != nil {
					t.Fatal(err)
				}

				if size > captureCommandBytes {
					t.Errorf("chunk %d is %d bytes, limit %d", i, size, captureCommandBytes)
				}

				joined = append(joined, chunk...)
			}

			if len(joined) != len(nodes) {
				t.Fatalf("chunks hold %d panes, want %d", len(joined), len(nodes))
			}

			for i := range joined {
				if joined[i].args[2].text != nodes[i].args[2].text {
					t.Fatalf("pane %d is %q after the split", i, joined[i].args[2].text)
				}
			}
		})
	}
}

func TestCapturePanesRefusesBadRequestsBeforeRunning(t *testing.T) {
	s, _, log := mockScriptServer(t)
	other, _, _ := mockScriptServer(t)

	elsewhere := fixtureIdentity(s)
	elsewhere.PID++
	elsewhere.Started = elsewhere.Started.Add(time.Hour)

	live := Pane{h: s.newHandle("%1", ObjectKindPane, fixtureIdentity(s))}

	var buffered CaptureOptions

	buffered.Buffer = "saved"

	cases := []struct {
		name  string
		panes []Pane
		opts  CaptureOptions
		want  error
	}{
		{"zero_handle", []Pane{live, {}}, CaptureOptions{}, ErrInvalidHandle},
		{"pane_of_another_server", capturePanes(other, "%1"), CaptureOptions{}, ErrInvalidHandle},
		{
			"panes_of_two_daemons",
			[]Pane{live, {h: s.newHandle("%2", ObjectKindPane, elsewhere)}},
			CaptureOptions{},
			ErrInvalidHandle,
		},
		{"capture_into_a_buffer", []Pane{live}, buffered, ErrInvalidArgument},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := s.CapturePanes(context.Background(), testCase.panes, testCase.opts)
			if got != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("CapturePanes = %v, %v; want %v", got, err, testCase.want)
			}
		})
	}

	got, err := s.CapturePanes(context.Background(), nil, CaptureOptions{})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty request = %v, %v", got, err)
	}

	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmux ran for a refused or empty request: %v", err)
	}
}
