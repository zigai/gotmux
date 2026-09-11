package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TransportSupport describes library execution paths, independent of daemon
// version, target validity, and command-specific argument restrictions.
type TransportSupport struct {
	RawCommands    bool
	ScalarReads    bool
	Capture        bool
	BinaryInput    bool
	InteractiveUI  bool
	TerminalAttach bool
}

// Endpoint records the exact socket selection parameters.
// Preserves raw values rather than resolving canonical paths to avoid equating distinct daemons.
type Endpoint struct {
	// SocketPath is the explicit path to the UNIX domain socket (-S flag).
	SocketPath string

	// SocketName is the short socket name (-L flag), e.g. "default".
	SocketName string

	// TempDir is the base directory containing named sockets (TMUX_TMPDIR or /tmp).
	TempDir string

	// UID is the user ID owning the tmux socket directory (tmux-<uid>).
	UID int
}

// Server represents an explicitly configured tmux server target.
// Subprocess operations are concurrency-safe and throttled by [Limits.Concurrent].
type Server struct {
	config   Config
	endpoint Endpoint
	runner   *runner
	conn     *Connection
	bound    *ServerIdentity
	lifetime *Connection
}

// String returns a human-readable representation of the endpoint socket path.
// For named sockets, this formats the expected path as TempDir/tmux-UID/SocketName.
func (e Endpoint) String() string {
	if e.SocketPath != "" {
		return e.SocketPath
	}

	return filepath.Join(e.TempDir, "tmux-"+strconv.Itoa(e.UID), e.SocketName)
}

// New validates and freezes configuration into an immutable [Server] handle.
// It never spawns a process, creates files, or contacts a daemon.
func New(cfg Config) (*Server, error) {
	limits, err := normalizeLimits(cfg.Limits)
	if err != nil {
		return nil, err
	}

	cfg.Limits = limits
	if cfg.SocketPath != "" && cfg.SocketName != "" {
		return nil, invalid("choose SocketPath or SocketName")
	}

	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}

	env = append([]string{}, env...)

	vars, err := parseEnvList(env)
	if err != nil {
		return nil, err
	}

	dir, err := resolveDir(cfg.Dir)
	if err != nil {
		return nil, err
	}

	binary := cfg.Binary
	if binary == "" {
		binary = "tmux"
	}

	binary, err = lookPath(binary, vars["PATH"], dir)
	if err != nil {
		return nil, err
	}

	endpoint, env, err := resolveEndpoint(cfg, vars, dir, env)
	if err != nil {
		return nil, err
	}

	configFile, err := resolveConfigFile(cfg.ConfigFile, dir)
	if err != nil {
		return nil, err
	}

	cfg.Binary = binary
	cfg.Dir = dir
	cfg.Env = env
	cfg.ConfigFile = configFile

	return &Server{
		config:   cfg,
		endpoint: endpoint,
		runner:   newRunner(binary, env, dir, limits.Concurrent),
		conn:     nil,
		bound:    nil,
		lifetime: nil,
	}, nil
}

// String returns a diagnostic summary of the server, including its target socket.
func (s *Server) String() string {
	if s == nil {
		return "tmux.Server(<invalid>)"
	}

	return fmt.Sprintf("tmux.Server(%s)", s.endpoint.String())
}

// Endpoint returns an immutable copy of the socket selection parameters.
func (s *Server) Endpoint() Endpoint {
	if s == nil {
		return Endpoint{SocketPath: "", SocketName: "", TempDir: "", UID: 0}
	}

	return s.endpoint
}

// Limits returns the active resource limits governing this server's operations.
func (s *Server) Limits() Limits {
	if s == nil {
		return Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0}
	}

	return s.config.Limits
}

// Transport reports whether operations on this server execute via independent
// short-lived [Subprocess] calls or over a persistent [Control] wire connection.
func (s *Server) Transport() Transport {
	if s != nil && s.conn != nil {
		return Control
	}

	return Subprocess
}

// UsingSubprocess explicitly selects subprocess execution while retaining any
// daemon identity and control-connection lifetime binding.
func (s *Server) UsingSubprocess() (*Server, error) {
	if s == nil || s.runner == nil {
		return nil, ErrInvalidHandle
	}

	if s.lifetime != nil {
		if err := s.lifetime.closedError(); err != nil {
			return nil, err
		}
	}

	if s.conn != nil {
		return s.conn.AuxiliaryServer(), nil
	}

	return s, nil
}

// Support reports transport restrictions without contacting tmux.
func (s *Server) Support() TransportSupport {
	subprocess := s != nil && s.runner != nil && s.conn == nil

	return TransportSupport{
		RawCommands: subprocess, ScalarReads: subprocess, Capture: subprocess,
		BinaryInput: subprocess, InteractiveUI: subprocess, TerminalAttach: subprocess,
	}
}

func (s *Server) baseArgs(allowStart bool) []string {
	args := []string{"-u"}
	if !allowStart {
		args = append(args, "-N")
	}

	if s.endpoint.SocketPath != "" {
		args = append(args, "-S", s.endpoint.SocketPath)
	} else {
		args = append(args, "-L", s.endpoint.SocketName)
	}

	if s.config.ConfigFile != "" {
		args = append(args, "-f", s.config.ConfigFile)
	}

	return args
}

func resolveDir(dir string) (string, error) {
	var err error
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("working directory: %w", err)
		}
	} else {
		dir, err = filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("absolute directory: %w", err)
		}
	}

	st, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("stat directory: %w", err)
	}

	if !st.IsDir() {
		return "", invalid("Dir is not a directory")
	}

	return dir, nil
}

func validSocketName(name string) bool {
	return !strings.ContainsAny(name, "/\x00") && name != "." && name != ".."
}

func resolveTempDir(dir, tmp string) string {
	if tmp == "" {
		tmp = "/tmp"
	}

	if !filepath.IsAbs(tmp) {
		tmp = filepath.Join(dir, tmp)
	}

	return tmp
}

func resolveEndpoint(cfg Config, vars map[string]string, dir string, env []string) (Endpoint, []string, error) {
	endpoint := Endpoint{SocketPath: "", SocketName: "", TempDir: "", UID: os.Getuid()}

	switch {
	case cfg.SocketPath != "":
		if !filepath.IsAbs(cfg.SocketPath) || strings.ContainsRune(cfg.SocketPath, 0) {
			return endpoint, nil, invalid("absolute SocketPath required")
		}

		endpoint.SocketPath = cfg.SocketPath
	case cfg.SocketName != "":
		if !validSocketName(cfg.SocketName) {
			return endpoint, nil, invalid("SocketName")
		}

		endpoint.SocketName = cfg.SocketName
	case vars["TMUX"] != "":
		hint, err := ParseEnvironment(Environment{TMUX: vars["TMUX"], TMUXPane: vars["TMUX_PANE"]})
		if err != nil {
			return endpoint, nil, err
		}

		endpoint.SocketPath = hint.SocketPath
	default:
		endpoint.SocketName = "default"
	}

	if endpoint.SocketName != "" {
		tmp := resolveTempDir(dir, vars["TMUX_TMPDIR"])
		endpoint.TempDir = tmp
		env = replaceEnv(env, "TMUX_TMPDIR", tmp)
	}

	return endpoint, env, nil
}

func resolveConfigFile(file, dir string) (string, error) {
	if file == "" {
		return "", nil
	}

	if strings.ContainsRune(file, 0) {
		return "", invalid("ConfigFile")
	}

	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}

	return file, nil
}
