//go:build integration

package tmux_test

import (
	"slices"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func validIdentity(id tmux.ServerIdentity) bool {
	return id.PID > 0 && !id.Started.IsZero() && id.ReportedSocket != ""
}

func TestCapabilities(t *testing.T) {
	server, _, ctx := apiFixture(t)

	caps, err := server.Capabilities(ctx)
	if err != nil {
		t.Fatalf("server.Capabilities failed: %v", err)
	}

	if !validIdentity(caps.Identity) {
		t.Errorf("expected caps.Identity to be valid, got %+v", caps.Identity)
	}

	if !caps.Version.AtLeast(3, 6) {
		t.Errorf("expected caps.Version.AtLeast(3, 6) to be true, got %v", caps.Version)
	}

	for _, cmd := range []string{"new-session", "list-panes", "split-window"} {
		if !caps.HasCommand(cmd) {
			t.Errorf("expected caps.HasCommand(%q) to be true", cmd)
		}
	}

	if caps.HasCommand("nonexistent-cmd-foo") {
		t.Errorf("expected caps.HasCommand(%q) to be false", "nonexistent-cmd-foo")
	}

	if len(caps.Commands) == 0 {
		t.Fatal("expected caps.Commands to be non-empty")
	}

	if !slices.IsSorted(caps.Commands) {
		t.Errorf("expected caps.Commands to be sorted")
	}

	// Also test ParseVersion.String() and Version(ctx)
	ver, err := server.Version(ctx)
	if err != nil {
		t.Fatalf("server.Version failed: %v", err)
	}
	if !ver.AtLeast(3, 6) {
		t.Errorf("expected server.Version.AtLeast(3, 6) to be true, got %v", ver)
	}
	if ver.String() == "" {
		t.Errorf("expected non-empty ver.String()")
	}

	parsed := tmux.ParseVersion("tmux 3.6a")
	if parsed.String() != "tmux 3.6a" {
		t.Errorf("expected parsed.String() == %q, got %q", "tmux 3.6a", parsed.String())
	}
	if parsed.Major != 3 || parsed.Minor != 6 || parsed.Patch != "a" || !parsed.Recognized {
		t.Errorf("unexpected ParseVersion result: %+v", parsed)
	}
	if !parsed.AtLeast(3, 6) {
		t.Errorf("expected parsed.AtLeast(3, 6) to be true")
	}
	if parsed.AtLeast(3, 7) {
		t.Errorf("expected parsed.AtLeast(3, 7) to be false")
	}

	parsedPlain := tmux.ParseVersion("3.6")
	if parsedPlain.String() != "3.6" {
		t.Errorf("expected parsedPlain.String() == %q, got %q", "3.6", parsedPlain.String())
	}
	if parsedPlain.Major != 3 || parsedPlain.Minor != 6 || parsedPlain.Patch != "" || !parsedPlain.Recognized {
		t.Errorf("unexpected ParseVersion result: %+v", parsedPlain)
	}

	unrecognized := tmux.ParseVersion("3.6-rc1")
	if unrecognized.Recognized {
		t.Errorf("expected unrecognized version with suffix to have Recognized=false")
	}
	if unrecognized.AtLeast(3, 6) {
		t.Errorf("expected unrecognized version AtLeast to return false")
	}
}
