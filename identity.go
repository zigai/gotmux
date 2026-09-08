package tmux

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ServerIdentity is the daemon identity reported by the answering tmux server.
// PID and start time have tmux's precision, normally whole seconds. Their reuse
// can collide; this is not a cryptographic or hostile-daemon boundary. Generation
// is nonzero for a control-bound handle and never reused by this process.
type ServerIdentity struct {
	Endpoint       Endpoint
	ReportedSocket string
	PID            int
	Started        time.Time
	Generation     uint64
}

func (i ServerIdentity) Equal(other ServerIdentity) bool {
	return i.sameDaemon(other) && i.Generation == other.Generation
}
func (i ServerIdentity) sameDaemon(other ServerIdentity) bool {
	return i.Endpoint == other.Endpoint && i.ReportedSocket == other.ReportedSocket && i.PID == other.PID && i.Started.Equal(other.Started)
}
func (i ServerIdentity) valid() bool {
	return i.PID > 0 && !i.Started.IsZero() && i.ReportedSocket != ""
}

// formatBytes encodes literal operands as expansions, not as format syntax.
// Braces, commas, #, and arbitrary non-NUL bytes cannot alter a comparison.
func formatBytes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
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

const guardOK = "TGO-GUARD-1:ok\n"
const guardServerChanged = "TGO-GUARD-1:server-changed\n"
const guardLinkChanged = "TGO-GUARD-1:link-changed\n"
const guardClientChanged = "TGO-GUARD-1:client-changed\n"

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

func markerNode(s string) wireNode {
	return leaf(command("display-message", "-p", strings.TrimSuffix(s, "\n")))
}
func conditionNode(condition string, yes, no []wireNode, target string) wireNode {
	n := wireNode{name: "if-shell", args: []wireArg{{text: "-F"}}}
	if target != "" {
		n.args = append(n.args, wireArg{text: "-t"}, wireArg{text: target})
	}
	n.args = append(n.args, wireArg{text: condition}, wireArg{nested: yes}, wireArg{nested: no})
	return n
}
func (g *guard) wrap(p plan) plan {
	nodes := append([]wireNode{markerNode(guardOK)}, p.nodes...)
	for i := len(g.clients) - 1; i >= 0; i-- {
		c := g.clients[i]
		cond := andFormat(eqFormat("client_name", string(c.name)), andFormat(eqFormat("client_pid", strconv.Itoa(c.pid)), eqFormat("client_created", strconv.FormatInt(c.created, 10))))
		// L: iterates clients in the answering daemon, not an arbitrary current client.
		cond = "#{L:#{?" + cond + ",1,}}"
		nodes = []wireNode{conditionNode(cond, nodes, []wireNode{markerNode(guardClientChanged)}, "")}
	}
	for i := len(g.links) - 1; i >= 0; i-- {
		l := g.links[i]
		// Iterate memberships inside the exact session. Missing/renumbered indexes
		// produce false rather than allowing tmux's fuzzy/index fallback to retarget.
		cond := andFormat(eqFormat("window_id", string(l.window)), eqFormat("window_index", strconv.Itoa(l.index)))
		cond = "#{W:#{?" + cond + ",1,}}"
		nodes = []wireNode{conditionNode(cond, nodes, []wireNode{markerNode(guardLinkChanged)}, string(l.session)+":")}
	}
	p.nodes = []wireNode{conditionNode(g.identity.condition(), nodes, []wireNode{markerNode(guardServerChanged)}, "")}
	p.nested = true
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
			return r, &CommandError{Command: "guard", Result: cloneResult(r), Outcome: Outcome{Effect: NotSent}, Err: errors.Join(c.cause, err)}
		}
	}
	if bytes.HasPrefix(r.Stdout, []byte(guardOK)) {
		r.Stdout = bytes.Clone(r.Stdout[len(guardOK):])
		if err != nil {
			var ce *CommandError
			if errors.As(err, &ce) {
				ce.Result = cloneResult(r)
			}
		}
		return r, err
	}
	if err != nil {
		if len(g.links) > 0 && errors.Is(err, ErrNotFound) {
			return r, &CommandError{Command: "guard", Result: cloneResult(r), Outcome: Outcome{Effect: NotSent}, Err: errors.Join(ErrLinkChanged, err)}
		}
		return r, err
	}
	return r, &CommandError{Command: "guard", Result: cloneResult(r), Outcome: Outcome{Effect: Unknown}, Err: ErrProtocol}
}
