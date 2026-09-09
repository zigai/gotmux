// Command generate creates scoped option accessors from the option schema.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed options.go.tmpl
var source string

var errStale = errors.New("options_generated.go is stale; run just generate")

type option struct {
	Method string `json:"method"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Min    int64  `json:"min"`
	Max    int64  `json:"max"`
}

type schema struct {
	Fields map[string][]option          `json:"fields"`
	Enums  map[string]map[string]string `json:"enums"`
}

func main() {
	check := flag.Bool("check", false, "check generated code without writing")
	root := flag.String("root", ".", "repository root")

	flag.Parse()

	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	data, err := os.ReadFile(filepath.Join(root, "internal/schema/options.json"))
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}

	var spec schema
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}

	generated, err := render(spec)
	if err != nil {
		return err
	}

	path := filepath.Join(root, "tmux/options_generated.go")
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read generated code: %w", err)
		}

		if !bytes.Equal(current, generated) {
			return errStale
		}

		return nil
	}

	//nolint:gosec // Generated Go source is a public repository artifact, readable by all users.
	if err := os.WriteFile(path, generated, 0o644); err != nil {
		return fmt.Errorf("write generated code: %w", err)
	}

	return nil
}

func render(spec schema) ([]byte, error) {
	tmpl, err := template.New("options").Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, spec); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	result, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated code: %w", err)
	}

	return result, nil
}
