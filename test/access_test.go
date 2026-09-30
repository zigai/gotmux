//go:build integration

package tmux_test

import (
	"context"
	"os/user"
	"slices"
	"strings"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationServerAccess(t *testing.T) {
	server, _, ctx := apiFixture(t)
	info, _ := server.Probe(ctx)
	t.Logf("Probe version: %+v, raw: %q", info.Version, info.Version.Raw)

	entries := accessEntries(t, ctx, server)
	t.Logf("AccessList entries (%d): %+v", len(entries), entries)

	if len(entries) == 0 {
		t.Fatal("expected at least one access entry for current user/owner")
	}

	// Grant read-only access to a user
	const testUser = "nobody"
	if err := server.GrantAccess(ctx, testUser, tmux.AccessOptions{IsGroup: false, ReadOnly: true}); err != nil {
		t.Fatalf("GrantAccess(RO) failed: %v", err)
	}

	entry := requireAccessEntry(t, ctx, server, testUser)
	if !entry.ReadOnly || entry.IsGroup {
		t.Errorf("expected user %q with ReadOnly=true and IsGroup=false, got: %+v", testUser, entry)
	}

	// Grant read-write access to the same user
	if err := server.GrantAccess(ctx, testUser, tmux.AccessOptions{IsGroup: false, ReadOnly: false}); err != nil {
		t.Fatalf("GrantAccess(RW) failed: %v", err)
	}

	if entry := requireAccessEntry(t, ctx, server, testUser); entry.ReadOnly {
		t.Errorf("expected user %q to have ReadOnly=false, got: %+v", testUser, entry)
	}

	// Revoke user access
	if err := server.RevokeAccess(ctx, testUser, tmux.RevokeAccessOptions{IsGroup: false}); err != nil {
		t.Fatalf("RevokeAccess failed: %v", err)
	}

	if entries := accessEntries(t, ctx, server); slices.ContainsFunc(entries, func(e tmux.AccessEntry) bool { return e.Name == testUser }) {
		t.Fatalf("expected user %q to be removed from access list, but still present: %+v", testUser, entries)
	}

	exerciseGroupAccess(t, ctx, server)
}

func accessEntries(t *testing.T, ctx context.Context, server *tmux.Server) []tmux.AccessEntry {
	t.Helper()

	entries, err := server.AccessList(ctx)
	if err != nil {
		fatalCommand(t, "AccessList", err)
	}

	return entries
}

func requireAccessEntry(t *testing.T, ctx context.Context, server *tmux.Server, name string) tmux.AccessEntry {
	t.Helper()

	entries := accessEntries(t, ctx, server)

	idx := slices.IndexFunc(entries, func(e tmux.AccessEntry) bool { return e.Name == name })
	if idx < 0 {
		t.Fatalf("expected user %q in access list, got: %+v", name, entries)
	}

	return entries[idx]
}

// exerciseGroupAccess is best effort: it only logs a missing group entry.
func exerciseGroupAccess(t *testing.T, ctx context.Context, server *tmux.Server) {
	t.Helper()

	group, ok := currentGroupName()
	if !ok {
		return
	}

	if err := server.GrantAccess(ctx, group, tmux.AccessOptions{IsGroup: true, ReadOnly: true}); err != nil {
		return
	}

	defer func() { _ = server.RevokeAccess(ctx, group, tmux.RevokeAccessOptions{IsGroup: true}) }()

	entries, err := server.AccessList(ctx)
	if err != nil {
		return
	}

	if !slices.ContainsFunc(entries, func(e tmux.AccessEntry) bool { return e.Name == group && e.IsGroup }) {
		t.Logf("group %q not listed as distinct group entry", group)
	}
}

func currentGroupName() (string, bool) {
	current, err := user.Current()
	if err != nil || current.Gid == "" {
		return "", false
	}

	group, err := user.LookupGroupId(current.Gid)
	if err != nil || group.Name == "" || strings.Contains(group.Name, " ") {
		return "", false
	}

	return group.Name, true
}
