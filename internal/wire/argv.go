// Package wire owns tmux's distinct argv, command-text, and output dialects.
package wire

import (
	"errors"
	"io"
	"strings"
)

var (
	ErrNULCommandText   = errors.New("NUL in tmux command text")
	ErrEncodedSizeLimit = errors.New("encoded size limit")
)

type (
	QuoteWriter struct{ W io.Writer }
	Counter     struct {
		N     int64
		Limit int64
		Err   error
	}
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
	if s == "" || !isLetter(s[0]) {
		return false
	}

	for i := 1; i < len(s); i++ {
		if !isCommandChar(s[i]) {
			return false
		}
	}

	return true
}

// LiteralFormat escapes format expansion, independently of command-text quoting.
func LiteralFormat(s string) string { return strings.ReplaceAll(s, "#", "#{a:35}") }

func (q QuoteWriter) Write(p []byte) (int, error) {
	start := 0

	for i, b := range p {
		if isSafeQuoteByte(b) {
			continue
		}

		if b == 0 {
			return i, ErrNULCommandText
		}

		if start < i {
			if _, err := q.W.Write(p[start:i]); err != nil {
				return start, err //nolint:wrapcheck // QuoteWriter implements io.Writer and must propagate underlying writer errors directly
			}
		}

		if err := writeOctalEscape(q.W, b); err != nil {
			return i, err
		}

		start = i + 1
	}

	if start < len(p) {
		if _, err := q.W.Write(p[start:]); err != nil {
			return start, err //nolint:wrapcheck // QuoteWriter implements io.Writer and must propagate underlying writer errors directly
		}
	}

	return len(p), nil
}

func Quoted(w io.Writer, s string) error {
	if _, err := io.WriteString(w, `"`); err != nil {
		return err //nolint:wrapcheck // Quoted writes to io.Writer and propagates underlying writer errors directly
	}

	if _, err := io.WriteString(QuoteWriter{W: w}, s); err != nil {
		return err //nolint:wrapcheck // Quoted writes to io.Writer and propagates underlying writer errors directly
	}

	_, err := io.WriteString(w, `"`)

	return err //nolint:wrapcheck // Quoted writes to io.Writer and propagates underlying writer errors directly
}

func Quote(s string) (string, error) { var b strings.Builder; e := Quoted(&b, s); return b.String(), e }

func (c *Counter) Write(b []byte) (int, error) {
	if c.Err != nil {
		return 0, c.Err
	}

	n := int64(len(b))
	if c.N > c.Limit-n {
		c.Err = ErrEncodedSizeLimit
		return 0, c.Err
	}

	c.N += n

	return len(b), nil
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isCommandChar(b byte) bool {
	return isLetter(b) || (b >= '0' && b <= '9') || b == '-' || b == '_'
}

func isSafeQuoteByte(b byte) bool {
	if b < 0x20 || b >= 0x7f {
		return false
	}

	switch b {
	case '"', '\\', '$', '`':
		return false
	default:
		return true
	}
}

func writeOctalEscape(w io.Writer, b byte) error {
	esc := [4]byte{'\\', '0' + (b >> 6), '0' + ((b >> 3) & 7), '0' + (b & 7)} //nolint:mnd // octal digit bit-shift constants
	_, err := w.Write(esc[:])

	return err //nolint:wrapcheck // QuoteWriter implements io.Writer and must propagate underlying writer errors directly
}
