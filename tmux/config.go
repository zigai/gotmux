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

// Config specifies how [New] locates a tmux daemon and configures subprocess execution.
// All fields are validated and frozen at [New] time; mutating this struct after initialization
// has no effect on an existing [Server].
type Config struct {
	// Binary is the tmux executable path or name. If empty, [New] looks up "tmux" in PATH.
	Binary string

	// SocketPath specifies an absolute path to the tmux server socket (-S flag).
	// This takes precedence over SocketName and any ambient TMUX environment variable.
	// Must be an absolute path and cannot contain NUL bytes.
	SocketPath string

	// SocketName specifies a named socket (-L flag) located under TMUX_TMPDIR.
	// It cannot be used together with SocketPath. Sockets named "." or ".." or
	// containing slashes or NUL bytes are rejected.
	SocketName string

	// ConfigFile specifies an alternate configuration file passed to tmux via -f.
	// If relative, it is resolved against Dir at [New] time. Passing "/dev/null"
	// prevents tmux from reading the user's ~/.tmux.conf.
	ConfigFile string

	// Env is the frozen environment slice passed to all spawned tmux subprocesses.
	// If nil, [New] snapshots the host environment via [os.Environ]. If non-nil
	// (even an empty slice), we use exactly this environment without inheriting
	// ambient host variables.
	Env []string

	// Dir is the working directory for subprocess execution. If empty, [New] resolves
	// and validates the current working directory via [os.Getwd]. Must be an existing
	// directory on disk.
	Dir string

	// Limits bounds resource consumption for command timeouts, buffer allocations,
	// and concurrent process execution. Zero values default to [DefaultLimits].
	Limits Limits
}

// Limits bounds execution time, concurrent processes, and memory buffers across
// all operations on a [Server]. Any zero-valued field will be populated with its
// corresponding value from [DefaultLimits].
type Limits struct {
	// CommandTimeout is the maximum duration allowed for a single command or probe
	// before the operation is canceled and subprocesses are terminated.
	CommandTimeout time.Duration

	// OutputBytes is the maximum number of stdout/stderr bytes read from tmux before
	// terminating the subprocess with [ErrOutputLimit].
	OutputBytes int64

	// InputBytes bounds the payload sent to tmux via stdin or command arguments.
	// Exceeding this limit returns [ErrInputLimit] before dispatch.
	InputBytes int64

	// Concurrent is the maximum number of concurrent tmux subprocesses permitted to run
	// simultaneously.
	Concurrent int
}

// DefaultLimits returns the standard operational bounds: 5s command timeout,
// 4 MiB output buffer, 1 MiB input buffer, and 8 concurrent subprocesses.
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
