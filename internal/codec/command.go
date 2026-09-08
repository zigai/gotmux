// Package codec owns tmux's distinct argv, command-text, and output dialects.
package codec

import (
	"errors"
	"io"
	"strings"
)

// Argv escapes only the special trailing-semicolon rule in cmd_parse_from_arguments.
// It is NOT shell quoting. Already-present backslashes are preserved.
func Argv(s string) string {
	if strings.HasSuffix(s, ";") {
		return s[:len(s)-1] + `\;`
	}
	return s
}
func ValidString(s string) bool { return !strings.ContainsRune(s, 0) }
func ValidCommand(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for i := 1; i < len(s); i++ {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}

// LiteralFormat escapes format expansion, independently of command-text quoting.
func LiteralFormat(s string) string { return strings.ReplaceAll(s, "#", "#{a:35}") }

// QuoteWriter writes the INSIDE of a tmux double-quoted word. Octal escapes
// preserve arbitrary non-NUL bytes, including non-UTF-8 and literal line feeds.
// It can wrap another QuoteWriter when encoding a nested command sequence.
type QuoteWriter struct{ W io.Writer }

func (q QuoteWriter) Write(p []byte) (int, error) {
	start := 0
	for i, b := range p {
		if b >= 0x20 && b < 0x7f && b != '"' && b != '\\' && b != '$' && b != '`' {
			continue
		}
		if b == 0 {
			return i, errors.New("NUL in tmux command text")
		}
		if start < i {
			if _, e := q.W.Write(p[start:i]); e != nil {
				return start, e
			}
		}
		esc := [4]byte{'\\', '0' + (b >> 6), '0' + ((b >> 3) & 7), '0' + (b & 7)}
		if _, e := q.W.Write(esc[:]); e != nil {
			return i, e
		}
		start = i + 1
	}
	if start < len(p) {
		if _, e := q.W.Write(p[start:]); e != nil {
			return start, e
		}
	}
	return len(p), nil
}
func Quoted(w io.Writer, s string) error {
	if _, e := io.WriteString(w, `"`); e != nil {
		return e
	}
	if _, e := io.WriteString(QuoteWriter{W: w}, s); e != nil {
		return e
	}
	_, e := io.WriteString(w, `"`)
	return e
}
func Quote(s string) (string, error) { var b strings.Builder; e := Quoted(&b, s); return b.String(), e }

type Counter struct {
	N     int64
	Limit int64
	Err   error
}

func (c *Counter) Write(b []byte) (int, error) {
	if c.Err != nil {
		return 0, c.Err
	}
	n := int64(len(b))
	if c.N > c.Limit-n {
		c.Err = errors.New("encoded size limit")
		return 0, c.Err
	}
	c.N += n
	return len(b), nil
}
