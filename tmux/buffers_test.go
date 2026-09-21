package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/gotmux/internal/wire"
)

func TestPasteOptions(t *testing.T) {
	flags, err := pasteFlags(PasteOptions{
		Buffer:            "",
		Delete:            false,
		DeleteAfter:       false,
		BracketedPaste:    false,
		Bracketed:         false,
		ReplaceEscapes:    false,
		RawNewlines:       false,
		StripNewlines:     true,
		NoTrailingNewline: false,
		Separator:         "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(flags) != 2 || flags[0] != "-s" || flags[1] != "" {
		t.Fatalf("expected [-s \"\"], got %v", flags)
	}

	flagsLegacy, err := pasteFlags(PasteOptions{
		Buffer:            "",
		Delete:            false,
		DeleteAfter:       false,
		BracketedPaste:    false,
		Bracketed:         false,
		ReplaceEscapes:    false,
		RawNewlines:       false,
		StripNewlines:     false,
		NoTrailingNewline: true,
		Separator:         "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(flagsLegacy) != 2 || flagsLegacy[0] != "-s" || flagsLegacy[1] != "" {
		t.Fatalf("expected [-s \"\"], got %v", flagsLegacy)
	}
}

func TestBoundBufferReadRequiresGuard(t *testing.T) {
	s, _, _ := mockScriptServer(t)
	id := mockServerIdentity(s)
	s.bound = &id

	g, _ := s.resolveGuard(plainPlan(command("show-buffer", "-b", "named")), nil)
	if g == nil {
		t.Fatal("bound server explicitly omits the daemon guard for show-buffer")
	}
}

func TestEmptySetBufferRejected(t *testing.T) {
	s, response, _ := mockScriptServer(t)
	probe := wire.EncodeRecord([]string{"42", "100", s.endpoint.String(), "3.6"})
	writeMockResponse(t, response, probe)
	marker := filepath.Join(filepath.Dir(response), "probed")

	script := fmt.Sprintf("#!/bin/sh\nif [ ! -f '%s' ]; then : > '%s'; exec /bin/cat '%s'; fi\nprintf 'TGO-GUARD-1:ok\\n'\n", marker, marker, response)
	if err := os.WriteFile(s.config.Binary, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(s.config.Binary, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := s.SetBufferWith(context.Background(), "named", nil, SetBufferOptions{Append: false}); err == nil {
		t.Fatal("empty set-buffer accepted and acknowledged as success, although tmux leaves the old buffer unchanged")
	}
}
