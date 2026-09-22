package tmux

import (
	"bytes"
	"context"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	singlePermParts = 1
	doublePermParts = 2
)

type (
	// AccessEntry captures an entry in the tmux server access list (server-access -l).
	AccessEntry struct {
		// Name is the user or group name.
		Name string

		// IsGroup is true if this entry applies to a UNIX group rather than a user (-g flag).
		IsGroup bool

		// ReadOnly indicates whether the user or group is restricted to read-only access (-r flag).
		// If false, read-write access is granted (-w flag).
		ReadOnly bool
	}

	// AccessOptions configures server socket access permissions (server-access -a).
	AccessOptions struct {
		// Group specifies that the target is a UNIX group name rather than a user (-g flag).
		Group bool

		// ReadOnly restricts the user or group to read-only access (-r flag).
		// If false, read-write access is granted (-w flag).
		ReadOnly bool
	}
)

// AccessList returns the current list of users and groups permitted to access the server socket (server-access -l).
func (s *Server) AccessList(ctx context.Context) ([]AccessEntry, error) {
	if s == nil {
		return nil, opError("AccessList", ErrInvalidHandle)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("AccessList", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("AccessList", err)
	}

	r, err := s.execute(opCtx, op, plainPlan(command("server-access", "-l")), newGuard(info.Identity), nil)
	if err != nil {
		return nil, opError("AccessList", err)
	}

	var entries []AccessEntry

	for line := range bytes.SplitSeq(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}

		if entry, ok := parseAccessLine(string(line)); ok {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func parsePerms(perms string) (bool, bool, bool) {
	parts := strings.Split(perms, ",")
	switch len(parts) {
	case singlePermParts:
		p0 := strings.TrimSpace(parts[0])
		if p0 != "R" && p0 != "W" {
			return false, false, false
		}

		return false, p0 == "R", true
	case doublePermParts:
		p0 := strings.TrimSpace(parts[0])
		p1 := strings.TrimSpace(parts[1])

		if (p0 != "U" && p0 != "G") || (p1 != "R" && p1 != "W") {
			return false, false, false
		}

		return p0 == "G", p1 == "R", true
	default:
		return false, false, false
	}
}

func parseAccessLine(line string) (AccessEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, false
	}

	lastOpen := strings.LastIndexByte(line, '(')
	lastClose := strings.LastIndexByte(line, ')')

	if lastOpen < 0 || lastClose <= lastOpen {
		return AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, false
	}

	name := strings.TrimSpace(line[:lastOpen])
	perms := line[lastOpen+1 : lastClose]

	isGroup, readOnly, ok := parsePerms(perms)
	if !ok {
		return AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, false
	}

	return AccessEntry{
		Name:     name,
		IsGroup:  isGroup,
		ReadOnly: readOnly,
	}, true
}

// GrantAccess grants or modifies socket access for a user or group (server-access -a).
func (s *Server) GrantAccess(ctx context.Context, name string, opts AccessOptions) error {
	if s == nil {
		return opError("GrantAccess", ErrInvalidHandle)
	}

	if name == "" || !wire.ValidString(name) {
		return opError("GrantAccess", invalid("name"))
	}

	var args []string
	if opts.Group {
		args = append(args, "-g")
	}

	if opts.ReadOnly {
		args = append(args, "-r")
	} else {
		args = append(args, "-w")
	}

	args = append(args, "--", name)

	return s.endpointAction(ctx, "server-access", args...)
}

// RevokeAccess revokes socket access for a user or group (server-access -d).
// If the user is currently attached, tmux automatically detaches their clients.
func (s *Server) RevokeAccess(ctx context.Context, name string, isGroup bool) error {
	if s == nil {
		return opError("RevokeAccess", ErrInvalidHandle)
	}

	if name == "" || !wire.ValidString(name) {
		return opError("RevokeAccess", invalid("name"))
	}

	args := []string{"-d"}
	if isGroup {
		args = append(args, "-g")
	}

	args = append(args, "--", name)

	return s.endpointAction(ctx, "server-access", args...)
}
