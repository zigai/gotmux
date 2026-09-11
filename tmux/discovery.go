package tmux

import (
	"os"
	"path/filepath"
	"strconv"
)

// DiscoverSockets searches standard tmux socket directories and returns absolute paths
// to all active tmux UNIX domain sockets owned by the current user.
func DiscoverSockets() ([]string, error) {
	var sockets []string

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

// DiscoverServers searches for active tmux sockets on the host and returns configured [Server] instances.
func DiscoverServers(baseCfg Config) ([]*Server, error) {
	sockets, err := DiscoverSockets()
	if err != nil {
		return nil, err
	}

	var servers []*Server

	for _, sock := range sockets {
		cfg := baseCfg
		cfg.SocketPath = sock
		cfg.SocketName = ""

		s, err := New(cfg)
		if err == nil {
			servers = append(servers, s)
		}
	}

	return servers, nil
}
