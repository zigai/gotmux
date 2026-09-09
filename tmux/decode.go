package tmux

import (
	"strconv"
	"strings"
	"time"

	"github.com/zigai/gotmux/internal/codec"
	"github.com/zigai/gotmux/internal/schema"
)

type recordDecoder struct {
	kind string
	raw  map[string]string
	err  error
}

func (d *recordDecoder) fail(field string, err error) {
	if d.err == nil {
		d.err = decodeError(d.kind, field, err)
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
	v, err := strconv.Atoi(d.str(field))
	if err != nil {
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
	n, err := strconv.ParseInt(d.str(field), 10, 64)
	if err != nil || n < 0 {
		d.fail(field, codec.ErrRecord)
	}

	return time.Unix(n, 0)
}

func parseRaw(data []byte, fields []string, kind string) ([]map[string]string, error) {
	rows, err := codec.ParseRecords(data, len(fields))
	if err != nil {
		return nil, decodeError(kind, "record", err)
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
	id := ServerIdentity{Endpoint: s.endpoint, PID: d.nonnegative("pid"), Started: d.timestamp("start_time"), ReportedSocket: d.str("socket_path"), Generation: 0}
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
	d := &recordDecoder{kind: kind, raw: m, err: nil}

	id := s.decodeIdentity(d)
	if expected != nil && !id.sameDaemon(*expected) {
		d.fail("identity", ErrServerChanged)
	}

	v := ParseVersion(d.str("version"))
	if err := supportedVersion(v); err != nil {
		d.fail("version", err)
	}

	return d, id
}

func (s *Server) newHandle(id string, kind ObjectKind, origin ServerIdentity) handle {
	return handle{server: s, origin: origin, id: id, kind: kind, client: clientCheck{name: "", pid: 0, created: 0}}
}

func (s *Server) decodeSession(m map[string]string, expected *ServerIdentity) (SessionInfo, error) {
	d, id := s.decoder(m, "session", expected)

	sid := SessionID(d.str("session_id"))
	if !sid.Valid() {
		d.fail("session_id", codec.ErrRecord)
	}

	group := UnavailableValue[string]()
	if d.boolean("session_grouped") {
		group = PresentValue(d.str("session_group"))
	}

	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	v := SessionInfo{
		rawRecord:   rawRecord{raw: m},
		ID:          sid,
		Name:        d.str("session_name"),
		Path:        d.str("session_path"),
		Created:     d.timestamp("session_created"),
		Activity:    d.timestamp("session_activity"),
		Attached:    d.nonnegative("session_attached"),
		WindowCount: d.nonnegative("session_windows"),
		Group:       group,
		h:           s.newHandle(string(sid), SessionKind, id),
	}

	if d.err != nil {
		return SessionInfo{}, d.err
	}

	return v, nil
}

func (s *Server) decodeWindow(m map[string]string, expected *ServerIdentity) (WindowInfo, WindowLinkInfo, error) {
	d, id := s.decoder(m, "window", expected)

	wid := WindowID(d.str("window_id"))
	if !wid.Valid() {
		d.fail("window_id", codec.ErrRecord)
	}

	sid := SessionID(d.str("session_id"))
	if !sid.Valid() {
		d.fail("session_id", codec.ErrRecord)
	}

	wh := s.newHandle(string(wid), WindowKind, id)
	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	v := WindowInfo{
		rawRecord: rawRecord{raw: m},
		ID:        wid,
		Name:      d.str("window_name"),
		Width:     d.nonnegative("window_width"),
		Height:    d.nonnegative("window_height"),
		PaneCount: d.nonnegative("window_panes"),
		Layout:    Layout(d.str("window_layout")),
		Zoomed:    d.boolean("window_zoomed_flag"),
		h:         wh,
	}

	windex := d.nonnegative("window_index")
	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	l := WindowLinkInfo{
		rawRecord: rawRecord{raw: m},
		SessionID: sid,
		WindowID:  wid,
		Index:     windex,
		Active:    d.boolean("window_active"),
		Flags:     d.str("window_flags"),
		link:      WindowLink{h: wh, session: sid, index: windex},
	}

	if d.err != nil {
		return WindowInfo{}, WindowLinkInfo{}, d.err
	}

	return v, l, nil
}

func (s *Server) decodePane(m map[string]string, expected *ServerIdentity) (PaneInfo, error) {
	d, id := s.decoder(m, "pane", expected)

	pid := PaneID(d.str("pane_id"))
	if !pid.Valid() {
		d.fail("pane_id", codec.ErrRecord)
	}

	wid := WindowID(d.str("window_id"))
	if !wid.Valid() {
		d.fail("window_id", codec.ErrRecord)
	}

	dead := d.boolean("pane_dead")

	deadStatus := UnavailableValue[int]()
	if dead && d.str("pane_dead_status") != "" {
		deadStatus = PresentValue(d.integer("pane_dead_status"))
	}

	inMode := d.boolean("pane_in_mode")

	mode := PresentValue("")
	if inMode {
		mode = PresentValue(d.str("pane_mode"))
	}

	selectionVal := UnavailableValue[SelectionInfo]()

	selection := d.str("selection_present")
	if inMode && selection != "" && d.boolean("selection_present") {
		selectionVal = PresentValue(SelectionInfo{
			StartX:         d.nonnegative("selection_start_x"),
			StartY:         d.nonnegative("selection_start_y"),
			EndX:           d.nonnegative("selection_end_x"),
			EndY:           d.nonnegative("selection_end_y"),
			Rectangle:      d.boolean("rectangle_toggle"),
			ScrollPosition: d.nonnegative("scroll_position"),
		})
	}

	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	v := PaneInfo{
		rawRecord:      rawRecord{raw: m},
		ID:             pid,
		WindowID:       wid,
		Index:          d.nonnegative("pane_index"),
		Title:          d.str("pane_title"),
		CurrentPath:    d.str("pane_current_path"),
		CurrentCommand: d.str("pane_current_command"),
		PID:            d.nonnegative("pane_pid"),
		TTY:            d.str("pane_tty"),
		Width:          d.nonnegative("pane_width"),
		Height:         d.nonnegative("pane_height"),
		Left:           d.nonnegative("pane_left"),
		Top:            d.nonnegative("pane_top"),
		Active:         d.boolean("pane_active"),
		HistorySize:    d.nonnegative("history_size"),
		Alternate:      d.boolean("alternate_on"),
		Dead:           dead,
		DeadStatus:     deadStatus,
		Mode:           mode,
		Selection:      selectionVal,
		CursorX:        d.nonnegative("cursor_x"),
		CursorY:        d.nonnegative("cursor_y"),
		h:              s.newHandle(string(pid), PaneKind, id),
	}

	if d.err != nil {
		return PaneInfo{}, d.err
	}

	return v, nil
}

func (s *Server) decodeClient(m map[string]string, expected *ServerIdentity) (ClientInfo, error) {
	d, id := s.decoder(m, "client", expected)

	cname := ClientName(d.str("client_name"))
	if !cname.Valid() {
		d.fail("client_name", codec.ErrRecord)
	}

	flags := []string{}
	if f := d.str("client_flags"); f != "" {
		flags = strings.Split(f, ",")
	}

	sessionID := UnavailableValue[SessionID]()

	if sid := d.str("session_id"); sid != "" {
		if !SessionID(sid).Valid() {
			d.fail("session_id", codec.ErrRecord)
		}

		sessionID = PresentValue(SessionID(sid))
	}

	cpid := d.nonnegative("client_pid")
	created := d.timestamp("client_created")
	ch := s.newHandle(string(cname), ClientKind, id)
	ch.client = clientCheck{name: cname, pid: cpid, created: created.Unix()}

	control := d.boolean("client_control_mode")
	dimension := func(field string) int {
		// Control clients without terminal geometry may omit a dimension.
		if control && d.str(field) == "" {
			return 0
		}

		return d.nonnegative(field)
	}
	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	v := ClientInfo{
		rawRecord: rawRecord{raw: m},
		Name:      cname,
		TTY:       d.str("client_tty"),
		PID:       cpid,
		Created:   created,
		Activity:  d.timestamp("client_activity"),
		Width:     dimension("client_width"),
		Height:    dimension("client_height"),
		Control:   control,
		ReadOnly:  d.boolean("client_readonly"),
		Flags:     flags,
		SessionID: sessionID,
		h:         ch,
	}

	if d.err != nil {
		return ClientInfo{}, d.err
	}

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
