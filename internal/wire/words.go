package wire

import (
	"errors"
	"strconv"
	"strings"
)

var ErrCommandText = errors.New("unsupported or malformed tmux command text")

// ParseWords decodes tmux's serialized argument form without evaluating any
// variable, format, shell, brace group, or command. Unrecognized syntax fails
// closed; callers retain original payloads when semantic decoding is impossible.
func ParseWords(text string) ([]string, error) {
	words := []string{}

	var b strings.Builder

	quote := byte(0)
	started := false

	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote == 0 && isSpace(c) {
			flushWord(&words, &b, &started)

			continue
		}

		if isWordComment(c, quote, started) {
			break
		}

		if quote == 0 && isWordSeparator(c) {
			return nil, ErrCommandText
		}

		if nextQuote, changed := toggleQuote(c, quote); changed {
			quote = nextQuote
			started = true

			continue
		}

		if canEscape(c, quote) {
			next, err := handleEscape(&b, text, i, &started)
			if err != nil {
				return nil, err
			}

			i = next

			continue
		}

		if c == 0 {
			return nil, ErrCommandText
		}

		b.WriteByte(c)

		started = true
	}

	if quote != 0 {
		return nil, ErrCommandText
	}

	flushWord(&words, &b, &started)

	return words, nil
}

func SplitSequence(text string) ([]string, error) {
	return splitSequence(text, false)
}

func SplitBindingSequence(text string) ([]string, error) {
	return splitSequence(text, true)
}

func splitSequence(text string, escapedSeparator bool) ([]string, error) {
	out := []string{}
	start := 0
	quote := byte(0)
	wordStarted := false

	for i := 0; i < len(text); i++ {
		c := text[i]
		if isWordComment(c, quote, wordStarted) {
			text = text[:i]
			break
		}

		if quote == 0 {
			nextI, nextStart, handled, err := handleUnquotedSequenceChar(text, start, i, c, escapedSeparator, &out)
			if err != nil {
				return nil, err
			}

			if handled {
				i = nextI
				start = nextStart
				wordStarted = false

				continue
			}
		}

		if canEscape(c, quote) {
			next, err := skipEscape(text, i)
			if err != nil {
				return nil, err
			}

			i = next
			wordStarted = true

			continue
		}

		quote, _ = toggleQuote(c, quote)
		wordStarted = quote != 0 || !isSpace(c)
	}

	if quote != 0 {
		return nil, ErrCommandText
	}

	part := strings.TrimSpace(text[start:])
	if part != "" {
		out = append(out, part)
	}

	return out, nil
}

func isWordComment(c, quote byte, started bool) bool {
	return c == '#' && quote == 0 && !started
}

func handleUnquotedSequenceChar(text string, start, i int, c byte, escapedSeparator bool, out *[]string) (int, int, bool, error) {
	if isSequenceInvalid(c) {
		return 0, 0, false, ErrCommandText
	}

	if c == ';' || (escapedSeparator && c == '\\' && i+1 < len(text) && text[i+1] == ';') {
		advance := 0
		if c == '\\' {
			advance = 1
		}

		nextI, nextStart, err := parseSequencePart(text, start, i, advance, out)

		return nextI, nextStart, true, err
	}

	return i, start, false, nil
}

func parseSequencePart(text string, start, i, advance int, out *[]string) (int, int, error) {
	part, err := extractPart(text, start, i)
	if err != nil {
		return 0, 0, err
	}

	*out = append(*out, part)

	return i + advance, i + advance + 1, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t'
}

func flushWord(words *[]string, b *strings.Builder, started *bool) {
	if *started {
		*words = append(*words, b.String())
		b.Reset()

		*started = false
	}
}

func canEscape(c, quote byte) bool {
	return c == '\\' && quote != '\''
}

func handleEscape(b *strings.Builder, text string, i int, started *bool) (int, error) {
	bval, next, ok, err := parseEscape(text, i)
	if err != nil {
		return 0, err
	}

	if ok {
		b.WriteByte(bval)

		*started = true
	}

	return next, nil
}

func extractPart(text string, start, end int) (string, error) {
	part := strings.TrimSpace(text[start:end])
	if part == "" {
		return "", ErrCommandText
	}

	return part, nil
}

func isWordSeparator(c byte) bool {
	switch c {
	case '\n', ';', '{', '}':
		return true
	default:
		return false
	}
}

func isSequenceInvalid(c byte) bool {
	switch c {
	case '{', '}', '\n':
		return true
	default:
		return false
	}
}

func toggleQuote(c byte, quote byte) (byte, bool) {
	if c == '\'' && quote != '"' {
		if quote == '\'' {
			return 0, true
		}

		return '\'', true
	}

	if c == '"' && quote != '\'' {
		if quote == '"' {
			return 0, true
		}

		return '"', true
	}

	return quote, false
}

func skipEscape(text string, i int) (int, error) {
	if i+1 >= len(text) {
		return 0, ErrCommandText
	}

	return i + 1, nil
}

func parseOctalEscape(text string, next int) (byte, int, error) {
	const octalLen = 2
	if next+octalLen >= len(text) {
		return 0, 0, ErrCommandText
	}

	s := text[next : next+octalLen+1]
	for j := range s {
		if s[j] < '0' || s[j] > '7' {
			return 0, 0, ErrCommandText
		}
	}

	n, e := strconv.ParseUint(s, 8, 8)
	if e != nil || n == 0 {
		return 0, 0, ErrCommandText
	}

	return byte(n), next + octalLen, nil
}

func mapEscapeChar(c byte) (byte, bool) {
	switch c {
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'v':
		return '\v', true
	case 'a':
		return '\a', true
	case 'e':
		return '\x1b', true
	case 's':
		return ' ', true
	case '\n':
		return 0, false
	default:
		return c, true
	}
}

func parseEscape(text string, i int) (byte, int, bool, error) {
	next := i + 1
	if next >= len(text) {
		return 0, 0, false, ErrCommandText
	}

	c := text[next]
	if c >= '0' && c <= '7' {
		b, nextPos, err := parseOctalEscape(text, next)

		return b, nextPos, err == nil, err
	}

	b, ok := mapEscapeChar(c)

	return b, next, ok, nil
}
