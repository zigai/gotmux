package tmux

import (
	"context"
	"testing"
)

func TestCaptureTitleDelimiter(t *testing.T) {
	s, response, _ := mockScriptServer(t)
	p := Pane{h: s.newHandle("%7", PaneKind, mockServerIdentity(s))}
	const marker = "___GOTMUX_CAPTURE_TITLE___"
	title := "first\n" + marker + "\nsecond"
	content := "actual pane contents\n"
	raw := guardOK + marker + "\n" + title + "\n" + marker + "\n" + content
	writeMockResponse(t, response, []byte(raw))
	got, err := p.CaptureWithTitle(context.Background(), CaptureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != title || string(got.Output) != content {
		t.Fatalf("title=%q output=%q; wanted title=%q output=%q", got.Title, got.Output, title, content)
	}
}
