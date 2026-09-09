package tmux

import (
	"context"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/codec"
)

const (
	supportedStablePattern    = `^(([4-9]|[1-9][0-9]+)[.][0-9]+|3[.]([6-9]|[1-9][0-9]+))[a-z]?$`
	unsupportedStartupVersion = "TGO-GUARD-1:unsupported-version\n"
	minMajorVersion           = 3
	minMinorVersion           = 6
)

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)([a-z]?)(.*)$`)

// Version retains the exact version string reported by tmux alongside parsed numeric components.
//
// Recognized is true only for standard stable releases (e.g. "3.6", "3.6a").
// Development builds ("next-3.4") or vendor-patched builds ("3.6-arch") have Recognized=false:
// we intentionally do not assume development or vendor builds are newer than stable releases,
// so feature gates requiring stable release semantics will reject them unless explicitly bypassed.
type (
	Version struct {
		// Raw is the original unparsed version string (e.g. "tmux 3.6a").
		Raw string

		// Major is the major version integer (e.g. 3).
		Major int

		// Minor is the minor version integer (e.g. 6).
		Minor int

		// Patch is the single-letter release suffix if present (e.g. "a", "b").
		Patch string

		// Suffix is any remaining unrecognized suffix (e.g. "-rc1", "-git").
		Suffix string

		// Recognized is true only if the version matched a canonical stable release pattern.
		Recognized bool
	}

	// Capabilities provides a point-in-time snapshot of the features and commands
	// supported by an answering tmux daemon. It is an immutable copy, not a live cache.
	Capabilities struct {
		// Identity is the daemon instance where these capabilities were observed.
		Identity ServerIdentity

		// Version is the daemon's reported tmux version.
		Version Version

		// Transport is the active transport for the server that performed the probe.
		Transport Transport

		// Support describes library operations available through this transport.
		// It does not assert that the daemon implements a particular command.
		Support TransportSupport

		// Commands is a sorted list of all command names recognized by the daemon.
		Commands []string
	}
)

// ParseVersion parses a raw tmux version string (such as "tmux 3.6a" or "3.6") into
// structured [Version] components. Leading "tmux " prefixes and whitespace are stripped.
func ParseVersion(raw string) Version {
	v := Version{Raw: raw, Major: 0, Minor: 0, Patch: "", Suffix: "", Recognized: false}
	s := strings.TrimSpace(strings.TrimPrefix(raw, "tmux "))

	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return v
	}

	var err error

	v.Major, err = strconv.Atoi(m[1])
	if err != nil {
		return v
	}

	v.Minor, err = strconv.Atoi(m[2])
	if err != nil {
		return v
	}

	v.Patch = m[3]
	v.Suffix = m[4]
	v.Recognized = v.Suffix == ""

	return v
}

// AtLeast reports whether the version is a recognized stable release and is greater than
// or equal to the specified major and minor version numbers.
// Unrecognized versions (development or vendor builds) always return false.
func (v Version) AtLeast(major, minor int) bool {
	return v.Recognized && (v.Major > major || v.Major == major && v.Minor >= minor)
}

// String returns the raw version string originally parsed.
func (v Version) String() string { return v.Raw }

func supportedVersion(v Version) error {
	if !v.AtLeast(minMajorVersion, minMinorVersion) {
		return unsupportedVersion("tmux version (requires recognized stable 3.6+)", v)
	}

	return nil
}

// Version reports the version of the selected tmux binary without starting or contacting a daemon.
// It runs "tmux -V" via a quick subprocess. For servers already bound to an active daemon,
// it returns the version reported by the live daemon probe instead.
func (s *Server) Version(ctx context.Context) (Version, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return Version{}, opError("Version", err)
	}
	defer op.close()

	if s.bound != nil {
		info, err := s.probe(opCtx, op)
		return info.Version, opError("Version", err)
	}

	v, err := s.executableVersion(opCtx, op)

	return v, opError("Version", err)
}

func (s *Server) executableVersion(ctx context.Context, op *operation) (Version, error) {
	r, err := s.executeProcess(ctx, op, []string{"-V"}, nil)
	if err != nil {
		return Version{}, err
	}

	v := ParseVersion(strings.TrimSuffix(string(r.Stdout), "\n"))
	if v.Raw == "" {
		return Version{}, decodeError("version", "version", ErrProtocol)
	}

	return v, nil
}

// Capabilities probes the answering daemon to discover its supported feature set,
// version, and complete list of registered command names via list-commands.
// Requires a running server; does not start one automatically.
func (s *Server) Capabilities(ctx context.Context) (Capabilities, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return Capabilities{}, opError("Capabilities", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return Capabilities{}, opError("Capabilities", err)
	}

	c := Capabilities{
		Identity:  info.Identity,
		Version:   info.Version,
		Transport: s.Transport(),
		Support:   s.Support(),
		Commands:  nil,
	}

	r, err := s.execute(opCtx, op, recordsPlan(command("list-commands", "-F", codec.RecordFormat([]string{"command_list_name"}))), newGuard(info.Identity), nil)
	if err != nil {
		return Capabilities{}, opError("Capabilities", err)
	}

	rows, err := codec.ParseRecords(r.Stdout, 1)
	if err != nil {
		return Capabilities{}, afterError("Capabilities", decodeError("commands", "command_list_name", err))
	}

	c.Commands = make([]string, 0, len(rows))
	for _, row := range rows {
		if _, err := NewCommand(row[0]); err != nil {
			return Capabilities{}, afterError("Capabilities", decodeError("commands", "command_list_name", err))
		}

		c.Commands = append(c.Commands, row[0])
	}

	sort.Strings(c.Commands)

	return c, nil
}

func startupVersionCondition() string { return "#{m/r:" + supportedStablePattern + ",#{version}}" }

// HasCommand reports whether the daemon advertises a native command. It does not
// imply a typed library wrapper or availability over the selected transport.
func (c Capabilities) HasCommand(name string) bool {
	return slices.Contains(c.Commands, name)
}
