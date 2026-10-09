package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

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
func DiscoverServers(baseCfg Config) ([]*Server, error) {
	sockets, err := socketCandidates()
	if err != nil {
		return nil, err
	}

	servers := []*Server{}

	for _, sock := range sockets {
		cfg := baseCfg
		cfg.SocketPath = sock
		cfg.SocketName = ""

		server, err := New(cfg)
		if err != nil {
			return nil, err
		}

		if _, err := server.Probe(context.Background()); err == nil {
			servers = append(servers, server)
		}
	}

	return servers, nil
}

func socketCandidates() ([]string, error) {
	sockets := []string{}
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
				p := filepath.Join(dir, entry.Name())
				sockets = append(sockets, p)
			}
		}
	}

	return sockets, nil
}
