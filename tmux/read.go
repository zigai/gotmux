package tmux

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/zigai/gotmux/internal/schema"
	"github.com/zigai/gotmux/internal/wire"
)

// readBoundary separates request outputs; every request selects at least one field, so a zero-field record cannot be a row.
const readBoundary = "TGO1:0:"

// ReadRequest selects the server-wide lists returned by [Server.Read]. Each present
// entry is queried with its own [QueryOptions]; absent entries are not queried.
type ReadRequest struct {
	Sessions Value[QueryOptions]
	Windows  Value[QueryOptions]
	Panes    Value[QueryOptions]
	Clients  Value[QueryOptions]
}

// ReadResult holds the lists returned by [Server.Read], all answered by the daemon in Server.
type ReadResult struct {
	Server   ServerInfo
	Sessions []SessionInfo
	Windows  []WindowInfo
	Links    []WindowLinkInfo
	Panes    []PaneInfo
	Clients  []ClientInfo
}

type readRequest struct {
	kind   ObjectKind
	fields []string
	node   wireNode
}

func listRequest(kind ObjectKind, opts QueryOptions, target string, scope ...string) (readRequest, error) {
	fields, err := queryFields(fieldsFor(kind), opts.ExtraFields)
	if err != nil {
		return readRequest{}, err
	}

	return listFields(kind, fields, opts.Filter, target, scope...)
}

func recordQueryRequest(query RecordQuery) (readRequest, error) {
	fields, err := queryFields(nil, query.Fields)
	if err != nil {
		return readRequest{}, err
	}

	if len(fields) == 0 || len(fields) > wire.MaxRecordFields {
		return readRequest{}, invalid("field count")
	}

	return listFields(query.Kind, fields, query.Filter, "")
}

func listFields(kind ObjectKind, fields []string, filter Format, target string, scope ...string) (readRequest, error) {
	name, args, err := listCommandAndArgs(kind, target)
	if err != nil {
		return readRequest{}, err
	}

	args = append(args, scope...)

	if target != "" {
		args = append(args, "-t", target)
	}

	if filter != "" {
		if !wire.ValidString(string(filter)) {
			return readRequest{}, invalid("filter")
		}

		args = append(args, "-f", string(filter))
	}

	args = append(args, "-F", wire.RecordFormat(fields))

	return readRequest{kind: kind, fields: fields, node: leaf(command(name, args...))}, nil
}

func inspectRequest(kind ObjectKind, target string, opts QueryOptions) (readRequest, error) {
	fields, err := queryFields(fieldsFor(kind), opts.ExtraFields)
	if err != nil {
		return readRequest{}, err
	}

	c := command("display-message", "-p", "-t", target, wire.RecordFormat(fields))
	if kind == ObjectKindClient {
		// display-message -c expands session fields from the default target, not the client's session.
		c = command("list-clients", "-f", clientFilter(target), "-F", wire.RecordFormat(fields))
	}

	return readRequest{kind: kind, fields: fields, node: leaf(c)}, nil
}

func identityNode() wireNode {
	return leaf(command("display-message", "-p", wire.RecordFormat(schema.Identity)))
}

// readRecords runs requests as one tmux command list. A control connection verified its daemon
// when it attached; otherwise the list starts with an identity probe answered by the
// same daemon in the same round trip.
func (s *Server) readRecords(ctx context.Context, label string, op *operation, requests []readRequest) (ServerInfo, [][]map[string]string, error) {
	probed := s.conn == nil

	nodes := make([]wireNode, 0, 2*len(requests)+1)
	if probed {
		nodes = append(nodes, identityNode())
	}

	for i, r := range requests {
		if i > 0 {
			nodes = append(nodes, leaf(command("display-message", "-p", readBoundary)))
		}

		nodes = append(nodes, r.node)
	}

	p := plan{nodes: nodes, mode: replyRecords, allowStart: false}

	r, err := s.execute(ctx, op, p, nil, nil)
	if err != nil {
		if probed && !bytes.HasPrefix(r.Stdout, []byte(wire.RecordPrefix)) {
			return ServerInfo{}, nil, &discoveryError{Err: err}
		}

		return ServerInfo{}, nil, err
	}

	info, rows, err := s.decodeRead(r.Stdout, probed, requests)
	if err == nil {
		return info, rows, nil
	}

	if !errors.Is(err, wire.ErrRecord) {
		return ServerInfo{}, nil, readError(label, err)
	}

	time.Sleep(retryBackoff)

	retried, retryErr := s.execute(ctx, op, p, nil, nil)
	if retryErr != nil {
		return ServerInfo{}, nil, readError(label, err)
	}

	retriedInfo, retriedRows, decodeErr := s.decodeRead(retried.Stdout, probed, requests)
	if decodeErr != nil {
		return ServerInfo{}, nil, readError(label, err)
	}

	return retriedInfo, retriedRows, nil
}

func readError(label string, err error) error {
	if _, ok := errors.AsType[*discoveryError](err); ok {
		return err
	}

	return afterError(label, err)
}

func (s *Server) decodeRead(data []byte, probed bool, requests []readRequest) (ServerInfo, [][]map[string]string, error) {
	reader := bytes.NewReader(data)

	var info ServerInfo

	if !probed {
		info = s.conn.verified()
	} else {
		values, err := wire.ReadRecordFields(reader, int64(reader.Len()))
		if err != nil || len(values) != len(schema.Identity) {
			return ServerInfo{}, nil, &discoveryError{Err: decodeError("server", "record", wire.ErrRecord)}
		}

		info, err = s.serverInfo(recordMap(schema.Identity, values))
		if err != nil {
			return ServerInfo{}, nil, &discoveryError{Err: err}
		}
	}

	out := make([][]map[string]string, len(requests))
	current := 0

	for reader.Len() > 0 {
		values, err := wire.ReadRecordFields(reader, int64(reader.Len()))
		if err != nil {
			return ServerInfo{}, nil, decodeError(string(requests[current].kind), "record", err)
		}

		if len(values) == 0 {
			current++
			if current == len(requests) {
				return ServerInfo{}, nil, decodeError("read", "boundary", wire.ErrRecord)
			}

			continue
		}

		if len(values) != len(requests[current].fields) {
			return ServerInfo{}, nil, decodeError(string(requests[current].kind), "record", wire.ErrRecord)
		}

		out[current] = append(out[current], recordMap(requests[current].fields, values))
	}

	if current != len(requests)-1 {
		return ServerInfo{}, nil, decodeError("read", "boundary", wire.ErrRecord)
	}

	return info, out, nil
}

func recordMap(fields, values []string) map[string]string {
	m := make(map[string]string, len(fields))
	for i, f := range fields {
		m[f] = values[i]
	}

	return m
}

func (s *Server) serverInfo(raw map[string]string) (ServerInfo, error) {
	d := &recordDecoder{kind: "server", raw: raw, err: nil}
	id := s.decodeIdentity(d)

	if s.bound != nil && s.bound.valid() && d.err == nil && !id.sameDaemon(*s.bound) {
		d.fail("identity", ErrServerChanged)
	}

	v := ParseVersion(d.str("version"))
	if d.err != nil {
		return ServerInfo{}, afterError("Server.Probe", d.err)
	}

	if err := supportedVersion(v); err != nil {
		return ServerInfo{}, err
	}

	return ServerInfo{rawRecord: rawRecord{raw: raw}, Identity: id, Version: v}, nil
}

// verifiedInfo returns the daemon identity a control connection established at attach,
// or probes the daemon over a fresh subprocess.
func (s *Server) verifiedInfo(ctx context.Context, op *operation) (ServerInfo, error) {
	if s.conn != nil {
		return s.conn.verified(), nil
	}

	return s.probe(ctx, op)
}

// Read queries the requested lists in one tmux command list: one subprocess, or one
// request on a control connection. tmux runs the list without interleaving other
// clients' commands, so all lists observe the same daemon state. If any list fails,
// tmux skips the rest and Read returns the error without partial results.
func (s *Server) Read(ctx context.Context, request ReadRequest) (ReadResult, error) {
	const label = "Server.Read"

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return ReadResult{}, opError(label, err)
	}
	defer op.close()

	out, err := s.readLists(opCtx, label, op, request)

	return out, opError(label, err)
}

func (s *Server) readLists(ctx context.Context, label string, op *operation, request ReadRequest) (ReadResult, error) {
	requests, err := request.lists()
	if err != nil {
		return ReadResult{}, err
	}

	info, rows, err := s.readRecords(ctx, label, op, requests)
	if err != nil {
		return ReadResult{}, err
	}

	return s.decodeLists(label, info, requests, rows)
}

func (r ReadRequest) lists() ([]readRequest, error) {
	selected := []struct {
		kind ObjectKind
		opts Value[QueryOptions]
	}{
		{ObjectKindSession, r.Sessions},
		{ObjectKindWindow, r.Windows},
		{ObjectKindPane, r.Panes},
		{ObjectKindClient, r.Clients},
	}

	var requests []readRequest

	for _, entry := range selected {
		opts, ok := entry.opts.Get()
		if !ok {
			continue
		}

		req, err := listRequest(entry.kind, opts, "")
		if err != nil {
			return nil, err
		}

		requests = append(requests, req)
	}

	if len(requests) == 0 {
		return nil, invalid("read request")
	}

	return requests, nil
}

func (s *Server) decodeLists(label string, info ServerInfo, requests []readRequest, rows [][]map[string]string) (ReadResult, error) {
	out := ReadResult{Server: info, Sessions: nil, Windows: nil, Links: nil, Panes: nil, Clients: nil}

	var err error

	for i, req := range requests {
		switch req.kind {
		case ObjectKindSession:
			out.Sessions, err = s.decodeSessions(label, rows[i], info.Identity)
		case ObjectKindWindow:
			out.Windows, out.Links, err = s.decodeWindows(label, rows[i], info.Identity)
		case ObjectKindPane:
			out.Panes, err = s.decodePanes(label, rows[i], info.Identity)
		case ObjectKindClient:
			out.Clients, err = s.decodeClients(label, rows[i], info.Identity)
		case ObjectKindWindowLink:
			err = invalid("object kind")
		}

		if err != nil {
			return ReadResult{}, err
		}
	}

	return out, nil
}
