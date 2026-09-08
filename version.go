package tmux

import (
	"context"
	"example.com/tmux/internal/codec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Version retains the original spelling. Recognized is false for development
// and unrecognized vendor builds: they are not assumed newer than stable tmux.
type Version struct {
	Raw        string
	Major      int
	Minor      int
	Patch      string
	Suffix     string
	Recognized bool
}

var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)([a-z]?)(.*)$`)

func ParseVersion(raw string) Version {
	v := Version{Raw: raw}
	s := strings.TrimSpace(strings.TrimPrefix(raw, "tmux "))
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return v
	}
	var e error
	v.Major, e = strconv.Atoi(m[1])
	if e != nil {
		return v
	}
	v.Minor, e = strconv.Atoi(m[2])
	if e != nil {
		return v
	}
	v.Patch = m[3]
	v.Suffix = m[4]
	v.Recognized = v.Suffix == ""
	return v
}
func (v Version) AtLeast(major, minor int) bool {
	return v.Recognized && (v.Major > major || v.Major == major && v.Minor >= minor)
}
func (v Version) String() string { return v.Raw }
func supportedVersion(v Version) error {
	if !v.AtLeast(3, 6) {
		return &UnsupportedError{Feature: "tmux version (requires recognized stable 3.6+)", Version: v}
	}
	return nil
}

// Version reports the selected executable's version without starting a daemon.
// Probe.Version describes the answering daemon instead.
func (s *Server) Version(ctx context.Context) (Version, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return Version{}, opError("Version", e)
	}
	defer op.close()
	if s.bound != nil {
		info, err := s.probe(op)
		return info.Version, opError("Version", err)
	}
	v, e := s.executableVersion(op)
	return v, opError("Version", e)
}
func (s *Server) executableVersion(op *operation) (Version, error) {
	r, e := s.executeProcess(op, []string{"-V"}, nil)
	if e != nil {
		return Version{}, e
	}
	v := ParseVersion(strings.TrimSuffix(string(r.Stdout), "\n"))
	if v.Raw == "" {
		return Version{}, decodeError("version", "version", ErrProtocol)
	}
	return v, nil
}

// Capabilities is a copied observation, not a mutable global cache.
type Capabilities struct {
	Identity          ServerIdentity
	Version           Version
	Transport         Transport
	ControlMode       bool
	BinaryBuffers     bool
	CaptureModeScreen bool
	FloatingPanes     bool
	Commands          []string
}

func (s *Server) Capabilities(ctx context.Context) (Capabilities, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return Capabilities{}, opError("Capabilities", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return Capabilities{}, opError("Capabilities", e)
	}
	c := Capabilities{Identity: info.Identity, Version: info.Version, Transport: s.Transport(), ControlMode: true,
		BinaryBuffers: s.conn == nil, CaptureModeScreen: true, FloatingPanes: false}
	// A presence in a newer binary is not a proof of typed floating-pane support.
	r, e := s.execute(op, recordsPlan(command("list-commands", "-F", codec.RecordFormat([]string{"command_list_name"}))), &guard{identity: info.Identity}, nil)
	if e != nil {
		return Capabilities{}, opError("Capabilities", e)
	}
	rows, e := codec.ParseRecords(r.Stdout, 1)
	if e != nil {
		return Capabilities{}, afterError("Capabilities", decodeError("commands", "command_list_name", e))
	}
	c.Commands = make([]string, 0, len(rows))
	for _, row := range rows {
		if _, e := NewCommand(row[0]); e != nil {
			return Capabilities{}, afterError("Capabilities", decodeError("commands", "command_list_name", e))
		}
		c.Commands = append(c.Commands, row[0])
	}
	sort.Strings(c.Commands)
	return c, nil
}

const supportedStablePattern = `^(([4-9]|[1-9][0-9]+)[.][0-9]+|3[.]([6-9]|[1-9][0-9]+))[a-z]?$`

func startupVersionCondition() string { return "#{m/r:" + supportedStablePattern + ",#{version}}" }

const unsupportedStartupVersion = "TGO-GUARD-1:unsupported-version\n"
