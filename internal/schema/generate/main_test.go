package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndCheck(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/schema", "tmux"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile("../options.json")
	if err != nil {
		t.Fatal(err)
	}

	//nolint:gosec // The path is fixed within t.TempDir(), which is private to this test.
	if err := os.WriteFile(filepath.Join(root, "internal/schema/options.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(root, false); err != nil {
		t.Fatal(err)
	}

	if err := run(root, true); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "tmux/options_generated.go")

	stale := []byte("package tmux\n")
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run(root, true); !errors.Is(err, errStale) {
		t.Fatalf("check error = %v, want %v", err, errStale)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(after, stale) {
		t.Fatal("check modified the stale output")
	}
}
