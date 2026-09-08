package tmux

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"example.com/tmux/internal/codec"
)

// Program separates a direct executable/argv from an explicitly interpreted
// shell script. Zero requests tmux's configured default program. Constructors
// copy argv and do no I/O; validation occurs before any requested action.
type Program struct {
	kind uint8
	name string
	args []string
}

func Exec(name string, args ...string) Program {
	return Program{kind: 1, name: name, args: append([]string{}, args...)}
}
func Shell(script string) Program { return Program{kind: 2, name: script} }

const execLauncher = `exec "$@"`

func (p Program) argv() ([]string, error) {
	if !codec.ValidString(p.name) {
		return nil, invalid("program NUL")
	}
	switch p.kind {
	case 0:
		return nil, nil
	case 1:
		if p.name == "" {
			return nil, invalid("executable")
		}
		for _, a := range p.args {
			if !codec.ValidString(a) {
				return nil, invalid("program argument NUL")
			}
		}
		if len(p.args) > 0 {
			return append([]string{p.name}, p.args...), nil
		}
		// tmux treats ONE program operand as a shell line. A fixed positional
		// launcher forces tmux's execvp path without adding an argument to the user's
		// executable. Shell builtins are deliberately excluded from this path below.
		if strings.HasPrefix(p.name, "-") && !strings.Contains(p.name, "/") {
			return nil, &UnsupportedError{Feature: "zero-argument executable beginning with dash; use an explicit path"}
		}
		// exec with a pathname never selects a shell builtin. A bare executable name
		// is searched by exec using PATH, not evaluated as shell text.
		return []string{"/bin/sh", "-c", execLauncher, "tmux-go-exec", p.name}, nil
	case 2:
		return []string{p.name}, nil
	default:
		return nil, invalid("program kind")
	}
}

// Size is measured in terminal cells. Both zero means use tmux's default.
type Size struct {
	Width  int
	Height int
}

func (s Size) valid() bool {
	return s.Width >= 0 && s.Height >= 0 && s.Width <= 1<<20 && s.Height <= 1<<20
}

type StartPolicy uint8

const (
	AllowStart StartPolicy = iota
	ExistingOnly
)

type Direction uint8

const (
	Vertical Direction = iota
	Horizontal
)

type SplitSize struct {
	Cells   int
	Percent int
}

func (s SplitSize) args() ([]string, error) {
	if s.Cells < 0 || s.Cells > 1<<20 || s.Percent < 0 || s.Percent > 100 || s.Cells != 0 && s.Percent != 0 {
		return nil, invalid("split size")
	}
	if s.Cells != 0 {
		return []string{"-l", strconv.Itoa(s.Cells)}, nil
	}
	if s.Percent != 0 {
		return []string{"-l", strconv.Itoa(s.Percent) + "%"}, nil
	}
	return nil, nil
}

// Layout accepts stock layout names or a tmux-generated serialized layout.
type Layout string

const (
	EvenHorizontal Layout = "even-horizontal"
	EvenVertical   Layout = "even-vertical"
	MainHorizontal Layout = "main-horizontal"
	MainVertical   Layout = "main-vertical"
	Tiled          Layout = "tiled"
)

func literal(s string) (string, error) {
	if !codec.ValidString(s) {
		return "", invalid("NUL literal")
	}
	return codec.LiteralFormat(s), nil
}
func sessionName(name string, optional bool) error {
	if optional && name == "" {
		return nil
	}
	if name == "" || !utf8.ValidString(name) || strings.ContainsAny(name, ":.\\") {
		return invalid("session name would be normalized")
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return invalid("session name would be normalized")
		}
	}
	return nil
}
func envName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range []byte(name) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func programArgs(dir string, env map[string]string, program Program) ([]string, []string, error) {
	args := []string{}
	if dir != "" {
		v, e := literal(dir)
		if e != nil {
			return nil, nil, e
		}
		args = append(args, "-c", v)
	}
	// Environment assignments are positional data in a fixed launcher, not -e
	// new-session options: -e would persist in the session and tmux overwrites
	// PATH/SHELL after applying several native per-spawn overrides.
	keys := make([]string, 0, len(env))
	for k, v := range env {
		if !envName(k) || !codec.ValidString(v) {
			return nil, nil, invalid("program environment")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	argv, err := program.argv()
	if err != nil || len(keys) == 0 {
		return args, argv, err
	}
	if program.kind == 0 {
		return nil, nil, &UnsupportedError{Feature: "environment overrides require an explicit Exec or Shell program"}
	}
	assignments := make([]string, 0, len(keys))
	for _, k := range keys {
		assignments = append(assignments, k+"="+env[k])
	}
	script := environmentLauncher
	var tail []string
	if program.kind == 1 {
		if strings.HasPrefix(program.name, "-") && !strings.Contains(program.name, "/") {
			return nil, nil, &UnsupportedError{Feature: "environment launcher requires an explicit path for a leading-dash executable"}
		}
		tail = append([]string{program.name}, program.args...)
	} else {
		script = shellEnvironmentLauncher
		tail = []string{program.name}
	}
	argv = []string{"/bin/sh", "-c", script, "tmux-go-env", strconv.Itoa(len(assignments))}
	argv = append(argv, assignments...)
	argv = append(argv, tail...)
	return args, argv, nil
}

// Each assignment is one quoted export operand. Neither its name nor its value
// is evaluated as source code. The scripts are fixed; caller data is argv only.
const environmentLauncher = `n=$1; shift
while [ "$n" -gt 0 ]; do
 export "$1" || exit
 shift
 n=$((n - 1))
done
exec "$@"`

// tmux supplies SHELL as its configured default-shell. Retain that executable
// before applying the program's requested environment (which may replace SHELL).
const shellEnvironmentLauncher = `tmux_go_shell=$SHELL
n=$1; shift
while [ "$n" -gt 0 ]; do
 export "$1" || exit
 shift
 n=$((n - 1))
done
exec "$tmux_go_shell" -c "$1"`
