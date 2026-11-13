package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/scanner"
)

const (
	colourType = "OPTIONS_TABLE_COLOUR"    //nolint:misspell // Exact external tmux C identifier.
	colourFlag = "OPTIONS_TABLE_IS_COLOUR" //nolint:misspell // Exact external tmux C identifier.
)

var errSource = errors.New("unsupported or malformed tmux source")

type sourceToken struct {
	text string
	kind rune
}

type macro struct {
	params []string
	body   []sourceToken
}

type entry struct {
	name    string
	ctype   string
	scope   string
	choices string
	array   bool
	hook    bool
	min     int64
	max     int64
}

type table struct {
	entries map[string]entry
	choices map[string][]string
}

func parseSource(source string) (table, error) {
	clean, macros, err := preprocess(source)
	if err != nil {
		return table{}, err
	}

	tokens, err := tokenize(clean)
	if err != nil {
		return table{}, err
	}

	blocks, err := initializers(tokens)
	if err != nil {
		return table{}, err
	}

	result := table{entries: make(map[string]entry), choices: make(map[string][]string)}

	for name, body := range blocks {
		if name == "options_table" {
			err = parseEntries(body, macros, result.entries)
		} else {
			result.choices[name], err = parseChoices(body)
		}

		if err != nil {
			return table{}, fmt.Errorf("%s: %w", name, err)
		}
	}

	if len(result.entries) == 0 || len(result.choices) == 0 {
		return table{}, fmt.Errorf("%w: missing option table or choice lists", errSource)
	}

	if err := validateTable(result); err != nil {
		return table{}, err
	}

	return result, nil
}

func initializers(tokens []sourceToken) (map[string][]sourceToken, error) {
	blocks := make(map[string][]sourceToken)

	for i := 0; i < len(tokens); i++ {
		name := tokens[i].text
		if name != "options_table" && !strings.HasSuffix(name, "_list") {
			continue
		}

		offset := initializerOffset(tokens[i+1:])
		if offset == 0 {
			continue
		}

		body, end, err := enclosed(tokens, i+offset, "{", "}")
		if err != nil {
			return nil, err
		}

		if _, exists := blocks[name]; exists {
			return nil, fmt.Errorf("%w: duplicate initializer %s", errSource, name)
		}

		blocks[name] = body
		i = end
	}

	return blocks, nil
}

func initializerOffset(tokens []sourceToken) int {
	prefix := [...]string{"[", "]", "=", "{"}
	if len(tokens) < len(prefix) {
		return 0
	}

	for i, text := range prefix {
		if tokens[i].text != text {
			return 0
		}
	}

	return len(prefix)
}

func validateTable(source table) error {
	for _, item := range source.entries {
		switch item.ctype {
		case "OPTIONS_TABLE_STRING", "OPTIONS_TABLE_NUMBER", "OPTIONS_TABLE_FLAG",
			colourType, "OPTIONS_TABLE_KEY", "OPTIONS_TABLE_COMMAND":
			if item.choices != "" {
				return fmt.Errorf("%w: non-choice %s has choices", errSource, item.name)
			}
		case "OPTIONS_TABLE_CHOICE":
			if len(source.choices[item.choices]) == 0 {
				return fmt.Errorf(
					"%w: %s has unknown choice list %s",
					errSource,
					item.name,
					item.choices,
				)
			}
		default:
			return fmt.Errorf("%w: %s has unknown type %s", errSource, item.name, item.ctype)
		}

		if item.ctype == "OPTIONS_TABLE_NUMBER" && item.min > item.max {
			return fmt.Errorf("%w: invalid numeric range for %s", errSource, item.name)
		}
	}

	return nil
}

func preprocess(source string) (string, map[string]macro, error) {
	// Translation phase two joins physical lines before directives are interpreted.
	source = strings.ReplaceAll(source, "\\\n", "")
	lines := strings.Split(source, "\n")
	macros := make(map[string]macro)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}

		lines[i] = ""

		if !strings.HasPrefix(line, "#define OPTIONS_TABLE_") || !strings.Contains(line, "HOOK(") {
			continue
		}

		tokens, err := tokenize(strings.TrimPrefix(line, "#define "))
		if err != nil {
			return "", nil, err
		}

		if len(tokens) < 4 || tokens[1].text != "(" {
			return "", nil, fmt.Errorf("%w: invalid hook definition", errSource)
		}

		params, end, err := enclosed(tokens, 1, "(", ")")
		if err != nil {
			return "", nil, err
		}

		definition := macro{params: nil, body: tokens[end+1:]}
		for _, param := range splitTokens(params) {
			if len(param) != 1 || param[0].kind != scanner.Ident {
				return "", nil, fmt.Errorf("%w: invalid macro parameter", errSource)
			}

			definition.params = append(definition.params, param[0].text)
		}

		macros[tokens[0].text] = definition
	}

	return strings.Join(lines, "\n"), macros, nil
}

func tokenize(source string) ([]sourceToken, error) {
	var (
		lexer   scanner.Scanner
		scanErr error
	)

	lexer.Init(strings.NewReader(source))
	lexer.Mode = scanner.ScanIdents | scanner.ScanInts | scanner.ScanStrings |
		scanner.ScanChars | scanner.ScanComments | scanner.SkipComments
	lexer.Error = func(s *scanner.Scanner, message string) {
		scanErr = fmt.Errorf("%w at %s: %s", errSource, s.Position, message)
	}

	var tokens []sourceToken
	for kind := lexer.Scan(); kind != scanner.EOF; kind = lexer.Scan() {
		tokens = append(tokens, sourceToken{text: lexer.TokenText(), kind: kind})
	}

	return tokens, scanErr
}

func enclosed(
	tokens []sourceToken,
	start int,
	open string,
	closer string,
) ([]sourceToken, int, error) {
	depth := 0

	for i := start; i < len(tokens); i++ {
		switch tokens[i].text {
		case open:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return tokens[start+1 : i], i, nil
			}
		}
	}

	return nil, 0, fmt.Errorf("%w: unclosed %s", errSource, open)
}

func splitTokens(tokens []sourceToken) [][]sourceToken {
	var result [][]sourceToken

	start, depth := 0, 0

	for i, item := range tokens {
		switch item.text {
		case "(", "{", "[":
			depth++
		case ")", "}", "]":
			depth--
		case ",":
			if depth == 0 {
				result = append(result, tokens[start:i])
				start = i + 1
			}
		}
	}

	if start < len(tokens) {
		result = append(result, tokens[start:])
	}

	return result
}

func parseChoices(tokens []sourceToken) ([]string, error) {
	parts := splitTokens(tokens)
	if len(parts) < 2 || tokenText(parts[len(parts)-1]) != "NULL" {
		return nil, fmt.Errorf("%w: choice list lacks NULL terminator", errSource)
	}

	values := make([]string, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		value, err := stringValue(part)
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

func parseEntries(tokens []sourceToken, macros map[string]macro, entries map[string]entry) error {
	parts := splitTokens(tokens)
	if len(parts) == 0 || tokenText(parts[len(parts)-1]) != "{.name=NULL}" {
		return fmt.Errorf("%w: option table lacks sentinel", errSource)
	}

	for _, part := range parts[:len(parts)-1] {
		expanded, err := expandEntry(part, macros, make(map[string]bool))
		if err != nil {
			return err
		}

		item, err := parseEntry(expanded)
		if err != nil {
			return err
		}

		if _, exists := entries[item.name]; exists {
			return fmt.Errorf("%w: duplicate option %s", errSource, item.name)
		}

		entries[item.name] = item
	}

	return nil
}

func expandEntry(
	tokens []sourceToken,
	macros map[string]macro,
	active map[string]bool,
) ([]sourceToken, error) {
	if len(tokens) != 0 && tokens[0].text == "{" {
		return tokens, nil
	}

	if len(tokens) < 3 || tokens[1].text != "(" {
		return nil, fmt.Errorf("%w: invalid hook invocation %s", errSource, tokenText(tokens))
	}

	definition, exists := macros[tokens[0].text]
	if !exists {
		return nil, fmt.Errorf("%w: unknown macro %s", errSource, tokens[0].text)
	}

	if active[tokens[0].text] {
		return nil, fmt.Errorf("%w: recursive macro %s", errSource, tokens[0].text)
	}

	active[tokens[0].text] = true

	expanded, err := expandMacro(tokens, definition)
	if err != nil {
		return nil, err
	}

	return expandEntry(expanded, macros, active)
}

func expandMacro(tokens []sourceToken, definition macro) ([]sourceToken, error) {
	args, end, err := enclosed(tokens, 1, "(", ")")
	if err != nil {
		return nil, err
	}

	parts := splitTokens(args)
	if end != len(tokens)-1 || len(parts) != len(definition.params) {
		return nil, fmt.Errorf("%w: macro arguments %s", errSource, tokens[0].text)
	}

	bindings := make(map[string][]sourceToken, len(parts))
	for i, param := range definition.params {
		bindings[param] = parts[i]
	}

	var expanded []sourceToken

	for _, item := range definition.body {
		if replacement, ok := bindings[item.text]; ok {
			expanded = append(expanded, replacement...)
		} else {
			expanded = append(expanded, item)
		}
	}

	return expanded, nil
}

func parseEntry(tokens []sourceToken) (entry, error) {
	attrs, err := parseAttributes(tokens)
	if err != nil {
		return entry{}, err
	}

	name, err := stringValue(attrs["name"])
	if err != nil {
		return entry{}, err
	}

	scope, err := parseScope(attrs["scope"])
	if err != nil {
		return entry{}, fmt.Errorf("%s: %w", name, err)
	}

	flags := tokenText(attrs["flags"])
	if err := validateFlags(flags); err != nil {
		return entry{}, err
	}

	if tokenText(attrs["type"]) == "OPTIONS_TABLE_NUMBER" {
		if len(attrs["minimum"]) == 0 || len(attrs["maximum"]) == 0 {
			return entry{}, fmt.Errorf("%w: %s has incomplete numeric bounds", errSource, name)
		}
	}

	minimum, err := numericValue(attrs["minimum"])
	if err != nil {
		return entry{}, fmt.Errorf("%s minimum: %w", name, err)
	}

	maximum, err := numericValue(attrs["maximum"])
	if err != nil {
		return entry{}, fmt.Errorf("%s maximum: %w", name, err)
	}

	return entry{
		name:  name,
		ctype: tokenText(attrs["type"]),
		scope: scope,
		choices: tokenText(
			attrs["choices"],
		),
		array: strings.Contains(flags, "OPTIONS_TABLE_IS_ARRAY"),
		hook:  strings.Contains(flags, "OPTIONS_TABLE_IS_HOOK"),
		min:   minimum,
		max:   maximum,
	}, nil
}

func parseAttributes(tokens []sourceToken) (map[string][]sourceToken, error) {
	if len(tokens) < 2 || tokens[0].text != "{" || tokens[len(tokens)-1].text != "}" {
		return nil, fmt.Errorf("%w: invalid option entry", errSource)
	}

	attrs := make(map[string][]sourceToken)

	for _, field := range splitTokens(tokens[1 : len(tokens)-1]) {
		if len(field) < 4 || field[0].text != "." || field[1].kind != scanner.Ident ||
			field[2].text != "=" {
			return nil, fmt.Errorf("%w: invalid designated field %s", errSource, tokenText(field))
		}

		name := field[1].text
		if _, exists := attrs[name]; exists {
			return nil, fmt.Errorf("%w: duplicate field %s", errSource, name)
		}

		attrs[name] = field[3:]
	}

	return attrs, nil
}

func validateFlags(flags string) error {
	if flags == "" {
		return nil
	}

	for flag := range strings.SplitSeq(flags, "|") {
		switch flag {
		case "OPTIONS_TABLE_IS_ARRAY",
			"OPTIONS_TABLE_IS_HOOK",
			"OPTIONS_TABLE_IS_STYLE",
			colourFlag:
		default:
			return fmt.Errorf("%w: unknown flag %s", errSource, flag)
		}
	}

	return nil
}

func parseScope(tokens []sourceToken) (string, error) {
	switch tokenText(tokens) {
	case "OPTIONS_TABLE_SERVER":
		return "ScopeServer", nil
	case "OPTIONS_TABLE_SESSION":
		return "ScopeSession", nil
	case "OPTIONS_TABLE_WINDOW":
		return "ScopeWindow", nil
	case "OPTIONS_TABLE_WINDOW|OPTIONS_TABLE_PANE":
		return "ScopePane", nil
	default:
		return "", fmt.Errorf("%w: unknown scope %s", errSource, tokenText(tokens))
	}
}

func numericValue(tokens []sourceToken) (int64, error) {
	value := tokenText(tokens)
	switch value {
	case "":
		return 0, nil
	case "INT_MAX":
		return math.MaxInt32, nil
	case "UINT_MAX":
		return math.MaxUint32, nil
	case "SHRT_MAX":
		return math.MaxInt16, nil
	case "USHRT_MAX":
		return math.MaxUint16, nil
	case "INPUT_BUF_DEFAULT_SIZE":
		// tmux 3.6 tmux.h defines the default input buffer as one MiB.
		const inputBufferSize = 1 << 20
		return inputBufferSize, nil
	}

	number, err := strconv.ParseInt(value, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: unknown numeric expression %q: %w", errSource, value, err)
	}

	return number, nil
}

func stringValue(tokens []sourceToken) (string, error) {
	if len(tokens) == 0 {
		return "", fmt.Errorf("%w: missing string", errSource)
	}

	var value strings.Builder

	for _, item := range tokens {
		if item.kind != scanner.String {
			return "", fmt.Errorf("%w: expected string, got %s", errSource, item.text)
		}

		part, err := strconv.Unquote(item.text)
		if err != nil {
			return "", fmt.Errorf("decode C string: %w", err)
		}

		value.WriteString(part)
	}

	return value.String(), nil
}

func tokenText(tokens []sourceToken) string {
	var result strings.Builder
	for _, item := range tokens {
		result.WriteString(item.text)
	}

	return result.String()
}
