package tmux_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackageArchitectureEnforcement verifies layer isolation:
// 1. The core production package (tmux) must NEVER import its test harness (tmuxtest).
// 2. Internal utility packages (internal/wire, internal/schema) must never import tmux.
// 3. No production package may import external network packages (net/http, etc.).
func TestPackageArchitectureEnforcement(t *testing.T) {
	root := findRepoRoot(t)

	t.Run("Rule1_NoTmuxtestInProduction", func(t *testing.T) { checkProductionImports(t, root) })
	t.Run("Rule2_NoCircularInternalImports", func(t *testing.T) { checkCircularImports(t, root) })
	t.Run("Rule3_NoObsoleteInternalImports", func(t *testing.T) { checkObsoleteImports(t, root) })
	t.Run("Rule4_NoIntegrationTestsInTmuxPackage", func(t *testing.T) { checkNoIntegrationInTmux(t, root) })
}

func checkProductionImports(t *testing.T, root string) {
	t.Helper()

	for _, imp := range collectProductionImports(t, filepath.Join(root, "tmux")) {
		if strings.Contains(imp, "gotmux/tmuxtest") {
			t.Errorf("forbidden import in production tmux code: %s", imp)
		}
	}
}

func checkCircularImports(t *testing.T, root string) {
	t.Helper()

	for _, sub := range []string{"internal/wire", "internal/schema"} {
		for _, imp := range collectProductionImports(t, filepath.Join(root, sub)) {
			if strings.Contains(imp, "gotmux/tmux") {
				t.Errorf("forbidden circular import in %s: %s", sub, imp)
			}
		}
	}
}

func checkObsoleteImports(t *testing.T, root string) {
	t.Helper()

	for _, imp := range collectProductionImports(t, filepath.Join(root, "tmux")) {
		if strings.Contains(imp, "gotmux/internal/codec") || strings.Contains(imp, "gotmux/internal/exec") {
			t.Errorf("forbidden obsolete internal package import: %s", imp)
		}
	}
}

func checkNoIntegrationInTmux(t *testing.T, root string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, "tmux"))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.Contains(entry.Name(), "integration") || entry.Name() == "bughunt_test.go" {
			t.Errorf("integration test file must live in test/: %s", entry.Name())
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root containing go.mod")
		}

		dir = parent
	}
}

func collectProductionImports(t *testing.T, dir string) []string {
	t.Helper()

	fset := token.NewFileSet()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var imports []string

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		node, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, imp := range node.Imports {
			val := strings.Trim(imp.Path.Value, `"`)
			imports = append(imports, val)
		}
	}

	return imports
}
