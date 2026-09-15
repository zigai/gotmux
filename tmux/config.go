package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// DefaultCommandTimeout is the default deadline for a command round-trip.
	// Expiration cancels the operation and terminates its local client.
	DefaultCommandTimeout = 5 * time.Second
	defaultOutputBytes    = 4 << 20
	defaultInputBytes     = 1 << 20
	defaultConcurrent     = 8
)

const (
	// UTF8Default emits -u, forcing UTF-8 encoding for reliable wire decoding (library default).
	UTF8Default UTF8Mode = iota

	// UTF8Force emits -u explicitly.
	UTF8Force

	// UTF8Omit omits -u, allowing tmux to auto-detect UTF-8 support from the environment.
	UTF8Omit
)

const (
	// LogNone disables tmux file logging (default).
	LogNone LogLevel = iota

	// LogVerbose enables verbose logging (-v flag, creating tmux-client/server-PID.log).
	LogVerbose

	// LogDebug enables maximum verbosity debug logging (-vv flag).
	LogDebug
)

type (
	// UTF8Mode controls whether the -u flag is emitted to force UTF-8 client mode.
	UTF8Mode uint8

	// LogLevel specifies tmux daemon and client file logging verbosity (-v / -vv flags).
	LogLevel uint8
)

// Config specifies how [New] locates a tmux daemon and configures subprocess execution.
// Frozen at [New] time; subsequent mutations have no effect.
type Config struct {
	// Binary is the tmux executable path or name. If empty, defaults to "tmux" in PATH.
	Binary string

	// SocketPath is an absolute path to the server socket (-S flag).
	// Wins over SocketName and ambient $TMUX. Cannot contain NUL bytes.
	SocketPath string

	// SocketName is a named socket (-L flag) under TMUX_TMPDIR. Cannot contain slashes or NUL bytes.
	SocketName string

	// ConfigFile is an alternate configuration file (-f flag).
	// Resolved against Dir. Pass "/dev/null" to skip ~/.tmux.conf.
	ConfigFile string

	// Env is the process environment for tmux subprocesses.
	// If nil, snapshots [os.Environ]. If non-nil (even empty), ambient host variables are not inherited.
	Env []string

	// Dir is the working directory for subprocess execution. If empty, resolves to [os.Getwd].
	Dir string

	// Limits bounds timeouts, buffer sizes, and concurrent subprocesses. Zero values use [DefaultLimits].
	Limits Limits

	// UTF8 controls emission of the -u flag. The zero value [UTF8Default] emits -u for reliable wire decoding.
	UTF8 UTF8Mode

	// Colors256 forces tmux to assume the terminal supports 256 colors (-2 flag).
	Colors256 bool

	// TerminalFeatures specifies terminal features for the client (-T flag, e.g. "256", "RGB", "bidi").
	// When non-empty, elements are joined with commas. Cannot contain NUL bytes or commas within elements.
	TerminalFeatures []string

	// LogLevel controls tmux file logging verbosity (-v or -vv flags).
	LogLevel LogLevel

	// LoginShell instructs tmux to behave as a login shell (-l flag).
	LoginShell bool
}

// Limits bounds execution time, concurrent processes, and buffers across operations on a [Server].
type Limits struct {
	// CommandTimeout is the maximum duration for a single command before cancellation.
	CommandTimeout time.Duration

	// OutputBytes caps stdout/stderr bytes read from tmux before returning [ErrOutputLimit].
	OutputBytes int64

	// InputBytes bounds stdin and argument bytes sent to tmux before returning [ErrInputLimit].
	InputBytes int64

	// Concurrent caps the number of tmux subprocesses executing simultaneously.
	Concurrent int
}

// DefaultLimits returns the standard operational bounds: 5s timeout, 4 MiB output, 1 MiB input, 8 workers.
func DefaultLimits() Limits {
	return Limits{CommandTimeout: DefaultCommandTimeout, OutputBytes: defaultOutputBytes, InputBytes: defaultInputBytes, Concurrent: defaultConcurrent}
}

func normalizeLimits(l Limits) (Limits, error) {
	if l.CommandTimeout < 0 || l.OutputBytes < 0 || l.InputBytes < 0 || l.Concurrent < 0 {
		return l, invalid("negative limit")
	}

	d := DefaultLimits()
	if l.CommandTimeout == 0 {
		l.CommandTimeout = d.CommandTimeout
	}

	if l.OutputBytes == 0 {
		l.OutputBytes = d.OutputBytes
	}

	if l.InputBytes == 0 {
		l.InputBytes = d.InputBytes
	}

	if l.Concurrent == 0 {
		l.Concurrent = d.Concurrent
	}

	if l.OutputBytes > 1<<40 || l.InputBytes > 1<<40 || l.Concurrent > 1<<20 {
		return l, invalid("nonsensical resource limit")
	}

	return l, nil
}

func parseEnvList(env []string) (map[string]string, error) {
	vars := make(map[string]string, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" || strings.ContainsRune(entry, 0) {
			return nil, invalid("malformed environment entry")
		}

		if _, ok := vars[name]; ok {
			return nil, invalid("duplicate environment key")
		}

		vars[name] = value
	}

	return vars, nil
}

func replaceEnv(env []string, key, value string) []string {
	out := append([]string{}, env...)
	for i, e := range out {
		if strings.HasPrefix(e, key+"=") {
			out[i] = key + "=" + value
			return out
		}
	}

	return append(out, key+"="+value)
}

func lookPath(name, path, dir string) (string, error) {
	check := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}

		st, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("stat binary: %w", err)
		}

		if st.IsDir() || st.Mode()&0o111 == 0 {
			return "", os.ErrPermission
		}

		return p, nil
	}
	if strings.ContainsAny(name, "/\\") {
		return check(name)
	}

	for _, p := range filepath.SplitList(path) {
		if p == "" {
			p = dir
		}

		if r, err := check(filepath.Join(p, name)); err == nil {
			return r, nil
		}
	}

	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
