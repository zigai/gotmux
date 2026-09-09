package tmux

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zigai/gotmux/internal/codec"
)

const (
	execLauncher = `exec "$@"`

	environmentLauncher = `n=$1; shift
while [ "$n" -gt 0 ]; do
 export "$1" || exit
 shift
 n=$((n - 1))
done
exec "$@"`

	shellEnvironmentLauncher = `tmux_go_shell=$SHELL
n=$1; shift
while [ "$n" -gt 0 ]; do
 export "$1" || exit
 shift
 n=$((n - 1))
done
exec "$tmux_go_shell" -c "$1"`
)

const (
	// AllowStart permits tmux to start a new background server daemon if one is not
	// already running on the selected socket endpoint.
	AllowStart StartPolicy = iota

	// ExistingOnly requires an already running daemon. If no daemon is listening,
	// operations will fail with [ErrNoServer] rather than starting a replacement.
	ExistingOnly
)

const (
	// Vertical splits the pane vertically (-v flag), placing the new pane below the current one.
	Vertical Direction = iota

	// Horizontal splits the pane horizontally (-h flag), placing the new pane beside the current one.
	Horizontal
)

const (
	// EvenHorizontal arranges panes in equal-width vertical columns side by side.
	EvenHorizontal Layout = "even-horizontal"

	// EvenVertical arranges panes in equal-height horizontal rows stacked on top of each other.
	EvenVertical Layout = "even-vertical"

	// MainHorizontal arranges a large primary pane on top with remaining panes tiled below.
	MainHorizontal Layout = "main-horizontal"

	// MainVertical arranges a large primary pane on the left with remaining panes tiled on the right.
	MainVertical Layout = "main-vertical"

	// Tiled arranges panes evenly in a 2D rectangular grid.
	Tiled Layout = "tiled"
)

const (
	programDefault uint8 = iota
	programExec
	programShell
)

type (
	// Program specifies how an initial process should be launched inside a new session,
	// window, or pane.
	//
	// The zero value of Program represents tmux's default shell behavior (running the user's
	// configured default-shell without extra arguments).
	Program struct {
		kind uint8
		name string
		args []string
	}

	// Size specifies terminal dimensions measured in character cells (columns and rows).
	// A zero value indicates that tmux should choose the dimensions automatically.
	Size struct {
		Width  int
		Height int
	}

	// StartPolicy controls whether an operation is permitted to spawn a new tmux server daemon.
	StartPolicy uint8

	// Direction indicates whether a split occurs vertically (top/bottom) or horizontally (left/right).
	Direction uint8

	// SplitSize specifies the size for a pane split or join.
	// Exactly one of Cells or Percent should be set; if both are zero, tmux splits the pane evenly (50%).
	SplitSize struct {
		// Cells specifies an exact size in character columns or rows.
		Cells int

		// Percent specifies the size as a percentage (1 to 100) of the parent pane or window.
		Percent int
	}

	// Layout identifies a named tmux window pane layout geometry.
	Layout string
)

// Exec creates a [Program] that executes a binary with literal command-line arguments.
// Arguments are passed directly without shell expansion or word splitting.
func Exec(name string, args ...string) Program {
	return Program{kind: programExec, name: name, args: append([]string{}, args...)}
}

// Shell creates a [Program] that passes script directly to the user's shell via "$SHELL -c".
func Shell(script string) Program { return Program{kind: programShell, name: script, args: nil} }

func (p Program) argv() ([]string, error) {
	if !codec.ValidString(p.name) {
		return nil, invalid("program NUL")
	}

	switch p.kind {
	case programDefault:
		return nil, nil
	case programExec:
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
			return nil, unsupported("zero-argument executable beginning with dash; use an explicit path")
		}
		// exec with a pathname never selects a shell builtin. A bare executable name
		// is searched by exec using PATH, not evaluated as shell text.
		return []string{"/bin/sh", "-c", execLauncher, "gotmux-exec", p.name}, nil
	case programShell:
		return []string{p.name}, nil
	default:
		return nil, invalid("program kind")
	}
}

// Size is measured in terminal cells. Both zero means use tmux's default.

func (s Size) valid() bool {
	return s.Width >= 0 && s.Height >= 0 && s.Width <= 1<<20 && s.Height <= 1<<20
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
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' && (i <= 0 || c < '0' || c > '9') {
			return false
		}
	}

	return true
}

func programArgs(dir string, env map[string]string, program Program) ([]string, []string, error) {
	args := []string{}

	if dir != "" {
		v, err := literal(dir)
		if err != nil {
			return nil, nil, err
		}

		args = append(args, "-c", v)
	}

	keys, err := sortedEnvKeys(env)
	if err != nil {
		return nil, nil, err
	}

	argv, err := program.argv()
	if err != nil || len(keys) == 0 {
		return args, argv, err
	}

	argv, err = wrapProgramEnv(program, env, keys)
	if err != nil {
		return nil, nil, err
	}

	return args, argv, nil
}

func sortedEnvKeys(env map[string]string) ([]string, error) {
	keys := make([]string, 0, len(env))
	for k, v := range env {
		if !envName(k) || !codec.ValidString(v) {
			return nil, invalid("program environment")
		}

		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys, nil
}

func wrapProgramEnv(program Program, env map[string]string, keys []string) ([]string, error) {
	if program.kind == 0 {
		return nil, unsupported("environment overrides require an explicit Exec or Shell program")
	}

	assignments := make([]string, 0, len(keys))
	for _, k := range keys {
		assignments = append(assignments, k+"="+env[k])
	}

	script := environmentLauncher

	var tail []string

	if program.kind == 1 {
		if strings.HasPrefix(program.name, "-") && !strings.Contains(program.name, "/") {
			return nil, unsupported("environment launcher requires an explicit path for a leading-dash executable")
		}

		tail = append([]string{program.name}, program.args...)
	} else {
		script = shellEnvironmentLauncher
		tail = []string{program.name}
	}

	argv := make([]string, 0, 5+len(assignments)+len(tail))
	argv = append(argv, "/bin/sh", "-c", script, "gotmux-env", strconv.Itoa(len(assignments)))
	argv = append(argv, assignments...)
	argv = append(argv, tail...)

	return argv, nil
}
