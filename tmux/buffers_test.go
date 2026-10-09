package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/gotmux/internal/wire"
)

func TestBufferNamesRejectLineBreaks(t *testing.T) {
	server := localServer(t)

	for _, name := range []string{"buffer\nbroken", "buffer\rbroken", "buffer\n%exit", "buffer\x00broken"} {
		if _, err := NamedBuffer(name); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("NamedBuffer(%q): %v", name, err)
		}

		for _, err := range []error{
			server.SetBuffer(context.Background(), name, []byte("data")),
			server.RenameBuffer(context.Background(), "source", name),
		} {
			if !errors.Is(err, ErrInvalidArgument) || outcomeOf(err).Effect != EffectNotSent {
				t.Errorf("buffer mutation using %q: %v", name, err)
			}
		}
	}

	for _, name := range []string{"buffer space", "buffer\\slash", "buffer-é"} {
		if _, err := NamedBuffer(name); err != nil {
			t.Errorf("valid name %q rejected: %v", name, err)
		}
	}
}

func TestPasteOptions(t *testing.T) {
	flags, err := pasteFlags(PasteOptions{
		Buffer:         "",
		Delete:         false,
		BracketedPaste: false,
		ReplaceEscapes: false,
		StripNewlines:  true,
		Separator:      "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(flags) != 2 || flags[0] != "-s" || flags[1] != "" {
		t.Fatalf("expected [-s \"\"], got %v", flags)
	}

	flagsAll, err := pasteFlags(PasteOptions{
		Buffer:         "b0",
		Delete:         true,
		BracketedPaste: true,
		ReplaceEscapes: true,
		StripNewlines:  false,
		Separator:      "",
	})
	if err != nil {
		t.Fatal(err)
	}

	expectedFlags := []string{"-r", "-p", "-d"}
	if len(flagsAll) != len(expectedFlags) {
		t.Fatalf("expected %v, got %v", expectedFlags, flagsAll)
	}

	for i, f := range expectedFlags {
		if flagsAll[i] != f {
			t.Fatalf("expected flag %d to be %q, got %q", i, f, flagsAll[i])
		}
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
