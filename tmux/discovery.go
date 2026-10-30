package tmux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

var discoveryCache = &socketVerdicts{mu: sync.Mutex{}, verdicts: map[string]socketVerdict{}}

type socketCandidate struct {
	path string
	info os.FileInfo
}

type socketVerdict struct {
	file           os.FileInfo
	refused        bool
	verifiedBinary string
}

type socketVerdicts struct {
	mu       sync.Mutex
	verdicts map[string]socketVerdict
}

// DiscoverSockets searches standard tmux socket directories and returns absolute paths
// to all active tmux UNIX domain sockets owned by the current user.
func DiscoverSockets() ([]string, error) {
	var config Config

	servers, err := DiscoverServers(config)
	if err != nil {
		return nil, err
	}

	sockets := make([]string, 0, len(servers))
	for _, server := range servers {
		sockets = append(sockets, server.Endpoint().SocketPath)
	}

	return sockets, nil
}

// DiscoverServers searches for active tmux sockets on the host and returns configured [Server] instances.
// Results are remembered per socket file: a file that refused a connection is not
// dialed again, and a daemon verified with the same binary is not probed again,
// until a new socket file replaces it.
func DiscoverServers(baseCfg Config) ([]*Server, error) {
	candidates, err := socketCandidates()
	if err != nil {
		return nil, err
	}

	servers := []*Server{}
	verdicts := make(map[string]socketVerdict, len(candidates))

	for _, candidate := range candidates {
		cached, known := discoveryCache.lookup(candidate)
		if known && cached.refused {
			verdicts[candidate.path] = cached

			continue
		}

		if err := dial(context.Background(), candidate.path); err != nil {
			if errors.Is(err, syscall.ECONNREFUSED) {
				verdicts[candidate.path] = socketVerdict{file: candidate.info, refused: true, verifiedBinary: ""}
			}

			continue
		}

		cfg := baseCfg
		cfg.SocketPath = candidate.path
		cfg.SocketName = ""

		server, err := New(cfg)
		if err != nil {
			return nil, err
		}

		if known && cached.verifiedBinary == server.runner.binary {
			verdicts[candidate.path] = cached

			servers = append(servers, server)

			continue
		}

		if _, err := server.Probe(context.Background()); err == nil {
			verdicts[candidate.path] = socketVerdict{file: candidate.info, refused: false, verifiedBinary: server.runner.binary}
			servers = append(servers, server)
		}
	}

	discoveryCache.replace(verdicts)

	return servers, nil
}

func (c *socketVerdicts) lookup(candidate socketCandidate) (socketVerdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	verdict, ok := c.verdicts[candidate.path]
	// tmpfs reuses inode numbers, so a new socket file can share the old
	// identity; its bind time still differs.
	if !ok || !os.SameFile(verdict.file, candidate.info) || !verdict.file.ModTime().Equal(candidate.info.ModTime()) {
		return socketVerdict{file: nil, refused: false, verifiedBinary: ""}, false
	}

	return verdict, true
}

func (c *socketVerdicts) replace(verdicts map[string]socketVerdict) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.verdicts = verdicts
}

func dial(ctx context.Context, path string) error {
	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("dial tmux socket: %w", err)
	}

	if err := conn.Close(); err != nil {
		return fmt.Errorf("close tmux socket: %w", err)
	}

	return nil
}

func socketCandidates() ([]socketCandidate, error) {
	sockets := []socketCandidate{}
	seen := map[string]bool{}

	uid := os.Getuid()
	dirs := []string{
		filepath.Join(os.TempDir(), "tmux-"+strconv.Itoa(uid)),
		filepath.Join("/tmp", "tmux-"+strconv.Itoa(uid)),
	}

	if tmpdir := os.Getenv("TMUX_TMPDIR"); tmpdir != "" {
		dirs = append([]string{filepath.Join(tmpdir, "tmux-"+strconv.Itoa(uid))}, dirs...)
	}

	for _, dir := range dirs {
		dir, err := filepath.Abs(dir)
		if err != nil {
			return nil, fmt.Errorf("socket directory: %w", err)
		}

		if seen[dir] {
			continue
		}

		seen[dir] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}

			if info.Mode()&os.ModeSocket != 0 {
				sockets = append(sockets, socketCandidate{path: filepath.Join(dir, entry.Name()), info: info})
			}
		}
	}

	return sockets, nil
}
