package tmux

import (
	"bytes"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	guardOK            = "TGO-GUARD-1:ok\n"
	guardServerChanged = "TGO-GUARD-1:server-changed\n"
	guardLinkChanged   = "TGO-GUARD-1:link-changed\n"
	guardClientChanged = "TGO-GUARD-1:client-changed\n"
)

// ServerIdentity captures the runtime identity of a specific tmux daemon instance.
//
// PID and start time have tmux's reported precision (normally whole seconds).
// Their reuse can collide if a daemon crashes and another starts within the same
// second with the recycled PID; this guards against accidental cross-daemon mutation,
// not hostile or cryptographic attackers.
//
// Generation is a process-local counter that is nonzero for control-bound handles
// and never reused within this process.
type ServerIdentity struct {
	// Endpoint records how the socket was originally selected.
	Endpoint Endpoint

	// ReportedSocket is the socket path reported by the daemon (#{socket_path}).
	ReportedSocket string

	// PID is the server process ID reported by tmux (#{pid}).
	PID int

	// Started is the start timestamp reported by tmux (#{start_time}).
	Started time.Time

	// Generation is nonzero for control-bound handles, uniquely identifying its connection lifetime.
	Generation uint64
}

type linkCheck struct {
	session SessionID
	index   int
	window  WindowID
}
type clientCheck struct {
	name    ClientName
	pid     int
	created int64
}
type guard struct {
	identity ServerIdentity
	links    []linkCheck
	clients  []clientCheck
}

func newGuard(id ServerIdentity) *guard {
	return &guard{identity: id, links: nil, clients: nil}
}

// Equal reports whether two identities describe the exact same daemon lifetime
// and the same control connection generation.
func (i ServerIdentity) Equal(other ServerIdentity) bool {
	return i.sameDaemon(other) && i.Generation == other.Generation
}

func (i ServerIdentity) sameDaemon(other ServerIdentity) bool {
	return i.Endpoint == other.Endpoint && i.ReportedSocket == other.ReportedSocket && i.PID == other.PID && i.Started.Equal(other.Started)
}

func (i ServerIdentity) valid() bool {
	return i.PID > 0 && !i.Started.IsZero() && i.ReportedSocket != ""
}

func (e Endpoint) isZero() bool {
	return e.SocketPath == "" && e.SocketName == "" && e.TempDir == "" && e.UID == 0
}

func (i ServerIdentity) isZero() bool {
	return i.PID == 0 && i.Started.IsZero() && i.ReportedSocket == "" && i.Endpoint.isZero() && i.Generation == 0
}

// formatBytes encodes literal operands as expansions, not as format syntax.
// Braces, commas, #, and arbitrary non-NUL bytes cannot alter a comparison.
func formatBytes(s string) string {
	var b strings.Builder
	for i := range len(s) {
		b.WriteString("#{a:")
		b.WriteString(strconv.Itoa(int(s[i])))
		b.WriteByte('}')
	}

	return b.String()
}

func eqFormat(variable, value string) string {
	return "#{==:#{" + variable + "}," + formatBytes(value) + "}"
}
func andFormat(a, b string) string { return "#{&&:" + a + "," + b + "}" }
func (i ServerIdentity) condition() string {
	return andFormat(andFormat(eqFormat("pid", strconv.Itoa(i.PID)), eqFormat("start_time", strconv.FormatInt(i.Started.Unix(), 10))), eqFormat("socket_path", i.ReportedSocket))
}

func markerNode(s string) wireNode {
	return leaf(command("display-message", "-p", strings.TrimSuffix(s, "\n")))
}

func conditionNode(condition string, yes, no []wireNode, target string) wireNode {
	n := wireNode{name: "if-shell", args: []wireArg{{text: "-F", nested: nil}}}
	if target != "" {
		n.args = append(n.args, wireArg{text: "-t", nested: nil}, wireArg{text: target, nested: nil})
	}

	n.args = append(n.args, wireArg{text: condition, nested: nil}, wireArg{text: "", nested: yes}, wireArg{text: "", nested: no})

	return n
}

func (g *guard) wrap(p plan) plan {
	nodes := append([]wireNode{markerNode(guardOK)}, p.nodes...)

	for _, c := range slices.Backward(g.clients) {
		cond := andFormat(eqFormat("client_name", string(c.name)), andFormat(eqFormat("client_pid", strconv.Itoa(c.pid)), eqFormat("client_created", strconv.FormatInt(c.created, 10))))
		// L: iterates clients in the answering daemon, not an arbitrary current client.
		cond = "#{L:#{?" + cond + ",1,}}"
		nodes = []wireNode{conditionNode(cond, nodes, []wireNode{markerNode(guardClientChanged)}, "")}
	}

	for _, l := range slices.Backward(g.links) {
		// Iterate memberships inside the exact session. Missing/renumbered indexes
		// produce false rather than allowing tmux's fuzzy/index fallback to retarget.
		cond := andFormat(eqFormat("window_id", string(l.window)), eqFormat("window_index", strconv.Itoa(l.index)))
		cond = "#{W:#{?" + cond + ",1,}}"
		nodes = []wireNode{conditionNode(cond, nodes, []wireNode{markerNode(guardLinkChanged)}, string(l.session)+":")}
	}

	p.nodes = []wireNode{conditionNode(g.identity.condition(), nodes, []wireNode{markerNode(guardServerChanged)}, "")}

	return p
}

func (g *guard) unwrap(r Result, err error) (Result, error) {
	cases := []struct {
		prefix string
		cause  error
	}{{guardServerChanged, ErrServerChanged}, {guardLinkChanged, ErrLinkChanged}, {guardClientChanged, ErrClientChanged}}
	for _, c := range cases {
		if bytes.HasPrefix(r.Stdout, []byte(c.prefix)) {
			r.Stdout = bytes.Clone(r.Stdout[len(c.prefix):])
			return r, &CommandError{Command: "guard", Result: cloneResult(r), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: errors.Join(c.cause, err)}
		}
	}

	if bytes.HasPrefix(r.Stdout, []byte(guardOK)) {
		r.Stdout = bytes.Clone(r.Stdout[len(guardOK):])

		if err != nil {
			if ce, ok := errors.AsType[*CommandError](err); ok {
				ce.Result = cloneResult(r)
			}
		}

		return r, err
	}

	if err != nil {
		if len(g.links) > 0 && errors.Is(err, ErrNotFound) {
			return r, &CommandError{Command: "guard", Result: cloneResult(r), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: errors.Join(ErrLinkChanged, err)}
		}

		return r, err
	}

	return r, &CommandError{Command: "guard", Result: cloneResult(r), Outcome: Outcome{Effect: Unknown, Steps: nil, Created: nil}, Timeout: NoTimeout, Err: ErrProtocol}
}
