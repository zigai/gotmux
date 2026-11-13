package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
)

var nameOverrides = map[string]string{
	"set-clipboard":                         "Clipboard",
	"set-titles":                            "Titles",
	"set-titles-string":                     "TitlesString",
	"options_table_pane_border_lines_list":  "PaneBorderLineStyle",
	"options_table_popup_border_lines_list": "MenuBorderLines",
	"options_table_set_clipboard_list":      "Clipboard",
}

type field struct {
	Method string `json:"method"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Min    int64  `json:"min"`
	Max    int64  `json:"max"`
}

type schema struct {
	MinimumVersion string                       `json:"minimum_version"`
	Source         string                       `json:"source"`
	ScopeSources   []string                     `json:"scope_sources"`
	Scopes         map[string][]string          `json:"scopes"`
	Fields         map[string][]field           `json:"fields"`
	Enums          map[string]map[string]string `json:"enums"`
}

func buildSchema(tables []table, versions []string, input schema) (schema, error) {
	if len(tables) != len(versions) || len(tables) == 0 {
		return schema{}, fmt.Errorf("%w: mismatched source lists", errSource)
	}

	result := schema{
		MinimumVersion: input.MinimumVersion,
		Source:         input.Source,
		ScopeSources:   input.ScopeSources,
		Scopes: map[string][]string{
			"ScopeServer":  {},
			"ScopeSession": {},
			"ScopeWindow":  {},
			"ScopePane":    {},
		},
		Fields: map[string][]field{
			"ServerOptions":  {},
			"SessionOptions": {},
			"WindowOptions":  {},
			"PaneOptions":    {},
		},
		Enums: make(map[string]map[string]string),
	}

	assignScopes(&result, tables)

	for _, name := range sortedEntries(tables[0].entries) {
		item := tables[0].entries[name]
		if item.hook || item.array {
			continue
		}

		reason, err := exclusion(item, tables, versions)
		if err != nil {
			return schema{}, err
		}

		if reason != "" {
			fmt.Printf("Excluded %s: %s\n", name, reason)
			continue
		}

		option, err := makeField(item, tables[0].choices, result.Enums)
		if err != nil {
			return schema{}, err
		}

		receiver := receiverName(item.scope)

		result.Fields[receiver] = append(result.Fields[receiver], option)
		if item.scope == "ScopePane" {
			result.Fields["WindowOptions"] = append(result.Fields["WindowOptions"], option)
		}
	}

	return result, nil
}

func assignScopes(result *schema, tables []table) {
	priority := []string{"ScopeServer", "ScopeSession", "ScopeWindow", "ScopePane"}
	selected := make(map[string]string)

	for _, current := range tables {
		for name, item := range current.entries {
			old, exists := selected[name]
			if !exists || slices.Index(priority, item.scope) > slices.Index(priority, old) {
				selected[name] = item.scope
			}
		}
	}

	for name, scope := range selected {
		result.Scopes[scope] = append(result.Scopes[scope], name)
	}

	for _, names := range result.Scopes {
		sort.Strings(names)
	}
}

func exclusion(item entry, tables []table, versions []string) (string, error) {
	baseType, err := goType(item)
	if err != nil {
		return "", err
	}

	for i, current := range tables[1:] {
		other, exists := current.entries[item.name]
		if !exists {
			return "absent in " + versions[i+1], nil
		}

		otherType, err := goType(other)
		if err != nil {
			return "", err
		}

		if otherType != baseType {
			return fmt.Sprintf(
				"%s changes Go type from %s to %s",
				versions[i+1],
				baseType,
				otherType,
			), nil
		}

		if !scopeIncludes(other.scope, item.scope) {
			return fmt.Sprintf(
				"%s changes scope from %s to %s",
				versions[i+1],
				item.scope,
				other.scope,
			), nil
		}

		if other.array || other.hook {
			return "becomes an array or hook in " + versions[i+1], nil
		}
	}

	return "", nil
}

func scopeIncludes(scope string, base string) bool {
	return scope == base || (scope == "ScopePane" && base == "ScopeWindow")
}

func goType(item entry) (string, error) {
	switch item.ctype {
	case "OPTIONS_TABLE_STRING", colourType, "OPTIONS_TABLE_COMMAND":
		return "string", nil
	case "OPTIONS_TABLE_NUMBER":
		return "int", nil
	case "OPTIONS_TABLE_FLAG":
		return "bool", nil
	case "OPTIONS_TABLE_KEY":
		return "Key", nil
	case "OPTIONS_TABLE_CHOICE":
		return choiceType(item.choices), nil
	default:
		return "", fmt.Errorf("%w: unknown option type %s for %s", errSource, item.ctype, item.name)
	}
}

func makeField(
	item entry,
	choices map[string][]string,
	enums map[string]map[string]string,
) (field, error) {
	valueType, err := goType(item)
	if err != nil {
		return field{}, err
	}

	result := field{
		Method: identifier(item.name),
		Name:   item.name,
		Type:   valueType,
		Min:    item.min,
		Max:    item.max,
	}
	if override, exists := nameOverrides[item.name]; exists {
		result.Method = override
	}

	if item.ctype == "OPTIONS_TABLE_CHOICE" {
		name, values, err := choiceEnum(item.choices, choices)
		if err != nil {
			return field{}, fmt.Errorf("%s: %w", item.name, err)
		}

		enums[name] = values
	}

	return result, nil
}

func choiceEnum(list string, choices map[string][]string) (string, map[string]string, error) {
	values, exists := choices[list]
	if !exists || !strings.HasPrefix(list, "options_table_") || !strings.HasSuffix(list, "_list") {
		return "", nil, fmt.Errorf("%w: unknown choice list %s", errSource, list)
	}

	name := choiceType(list)

	constants := make(map[string]string, len(values))
	for _, value := range values {
		constant := name + identifier(value)
		if _, duplicate := constants[constant]; duplicate {
			return "", nil, fmt.Errorf("%w: duplicate enum identifier %s", errSource, constant)
		}

		constants[constant] = value
	}

	return name, constants, nil
}

func choiceType(list string) string {
	if override, ok := nameOverrides[list]; ok {
		return override
	}

	return identifier(strings.TrimSuffix(strings.TrimPrefix(list, "options_table_"), "_list"))
}

func identifier(value string) string {
	var result strings.Builder

	upper := true

	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}

		if upper {
			r = unicode.ToUpper(r)
		}

		result.WriteRune(r)

		upper = false
	}

	return result.String()
}

func receiverName(scope string) string {
	return strings.TrimPrefix(scope, "Scope") + "Options"
}

func sortedEntries(entries map[string]entry) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
