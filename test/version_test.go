//go:build integration

package tmux_test

import (
	"slices"
	"strings"
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
}

func TestParseVersionForms(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		want          tmux.Version
		atLeast36     bool
		atLeast37     bool
		wantRendering string
	}{
		{"tmux 3.6a", tmux.Version{Raw: "tmux 3.6a", Major: 3, Minor: 6, Patch: "a", Suffix: "", Recognized: true}, true, false, "tmux 3.6a"},
		{"3.6", tmux.Version{Raw: "3.6", Major: 3, Minor: 6, Patch: "", Suffix: "", Recognized: true}, true, false, "3.6"},
		{"3.6-rc1", tmux.Version{Raw: "3.6-rc1", Major: 3, Minor: 6, Patch: "", Suffix: "-rc1", Recognized: false}, true, false, "3.6-rc1"},
	} {
		got := tmux.ParseVersion(tc.raw)
		if got != tc.want || got.String() != tc.wantRendering {
			t.Errorf("ParseVersion(%q) = %+v (String %q), want %+v (String %q)", tc.raw, got, got.String(), tc.want, tc.wantRendering)
		}

		if got.AtLeast(3, 6) != tc.atLeast36 || got.AtLeast(3, 7) != tc.atLeast37 {
			t.Errorf("ParseVersion(%q): AtLeast(3, 6) = %v, AtLeast(3, 7) = %v; want %v, %v", tc.raw, got.AtLeast(3, 6), got.AtLeast(3, 7), tc.atLeast36, tc.atLeast37)
		}
	}
}

func TestIntegrationUsage(t *testing.T) {
	server, _, ctx := apiFixture(t)

	usage, err := server.Usage(ctx)
	if err != nil {
		t.Fatalf("server.Usage failed: %v", err)
	}

	if !strings.HasPrefix(usage, "usage: tmux") {
		t.Fatalf("expected usage to start with 'usage: tmux', got %q", usage)
	}
}
