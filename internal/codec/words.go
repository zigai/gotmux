package codec

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
	flush := func() {
		if started {
			words = append(words, b.String())
			b.Reset()
			started = false
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote == 0 && (c == ' ' || c == '\t') {
			flush()
			continue
		}
		if quote == 0 && (c == '\n' || c == ';' || c == '{' || c == '}') {
			return nil, ErrCommandText
		}
		if c == '\'' && quote != '"' {
			if quote == '\'' {
				quote = 0
			} else {
				quote = '\''
			}
			started = true
			continue
		}
		if c == '"' && quote != '\'' {
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			started = true
			continue
		}
		if c == '\\' && quote != '\'' {
			i++
			if i >= len(text) {
				return nil, ErrCommandText
			}
			c = text[i]
			if c >= '0' && c <= '7' {
				if i+2 >= len(text) {
					return nil, ErrCommandText
				}
				s := text[i : i+3]
				for j := range s {
					if s[j] < '0' || s[j] > '7' {
						return nil, ErrCommandText
					}
				}
				n, e := strconv.ParseUint(s, 8, 8)
				if e != nil || n == 0 {
					return nil, ErrCommandText
				}
				b.WriteByte(byte(n))
				i += 2
				started = true
				continue
			}
			switch c {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'v':
				c = '\v'
			case 'a':
				c = '\a'
			case '\n':
				continue
			}
			b.WriteByte(c)
			started = true
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
	flush()
	return words, nil
}

func SplitSequence(text string) ([]string, error) {
	out := []string{}
	start := 0
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\\' && quote != '\'' {
			i++
			if i >= len(text) {
				return nil, ErrCommandText
			}
			continue
		}
		if c == '\'' && quote != '"' {
			if quote == '\'' {
				quote = 0
			} else {
				quote = '\''
			}
			continue
		}
		if c == '"' && quote != '\'' {
			if quote == '"' {
				quote = 0
			} else {
				quote = '"'
			}
			continue
		}
		if quote == 0 {
			if c == '{' || c == '}' || c == '\n' {
				return nil, ErrCommandText
			}
			if c == ';' {
				part := strings.TrimSpace(text[start:i])
				if part == "" {
					return nil, ErrCommandText
				}
				out = append(out, part)
				start = i + 1
			}
		}
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
