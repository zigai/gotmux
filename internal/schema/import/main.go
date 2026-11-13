package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

var errImport = errors.New("option import failed")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	input, err := readInputs()
	if err != nil {
		return err
	}

	tables, urls, err := fetchTables(input)
	if err != nil {
		return err
	}

	result, err := buildSchema(tables, urls, input)
	if err != nil {
		return err
	}

	if err := checkNames(result); err != nil {
		return err
	}

	report(result, tables, urls)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schema: %w", err)
	}

	data = append(data, '\n')
	if err := os.WriteFile("internal/schema/options.json", data, 0o644); err != nil {
		return fmt.Errorf("write schema: %w", err)
	}

	return nil
}

func readInputs() (schema, error) {
	data, err := os.ReadFile("internal/schema/options.json")
	if err != nil {
		return schema{}, fmt.Errorf("read schema inputs: %w", err)
	}

	var input schema
	if err := json.Unmarshal(data, &input); err != nil {
		return schema{}, fmt.Errorf("decode schema inputs: %w", err)
	}

	if input.MinimumVersion == "" || input.Source == "" || len(input.ScopeSources) == 0 {
		return schema{}, fmt.Errorf(
			"%w: missing minimum_version, source, or scope_sources",
			errImport,
		)
	}

	return input, nil
}

func fetchTables(input schema) ([]table, []string, error) {
	urls := []string{input.Source}
	for _, source := range input.ScopeSources {
		if !slices.Contains(urls, source) {
			urls = append(urls, source)
		}
	}

	tables := make([]table, 0, len(urls))

	client := &http.Client{}
	for _, url := range urls {
		source, err := fetch(client, url)
		if err != nil {
			return nil, nil, err
		}

		parsed, err := parseSource(source)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", url, err)
		}

		tables = append(tables, parsed)
	}

	return tables, urls, nil
}

func fetch(client *http.Client, url string) (string, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close response:", err)
		}
	}()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: fetch %s: %s", errImport, url, response.Status)
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", url, err)
	}

	return string(data), nil
}

func checkNames(spec schema) error {
	names, err := sourceNames()
	if err != nil {
		return err
	}

	for enum, values := range spec.Enums {
		if err := reserve(names, enum, "enum "+enum); err != nil {
			return err
		}

		for constant := range values {
			if err := reserve(names, constant, "enum "+enum); err != nil {
				return err
			}
		}
	}

	for receiver, fields := range spec.Fields {
		if err := checkMethods(receiver, fields); err != nil {
			return err
		}
	}

	return nil
}

func sourceNames() (map[string]string, error) {
	files, err := filepath.Glob("tmux/*.go")
	if err != nil {
		return nil, fmt.Errorf("list Go sources: %w", err)
	}

	names := make(map[string]string)

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") || filepath.Base(file) == "options_generated.go" {
			continue
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}

		for name := range parsed.Scope.Objects {
			names[name] = file
		}
	}

	return names, nil
}

func checkMethods(receiver string, fields []field) error {
	methods := make(map[string]string)
	for _, name := range []string{
		"Get", "Set", "SetWith", "Unset", "UnsetWith", "List", "ListWith", "User",
		"SetUser", "SetUserWith", "UnsetUser", "UnsetUserWith", "Array", "UpdateArray",
	} {
		methods[name] = "generic method"
	}

	for _, option := range fields {
		for _, method := range []string{option.Method, "Set" + option.Method, "Unset" + option.Method} {
			if err := reserve(methods, method, receiver+" "+option.Name); err != nil {
				return err
			}
		}
	}

	return nil
}

func reserve(names map[string]string, name string, owner string) error {
	if previous, exists := names[name]; exists {
		return fmt.Errorf(
			"%w: identifier %s collides between %s and %s",
			errImport,
			name,
			previous,
			owner,
		)
	}

	names[name] = owner

	return nil
}

func report(spec schema, tables []table, versions []string) {
	keys := make([]string, 0, len(nameOverrides))
	for key := range nameOverrides {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		fmt.Printf("Override %s => %s\n", key, nameOverrides[key])
	}

	for _, name := range sortedEntries(tables[0].entries) {
		item := tables[0].entries[name]
		if item.hook {
			fmt.Printf("Excluded %s: hook\n", name)
		} else if item.array {
			fmt.Printf("Excluded %s: array\n", name)
		}
	}

	for receiver, fields := range spec.Fields {
		fmt.Printf("Typed %s: %d\n", receiver, len(fields))
	}

	for _, list := range sortedLists(tables[0].choices) {
		base := strings.Join(tables[0].choices[list], ",")
		for i, current := range tables[1:] {
			if values, ok := current.choices[list]; ok && strings.Join(values, ",") != base {
				fmt.Printf("Choice %s in %s: %s\n", list, versions[i+1], strings.Join(values, ","))
			}
		}
	}
}

func sortedLists(lists map[string][]string) []string {
	keys := make([]string, 0, len(lists))
	for key := range lists {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
