package tmux

import (
	"strconv"
	"strings"
	"time"

	"example.com/tmux/internal/codec"
	"example.com/tmux/internal/schema"
)

type recordDecoder struct {
	kind string
	raw  map[string]string
	err  error
}

func (d *recordDecoder) fail(field string, e error) {
	if d.err == nil {
		d.err = decodeError(d.kind, field, e)
	}
}
func (d *recordDecoder) str(field string) string {
	v, ok := d.raw[field]
	if !ok {
		d.fail(field, codec.ErrRecord)
	}
	return v
}
func (d *recordDecoder) integer(field string) int {
	v, e := strconv.Atoi(d.str(field))
	if e != nil {
		d.fail(field, codec.ErrRecord)
	}
	return v
}
func (d *recordDecoder) nonnegative(field string) int {
	v := d.integer(field)
	if v < 0 {
		d.fail(field, codec.ErrRecord)
	}
	return v
}
func (d *recordDecoder) boolean(field string) bool {
	v := d.str(field)
	if v != "0" && v != "1" {
		d.fail(field, codec.ErrRecord)
	}
	return v == "1"
}
func (d *recordDecoder) timestamp(field string) time.Time {
	n, e := strconv.ParseInt(d.str(field), 10, 64)
	if e != nil || n < 0 {
		d.fail(field, codec.ErrRecord)
	}
	return time.Unix(n, 0)
}
func parseRaw(data []byte, fields []string, kind string) ([]map[string]string, error) {
	rows, e := codec.ParseRecords(data, len(fields))
	if e != nil {
		return nil, decodeError(kind, "record", e)
	}
	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		m := make(map[string]string, len(fields))
		for i, f := range fields {
			m[f] = row[i]
		}
		out = append(out, m)
	}
	return out, nil
}
func (s *Server) decodeIdentity(d *recordDecoder) ServerIdentity {
	id := ServerIdentity{Endpoint: s.endpoint, PID: d.nonnegative("pid"), Started: d.timestamp("start_time"), ReportedSocket: d.str("socket_path")}
	if s.conn != nil {
		id.Generation = s.conn.generation
	} else if s.bound != nil {
		id.Generation = s.bound.Generation
	}
	if !id.valid() {
		d.fail("identity", codec.ErrRecord)
	}
	return id
}
func (s *Server) decoder(m map[string]string, kind string, expected *ServerIdentity) (*recordDecoder, ServerIdentity) {
	d := &recordDecoder{kind: kind, raw: m}
	id := s.decodeIdentity(d)
	if expected != nil && !id.sameDaemon(*expected) {
		d.fail("identity", ErrServerChanged)
	}
	v := ParseVersion(d.str("version"))
	if e := supportedVersion(v); e != nil {
		d.fail("version", e)
	}
	return d, id
}
func (s *Server) newHandle(id string, kind ObjectKind, origin ServerIdentity) handle {
	return handle{server: s, origin: origin, id: id, kind: kind}
}
func (s *Server) decodeSession(m map[string]string, expected *ServerIdentity) (SessionInfo, error) {
	d, id := s.decoder(m, "session", expected)
	v := SessionInfo{ID: SessionID(d.str("session_id")), Name: d.str("session_name"), Path: d.str("session_path"), Created: d.timestamp("session_created"), Activity: d.timestamp("session_activity"), Attached: d.nonnegative("session_attached"), WindowCount: d.nonnegative("session_windows"), rawRecord: rawRecord{raw: m}}
	if !v.ID.Valid() {
		d.fail("session_id", codec.ErrRecord)
	}
	if d.boolean("session_grouped") {
		v.Group = PresentValue(d.str("session_group"))
	}
	if d.err != nil {
		return SessionInfo{}, d.err
	}
	v.h = s.newHandle(string(v.ID), SessionKind, id)
	return v, nil
}
func (s *Server) decodeWindow(m map[string]string, expected *ServerIdentity) (WindowInfo, WindowLinkInfo, error) {
	d, id := s.decoder(m, "window", expected)
	v := WindowInfo{ID: WindowID(d.str("window_id")), Name: d.str("window_name"), Width: d.nonnegative("window_width"), Height: d.nonnegative("window_height"), PaneCount: d.nonnegative("window_panes"), Layout: Layout(d.str("window_layout")), Zoomed: d.boolean("window_zoomed_flag"), rawRecord: rawRecord{raw: m}}
	l := WindowLinkInfo{SessionID: SessionID(d.str("session_id")), WindowID: v.ID, Index: d.nonnegative("window_index"), Active: d.boolean("window_active"), Flags: d.str("window_flags"), rawRecord: rawRecord{raw: m}}
	if !v.ID.Valid() {
		d.fail("window_id", codec.ErrRecord)
	}
	if !l.SessionID.Valid() {
		d.fail("session_id", codec.ErrRecord)
	}
	if d.err != nil {
		return WindowInfo{}, WindowLinkInfo{}, d.err
	}
	v.h = s.newHandle(string(v.ID), WindowKind, id)
	l.link = WindowLink{h: v.h, session: l.SessionID, index: l.Index}
	return v, l, nil
}
func (s *Server) decodePane(m map[string]string, expected *ServerIdentity) (PaneInfo, error) {
	d, id := s.decoder(m, "pane", expected)
	v := PaneInfo{ID: PaneID(d.str("pane_id")), WindowID: WindowID(d.str("window_id")), Index: d.nonnegative("pane_index"), Title: d.str("pane_title"), CurrentPath: d.str("pane_current_path"), CurrentCommand: d.str("pane_current_command"), PID: d.nonnegative("pane_pid"), TTY: d.str("pane_tty"), Width: d.nonnegative("pane_width"), Height: d.nonnegative("pane_height"), Left: d.nonnegative("pane_left"), Top: d.nonnegative("pane_top"), Active: d.boolean("pane_active"), HistorySize: d.nonnegative("history_size"), Alternate: d.boolean("alternate_on"), Dead: d.boolean("pane_dead"), CursorX: d.nonnegative("cursor_x"), CursorY: d.nonnegative("cursor_y"), rawRecord: rawRecord{raw: m}}
	if !v.ID.Valid() {
		d.fail("pane_id", codec.ErrRecord)
	}
	if !v.WindowID.Valid() {
		d.fail("window_id", codec.ErrRecord)
	}
	if v.Dead && d.str("pane_dead_status") != "" {
		v.DeadStatus = PresentValue(d.integer("pane_dead_status"))
	}
	inMode := d.boolean("pane_in_mode")
	if inMode {
		v.Mode = PresentValue(d.str("pane_mode"))
	} else {
		v.Mode = PresentValue("")
	}
	// Selection fields are only meaningful in a copy-mode context. Empty values
	// outside that context are unavailable, not malformed numeric zeros.
	selection := d.str("selection_present")
	if inMode && selection != "" {
		if d.boolean("selection_present") {
			v.Selection = PresentValue(SelectionInfo{StartX: d.nonnegative("selection_start_x"), StartY: d.nonnegative("selection_start_y"), EndX: d.nonnegative("selection_end_x"), EndY: d.nonnegative("selection_end_y"), Rectangle: d.boolean("rectangle_toggle"), ScrollPosition: d.nonnegative("scroll_position")})
		}
	}
	if d.err != nil {
		return PaneInfo{}, d.err
	}
	v.h = s.newHandle(string(v.ID), PaneKind, id)
	return v, nil
}
func (s *Server) decodeClient(m map[string]string, expected *ServerIdentity) (ClientInfo, error) {
	d, id := s.decoder(m, "client", expected)
	v := ClientInfo{Name: ClientName(d.str("client_name")), TTY: d.str("client_tty"), PID: d.nonnegative("client_pid"), Created: d.timestamp("client_created"), Activity: d.timestamp("client_activity"), Width: d.nonnegative("client_width"), Height: d.nonnegative("client_height"), Control: d.boolean("client_control_mode"), ReadOnly: d.boolean("client_readonly"), Flags: []string{}, rawRecord: rawRecord{raw: m}}
	if f := d.str("client_flags"); f != "" {
		v.Flags = strings.Split(f, ",")
	}
	if sid := d.str("session_id"); sid != "" {
		if !SessionID(sid).Valid() {
			d.fail("session_id", codec.ErrRecord)
		}
		v.SessionID = PresentValue(SessionID(sid))
	}
	if !v.Name.Valid() {
		d.fail("client_name", codec.ErrRecord)
	}
	if d.err != nil {
		return ClientInfo{}, d.err
	}
	v.h = s.newHandle(string(v.Name), ClientKind, id)
	v.h.client = clientCheck{name: v.Name, pid: v.PID, created: v.Created.Unix()}
	return v, nil
}
func fieldsFor(kind ObjectKind) []string {
	switch kind {
	case SessionKind:
		return schema.WithIdentity(schema.Session)
	case WindowKind, LinkKind:
		return schema.WithIdentity(schema.Window)
	case PaneKind:
		return schema.WithIdentity(schema.Pane)
	case ClientKind:
		return schema.WithIdentity(schema.Client)
	}
	return append([]string{}, schema.Identity...)
}
