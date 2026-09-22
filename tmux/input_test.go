package tmux

import (
	"context"
	"slices"
	"testing"
)

func TestCaptureFlagsDistinguishEscapesFromEmptyCellTrimming(t *testing.T) {
	var opts CaptureOptions

	opts.IncludeEscapes = true

	flags := captureFlagArgs(opts)

	if !slices.Contains(flags, "-e") || slices.Contains(flags, "-T") {
		t.Fatalf("IncludeEscapes flags = %v", flags)
	}

	opts.IncludeEscapes = false
	opts.TrimEmptyCells = true

	flags = captureFlagArgs(opts)

	if !slices.Contains(flags, "-T") || slices.Contains(flags, "-e") {
		t.Fatalf("TrimEmptyCells flags = %v", flags)
	}
}

func TestCaptureTitleDelimiter(t *testing.T) {
	s, response, _ := mockScriptServer(t)
	p := Pane{h: s.newHandle("%7", PaneKind, mockServerIdentity(s))}

	const marker = "___GOTMUX_CAPTURE_TITLE___"

	title := "first\n" + marker + "\nsecond"
	content := "actual pane contents\n"
	raw := guardOK + marker + "\n" + title + "\n" + marker + "\n" + content
	writeMockResponse(t, response, []byte(raw))

	got, err := p.CaptureWithTitle(context.Background(), CaptureOptions{
		Start:               nil,
		End:                 nil,
		EntireHistory:       false,
		ScrollbackEnd:       false,
		JoinWrapped:         false,
		IncludeEscapes:      false,
		PreserveSpaces:      false,
		PaneState:           false,
		Quiet:               false,
		TrimEmptyCells:      false,
		Screen:              0,
		Buffer:              "",
		MaxBytes:            0,
		EscapeNonPrintable:  false,
		AlternateScreenOnly: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Title != title || string(got.Output) != content {
		t.Fatalf("title=%q output=%q; wanted title=%q output=%q", got.Title, got.Output, title, content)
	}
}
