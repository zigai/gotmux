package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	process "example.com/tmux/internal/exec"
)

type Config struct {
	Binary     string
	SocketPath string
	SocketName string
	ConfigFile string
	Env        []string
	Dir        string
	Limits     Limits
}
type Limits struct {
	CommandTimeout time.Duration
	OutputBytes    int64
	InputBytes     int64
	Concurrent     int
}

func DefaultLimits() Limits {
	return Limits{CommandTimeout: 5 * time.Second, OutputBytes: 4 << 20, InputBytes: 1 << 20, Concurrent: 8}
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

// Endpoint records exactly how the socket was selected. Distinct selections are
// never equated through basenames, symlinks, or heuristic server discovery.
type Endpoint struct {
	SocketPath string
	SocketName string
	TempDir    string
	UID        int
}

func (e Endpoint) String() string {
	if e.SocketPath != "" {
		return e.SocketPath
	}
	return filepath.Join(e.TempDir, "tmux-"+strconv.Itoa(e.UID), e.SocketName)
}

type Server struct {
	config   Config
	endpoint Endpoint
	runner   *process.Runner
	conn     *Connection
	bound    *ServerIdentity
	lifetime *Connection
}

// New validates and freezes configuration; it never spawns a process or contacts
// a daemon. The module path is provisional; see README for choosing a real one.
func New(cfg Config) (*Server, error) {
	l, e := normalizeLimits(cfg.Limits)
	if e != nil {
		return nil, e
	}
	cfg.Limits = l
	if cfg.SocketPath != "" && cfg.SocketName != "" {
		return nil, invalid("choose SocketPath or SocketName")
	}
	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}
	env = append([]string{}, env...)
	vars, e := parseEnvList(env)
	if e != nil {
		return nil, e
	}
	dir := cfg.Dir
	if dir == "" {
		dir, e = os.Getwd()
		if e != nil {
			return nil, e
		}
	} else {
		dir, e = filepath.Abs(dir)
		if e != nil {
			return nil, e
		}
	}
	st, e := os.Stat(dir)
	if e != nil {
		return nil, e
	}
	if !st.IsDir() {
		return nil, invalid("Dir is not a directory")
	}
	binary := cfg.Binary
	if binary == "" {
		binary = "tmux"
	}
	binary, e = lookPath(binary, vars["PATH"], dir)
	if e != nil {
		return nil, e
	}
	endpoint := Endpoint{UID: os.Getuid()}
	switch {
	case cfg.SocketPath != "":
		if !filepath.IsAbs(cfg.SocketPath) || strings.ContainsRune(cfg.SocketPath, 0) {
			return nil, invalid("absolute SocketPath required")
		}
		endpoint.SocketPath = cfg.SocketPath
	case cfg.SocketName != "":
		if strings.ContainsAny(cfg.SocketName, "/\x00") || cfg.SocketName == "." || cfg.SocketName == ".." {
			return nil, invalid("SocketName")
		}
		endpoint.SocketName = cfg.SocketName
	case vars["TMUX"] != "":
		hint, e := ParseEnvironment(Environment{TMUX: vars["TMUX"], TMUXPane: vars["TMUX_PANE"]})
		if e != nil {
			return nil, e
		}
		endpoint.SocketPath = hint.SocketPath
	default:
		endpoint.SocketName = "default"
	}
	if endpoint.SocketName != "" {
		tmp := vars["TMUX_TMPDIR"]
		if tmp == "" {
			tmp = "/tmp"
		}
		if !filepath.IsAbs(tmp) {
			tmp = filepath.Join(dir, tmp)
		}
		endpoint.TempDir = tmp
		env = replaceEnv(env, "TMUX_TMPDIR", tmp)
	}
	if cfg.ConfigFile != "" {
		if strings.ContainsRune(cfg.ConfigFile, 0) {
			return nil, invalid("ConfigFile")
		}
		if !filepath.IsAbs(cfg.ConfigFile) {
			cfg.ConfigFile = filepath.Join(dir, cfg.ConfigFile)
		}
	}
	cfg.Binary = binary
	cfg.Dir = dir
	cfg.Env = env
	return &Server{config: cfg, endpoint: endpoint, runner: process.New(binary, env, dir, l.Concurrent)}, nil
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
		st, e := os.Stat(p)
		if e != nil {
			return "", e
		}
		if st.IsDir() || st.Mode()&0111 == 0 {
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
		if r, e := check(filepath.Join(p, name)); e == nil {
			return r, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
func (s *Server) Endpoint() Endpoint {
	if s == nil {
		return Endpoint{}
	}
	return s.endpoint
}
func (s *Server) Limits() Limits {
	if s == nil {
		return Limits{}
	}
	return s.config.Limits
}
func (s *Server) Transport() Transport {
	if s != nil && s.conn != nil {
		return Control
	}
	return Subprocess
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

type operation struct {
	ctx    context.Context
	caller context.Context
	close  context.CancelFunc
	output int64
	stderr int64
	input  int64
}

func (s *Server) begin(ctx context.Context) (*operation, error) {
	if s == nil || s.runner == nil {
		return nil, ErrInvalidHandle
	}
	if ctx == nil {
		return nil, invalid("nil context")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, &UnsupportedError{Feature: "platform " + runtime.GOOS}
	}
	if s.lifetime != nil {
		if e := s.lifetime.closedError(); e != nil {
			return nil, e
		}
	}
	child, cancel := context.WithTimeout(ctx, s.config.Limits.CommandTimeout)
	return &operation{ctx: child, caller: ctx, close: cancel, output: s.config.Limits.OutputBytes, stderr: s.config.Limits.OutputBytes, input: s.config.Limits.InputBytes}, nil
}
func (s *Server) executeProcess(op *operation, args []string, input []byte) (Result, error) {
	r := s.runner.Run(op.ctx, args, input, max(op.output, 0), max(op.stderr, 0))
	result := Result{Stdout: r.Stdout, Stderr: r.Stderr, ExitCode: r.ExitCode}
	op.output -= int64(len(r.Stdout))
	op.stderr -= int64(len(r.Stderr))
	if r.Err == nil {
		return result, nil
	}
	err := r.Err
	if errors.Is(err, process.ErrOutputLimit) {
		err = errors.Join(ErrOutputLimit, err)
	}
	if errors.Is(err, process.ErrShutdownIncomplete) {
		err = errors.Join(ErrShutdownIncomplete, err)
	}
	if klass := classifyStderr(result.Stderr); klass != nil {
		err = errors.Join(klass, err)
	}
	effect := NotSent
	if r.Started {
		effect = Unknown
	}
	return result, &CommandError{Command: "process", Result: cloneResult(result), Outcome: Outcome{Effect: effect}, Timeout: contextSource(op.caller, err), Err: err}
}
func classifyStderr(data []byte) error {
	// Only exact errno diagnostics distinguish a missing endpoint from permission,
	// malformed sockets, and vendor-specific failures. Diagnostics remain available.
	s := string(data)
	if strings.Contains(s, "Permission denied") || strings.Contains(s, "Operation not permitted") {
		return os.ErrPermission
	}
	if (strings.HasPrefix(s, "error connecting to ") && strings.Contains(s, "(No such file or directory)")) || strings.HasPrefix(s, "no server running on ") {
		return ErrNoServer
	}
	if strings.HasPrefix(s, "can't find pane:") || strings.HasPrefix(s, "can't find window:") || strings.HasPrefix(s, "can't find session:") || strings.HasPrefix(s, "can't find client:") || strings.HasPrefix(s, "no such buffer:") {
		return ErrNotFound
	}
	if strings.HasPrefix(s, "multiple sessions:") || strings.HasPrefix(s, "ambiguous ") {
		return ErrAmbiguousTarget
	}
	return nil
}

func (s *Server) execute(op *operation, p plan, g *guard, input []byte) (Result, error) {
	failed := func(e error) (Result, error) {
		r := Result{ExitCode: -1}
		return r, &CommandError{Command: planName(p), Result: r, Outcome: Outcome{Effect: NotSent}, Err: e}
	}
	if e := op.ctx.Err(); e != nil {
		return failed(e)
	}
	if s.lifetime != nil {
		if e := s.lifetime.closedError(); e != nil {
			return failed(e)
		}
	}
	if s.bound != nil && g == nil && planName(p) != "show-buffer" {
		g = &guard{identity: *s.bound}
	}
	if g != nil {
		p = g.wrap(p)
	}
	n, e := p.size(op.input)
	if e != nil {
		return failed(e)
	}
	if s.conn != nil {
		if n > op.input-controlWireOverhead {
			return failed(ErrInputLimit)
		}
		n += controlWireOverhead
	}
	if int64(len(input)) > op.input-n {
		return failed(ErrInputLimit)
	}
	op.input -= n + int64(len(input))
	var r Result
	if s.conn != nil {
		if input != nil {
			return failed(&UnsupportedError{Feature: "stdin over control", Transport: Control, Err: ErrTransportUnsupported})
		}
		r, e = s.conn.run(op, p, n)
		op.output -= int64(len(r.Stdout))
		op.stderr -= int64(len(r.Stderr))
	} else {
		var args []string
		args, e = p.argv()
		if e != nil {
			return failed(e)
		}
		args = append(s.baseArgs(p.allowStart), args...)
		r, e = s.executeProcess(op, args, input)
	}
	if g != nil {
		r, e = g.unwrap(r, e)
	}
	if e != nil {
		var ce *CommandError
		if errors.As(e, &ce) {
			ce.Command = planName(p)
		}
		return r, e
	}
	if e = op.ctx.Err(); e != nil {
		return r, &CommandError{Command: planName(p), Result: cloneResult(r), Outcome: Outcome{Effect: Confirmed}, Timeout: contextSource(op.caller, e), Err: e}
	}
	return r, nil
}
func planName(p plan) string {
	if len(p.nodes) == 1 {
		return p.nodes[0].name
	}
	return "sequence"
}
func (s *Server) String() string {
	if s == nil {
		return "tmux.Server(<invalid>)"
	}
	return fmt.Sprintf("tmux.Server(%s)", s.endpoint.String())
}
