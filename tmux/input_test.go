package tmux

import (
	"context"
	"slices"
	"testing"

	"github.com/zigai/gotmux/internal/wire"
)

func TestCaptureFlagArgs(test *testing.T) {
	cases := []struct {
		name               string
		includeEscapes     bool
		trimEmptyCells     bool
		escapeNonPrintable bool
		alternateOnly      bool
		present            []string
		absent             []string
	}{
		{"escapes", true, false, false, false, []string{"-e"}, []string{"-T"}},
		{"trim_empty_cells", false, true, false, false, []string{"-T"}, []string{"-e"}},
		{"non_printable_and_alternate", false, false, true, true, []string{"-C", "-F"}, nil},
	}

	for _, testCase := range cases {
		test.Run(testCase.name, func(test *testing.T) {
			var options CaptureOptions

			options.IncludeEscapes = testCase.includeEscapes
			options.TrimEmptyCells = testCase.trimEmptyCells
			options.EscapeNonPrintable = testCase.escapeNonPrintable
			options.AlternateScreenOnly = testCase.alternateOnly

			flags := captureFlagArgs(options)
			for _, flag := range testCase.present {
				if !slices.Contains(flags, flag) {
					test.Errorf("flags = %v; missing %s", flags, flag)
				}
			}

			for _, flag := range testCase.absent {
				if slices.Contains(flags, flag) {
					test.Errorf("flags = %v; unexpected %s", flags, flag)
				}
			}
		})
	}
}

func TestCaptureTitleDelimiter(t *testing.T) {
	s, response, _ := mockScriptServer(t)
	p := Pane{h: s.newHandle("%7", ObjectKindPane, mockServerIdentity(s))}

	const marker = "___GOTMUX_CAPTURE_TITLE___"

	title := "first\n" + marker + "\nsecond"
	content := "actual pane contents\n" + marker + "\nmore pane contents\n"

	raw := append([]byte(guardOK), wire.EncodeRecord([]string{title})...)
	raw = append(raw, content...)
	writeMockResponse(t, response, raw)

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
