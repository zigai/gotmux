package tmux

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/gotmux/internal/schema"
	"github.com/zigai/gotmux/internal/wire"
)

func paneFixture(s *Server) map[string]string {
	m := map[string]string{}
	for _, f := range schema.WithIdentity(schema.Pane) {
		m[f] = "0"
	}

	for _, f := range []string{"pane_title", "pane_current_path", "pane_current_command", "pane_tty", "pane_dead_status", "pane_mode", "selection_present"} {
		m[f] = ""
	}

	m["pid"] = "42"
	m["start_time"] = "100"
	m["socket_path"] = s.endpoint.String()
	m["version"] = "3.6"
	m["pane_id"] = "%7"
	m["window_id"] = "@2"
	m["session_id"] = "$0"
	m["session_name"] = "test"
	m["window_name"] = "win"
	m["window_index"] = "0"
	m["pane_active"] = "1"
	m["pane_width"] = "80"
	m["pane_height"] = "24"
	m["pane_title"] = "tabs\tnewline\n;#{pane_id}\xff"

	return m
}

func sessionFixture(s *Server) map[string]string {
	m := map[string]string{}
	for _, f := range schema.WithIdentity(schema.Session) {
		m[f] = "0"
	}

	for _, f := range []string{"session_path", "session_group"} {
		m[f] = ""
	}

	m["pid"] = "42"
	m["start_time"] = "100"
	m["socket_path"] = s.endpoint.String()
	m["version"] = "3.6"
	m["session_id"] = "$0"
	m["session_name"] = "unit-session"
	m["session_created"] = "100"
	m["session_activity"] = "100"
	m["session_attached"] = "1"
	m["session_windows"] = "1"
	m["session_grouped"] = "0"

	return m
}

func windowFixture(s *Server) map[string]string {
	m := map[string]string{}
	for _, f := range schema.WithIdentity(schema.Window) {
		m[f] = "0"
	}

	for _, f := range []string{"window_flags"} {
		m[f] = ""
	}

	m["pid"] = "42"
	m["start_time"] = "100"
	m["socket_path"] = s.endpoint.String()
	m["version"] = "3.6"
	m["window_id"] = "@2"
	m["session_id"] = "$0"
	m["window_name"] = "unit-window"
	m["window_width"] = "80"
	m["window_height"] = "24"
	m["window_panes"] = "1"
	m["window_layout"] = "bb62,80x24,0,0,1"
	m["window_zoomed_flag"] = "0"
	m["window_index"] = "0"
	m["window_active"] = "1"

	return m
}

func clientFixture(s *Server) map[string]string {
	m := map[string]string{}
	for _, f := range schema.WithIdentity(schema.Client) {
		m[f] = "0"
	}

	for _, f := range []string{"client_flags"} {
		m[f] = ""
	}

	m["pid"] = "42"
	m["start_time"] = "100"
	m["socket_path"] = s.endpoint.String()
	m["version"] = "3.6"
	m["client_name"] = "/dev/pts/1"
	m["client_tty"] = "/dev/pts/1"
	m["client_pid"] = "1234"
	m["client_created"] = "100"
	m["client_activity"] = "100"
	m["client_width"] = "80"
	m["client_height"] = "24"
	m["session_id"] = "$0"
	m["client_control_mode"] = "0"
	m["client_readonly"] = "0"

	return m
}

func TestRecordProvenanceAndRawOwnership(t *testing.T) {
	s := localServer(t)

	p, e := s.decodePane(paneFixture(s), nil)
	if e != nil {
		t.Fatal(e)
	}

	h := p.Handle()
	if !h.Valid() {
		t.Fatal("missing provenance")
	}

	p.ID = "%99"

	p.WindowID = "@99"
	if !p.Handle().Equal(h) || p.Handle().ID() != "%7" {
		t.Fatal("exported ID retargeted handle")
	}

	raw, _ := p.Raw("pane_title")
	raw[0] = 'x'

	again, _ := p.Raw("pane_title")
	if reflect.DeepEqual(raw, again) {
		t.Fatal("aliased raw")
	}

	//nolint:exhaustruct_v5 // test verifies manual uninitialized record cannot fabricate valid handle provenance
	if (PaneInfo{ID: "%7"}).Handle().Valid() {
		t.Fatal("manual record fabricated provenance")
	}

	mode, ok := p.Mode.Get()
	if !ok || mode != "" || p.Selection.State() != Unavailable {
		t.Fatal("presence")
	}
}

func TestDecodeMalformedIsNotZero(t *testing.T) {
	for _, field := range []string{"pane_width", "pane_active", "pane_id", "version", "start_time"} {
		s := localServer(t)
		m := paneFixture(s)
		m[field] = "malformed"
		_, e := s.decodePane(m, nil)

		if _, ok := errors.AsType[*DecodeError](e); !ok {
			t.Fatalf("%s: %v", field, e)
		}
	}
}

func TestDecodeChangedDaemon(t *testing.T) {
	s := localServer(t)
	id := fixtureIdentity(s)
	m := paneFixture(s)
	m["pid"] = "43"

	_, e := s.decodePane(m, &id)
	if !errors.Is(e, ErrServerChanged) {
		t.Fatal(e)
	}
}

func TestSelectionCoordinates(t *testing.T) {
	s := localServer(t)
	m := paneFixture(s)
	m["pane_in_mode"] = "1"
	m["pane_mode"] = "copy-mode"
	m["selection_present"] = "1"
	m["selection_start_y"] = "1000"
	m["selection_end_y"] = "1003"
	m["scroll_position"] = "20"

	p, e := s.decodePane(m, nil)
	if e != nil {
		t.Fatal(e)
	}

	sel, ok := p.Selection.Get()
	if !ok || sel.StartY != 1000 || sel.EndY != 1003 || sel.ScrollPosition != 20 {
		t.Fatal(sel)
	}
}

func TestSnapshotCopiesAndChurn(t *testing.T) {
	s := localServer(t)

	p, e := s.decodePane(paneFixture(s), nil)
	if e != nil {
		t.Fatal(e)
	}

	//nolint:exhaustruct_v5 // testing snapshot field copying on partial test fixture
	snap := Snapshot{panes: []PaneInfo{p}, clients: []ClientInfo{{Name: "x", Flags: []string{"read-only"}}}}
	snap.assess()

	if snap.Consistency != Incomplete || snap.MissingCount != 1 {
		t.Fatal(snap)
	}

	panes := snap.Panes()

	panes[0].ID = "%99"
	if snap.Panes()[0].ID != "%7" {
		t.Fatal("slice alias")
	}

	clients := snap.Clients()

	clients[0].Flags[0] = "changed"
	if snap.Clients()[0].Flags[0] != "read-only" {
		t.Fatal("nested alias")
	}

	if h, ok := snap.ResolvePane("%7"); !ok || !h.Equal(p.Handle()) {
		t.Fatal("resolve lost provenance")
	}

	refs := snap.MissingReferences()

	refs[0].Reason = "changed"
	if snap.MissingReferences()[0].Reason == "changed" {
		t.Fatal("reference alias")
	}
}

func TestSnapshotMissingDetailsBounded(t *testing.T) {
	var snap Snapshot
	for range 1000 {
		//nolint:exhaustruct_v5 // testing bounding of missing references on partial test fixture
		snap.panes = append(snap.panes, PaneInfo{ID: "%1", WindowID: "@999"})
	}

	snap.assess()

	if snap.MissingCount != 1000 || len(snap.MissingReferences()) != 128 {
		t.Fatal("unbounded missing references")
	}
}

func TestGuardWrap(t *testing.T) {
	s := localServer(t)
	g := &guard{identity: fixtureIdentity(s), links: []linkCheck{{session: "$1", index: 0, window: "@2"}}, clients: nil}
	p := g.wrap(emptyPlan(command("kill-pane", "-t", "%7")))

	argv, e := p.argv()
	if e != nil {
		t.Fatal(e)
	}

	combined := strings.Join(argv, " ")
	if argv[0] != "if-shell" || !strings.Contains(combined, "start_time") || !strings.Contains(combined, "window_index") {
		t.Fatal(argv)
	}

	text, e := p.text()
	if e != nil {
		t.Fatal(e)
	}

	size, e := p.size(1 << 20)
	if e != nil || int64(len(text)) != size {
		t.Fatal(size, len(text), e)
	}
}

func TestGuardUnwrap(t *testing.T) {
	s := localServer(t)
	g := &guard{identity: fixtureIdentity(s), links: []linkCheck{{session: "$1", index: 0, window: "@2"}}, clients: nil}

	_, e := g.unwrap(Result{Stdout: []byte(guardServerChanged), Stderr: nil, ExitCode: 0}, nil)
	if !errors.Is(e, ErrServerChanged) || outcomeOf(e).Effect != NotSent {
		t.Fatal(e)
	}

	r, e := g.unwrap(Result{Stdout: append([]byte(guardOK), []byte("payload\n")...), Stderr: nil, ExitCode: 0}, nil)
	if e != nil || string(r.Stdout) != "payload\n" {
		t.Fatal(r, e)
	}

	_, e = g.unwrap(Result{Stdout: nil, Stderr: nil, ExitCode: 0}, nil)
	if !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
}

func TestNestedCommandEncoding(t *testing.T) {
	s := localServer(t)
	want := `a"; kill-server ; "$HOME #{pane_id}`
	g := newGuard(fixtureIdentity(s))
	p := g.wrap(emptyPlan(command("send-keys", "-l", "--", want)))

	text, e := p.text()
	if e != nil {
		t.Fatal(e)
	}

	words, e := wire.ParseWords(strings.TrimSuffix(text, "\n"))
	if e != nil {
		t.Fatal(e)
	}

	branches, e := wire.SplitSequence(words[len(words)-2])
	if e != nil || len(branches) != 2 {
		t.Fatal(branches, e)
	}

	w, e := wire.ParseWords(branches[1])
	if e != nil || w[len(w)-1] != want {
		t.Fatal(w, e)
	}
}

func TestPresence(t *testing.T) {
	var z Value[string]
	if _, ok := z.Get(); ok || z.State() != Unavailable {
		t.Fatal(z)
	}

	p := PresentValue("")
	if v, ok := p.Get(); !ok || v != "" || p.State() != Present {
		t.Fatal(p)
	}

	if UnsupportedValue[bool]().State() != Unsupported {
		t.Fatal("unsupported")
	}
}

func TestCreationRecoveryUsesObjectIDNotPID(t *testing.T) {
	s := localServer(t)
	m := paneFixture(s)
	fields := fieldsFor(PaneKind)

	values := make([]string, len(fields))
	for i, f := range fields {
		values[i] = m[f]
	}

	objects := recoverCreated(wire.EncodeRecord(values), PaneKind)
	if len(objects) != 1 || objects[0].RawID != "%7" || objects[0].Identity.State() != Unavailable {
		t.Fatalf("%#v", objects)
	}
}

func TestProbeFailureDoesNotDispatchRequestedMutation(t *testing.T) {
	e := opError("NewSession", &discoveryError{Err: &CommandError{Command: "", Result: failedResult(), Outcome: Outcome{Effect: Unknown, Steps: nil, Created: nil}, Timeout: NoTimeout, Err: ErrNoServer}})
	if outcomeOf(e).Effect != NotSent || !errors.Is(e, ErrNoServer) {
		t.Fatal(e)
	}
}

func TestQueryFieldsDeduplication(t *testing.T) {
	base := []string{"pid", "start_time", "socket_path", "version"}
	extra := []string{"socket_path", "session_id", "pid", "custom_field"}

	fields, err := queryFields(base, extra)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"pid", "start_time", "socket_path", "version", "session_id", "custom_field"}
	if len(fields) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, fields)
	}

	for i, f := range expected {
		if fields[i] != f {
			t.Fatalf("at %d: expected %s, got %s", i, f, fields[i])
		}
	}
}

func TestUnprobedHandles(t *testing.T) {
	s := localServer(t)

	p, err := s.PaneHandle("%42")
	if err != nil || !p.Valid() || p.ID() != "%42" {
		t.Fatalf("pane handle: %v %v", p, err)
	}

	sess, err := s.SessionHandle("$1")
	if err != nil || !sess.Valid() || sess.ID() != "$1" {
		t.Fatalf("session handle: %v %v", sess, err)
	}

	w, err := s.WindowHandle("@3")
	if err != nil || !w.Valid() || w.ID() != "@3" {
		t.Fatalf("window handle: %v %v", w, err)
	}

	testInvalidUnprobedHandles(t, s)
}

func testInvalidUnprobedHandles(t *testing.T, s *Server) {
	t.Helper()

	if _, err := s.PaneHandle("invalid"); err == nil {
		t.Fatal("expected error on invalid pane ID")
	}

	if _, err := s.SessionHandle("invalid"); err == nil {
		t.Fatal("expected error on invalid session ID")
	}

	if _, err := s.WindowHandle("invalid"); err == nil {
		t.Fatal("expected error on invalid window ID")
	}
}

func TestSnapshotResolveEmpty(t *testing.T) {
	var snap Snapshot

	w, ok := snap.ResolveWindow("@1")
	if ok || w.Valid() {
		t.Fatalf("expected empty snapshot window resolution to fail, got ok=%v, w=%v", ok, w)
	}

	sess, ok := snap.ResolveSession("$0")
	if ok || sess.Valid() {
		t.Fatalf("expected empty snapshot session resolution to fail, got ok=%v, sess=%v", ok, sess)
	}

	c, ok := snap.ResolveClient("/dev/pts/1")
	if ok || c.Valid() {
		t.Fatalf("expected empty snapshot client resolution to fail, got ok=%v, c=%v", ok, c)
	}
}

func assertResolvedWindow(t *testing.T, snap Snapshot, id WindowID, want Window, origin ServerIdentity) {
	t.Helper()

	w, ok := snap.ResolveWindow(id)
	if !ok || !w.Valid() || w.ID() != id || !w.Equal(want) || !w.Identity().Equal(origin) {
		t.Fatalf("unexpected window resolution: ok=%v, handle=%v", ok, w)
	}

	wInfo, ok := snap.Window(id)
	if !ok || !wInfo.Handle().Equal(w) {
		t.Fatalf("snapshot.Window mismatch: ok=%v, info=%v", ok, wInfo)
	}
}

func assertResolvedSession(t *testing.T, snap Snapshot, id SessionID, want Session, origin ServerIdentity) {
	t.Helper()

	sess, ok := snap.ResolveSession(id)
	if !ok || !sess.Valid() || sess.ID() != id || !sess.Equal(want) || !sess.Identity().Equal(origin) {
		t.Fatalf("unexpected session resolution: ok=%v, handle=%v", ok, sess)
	}

	sInfo, ok := snap.Session(id)
	if !ok || !sInfo.Handle().Equal(sess) {
		t.Fatalf("snapshot.Session mismatch: ok=%v, info=%v", ok, sInfo)
	}
}

func assertResolvedClient(t *testing.T, snap Snapshot, name ClientName, want Client, origin ServerIdentity) {
	t.Helper()

	c, ok := snap.ResolveClient(name)
	if !ok || !c.Valid() || c.Name() != name || !c.Equal(want) || !c.Identity().Equal(origin) {
		t.Fatalf("unexpected client resolution: ok=%v, handle=%v", ok, c)
	}

	cInfo, ok := snap.Client(name)
	if !ok || !cInfo.Handle().Equal(c) {
		t.Fatalf("snapshot.Client mismatch: ok=%v, info=%v", ok, cInfo)
	}
}

func assertResolveMissing[K ~string, V interface{ Valid() bool }](t *testing.T, resolve func(K) (V, bool), keys []K) {
	t.Helper()

	for _, k := range keys {
		v, ok := resolve(k)
		if ok || v.Valid() {
			t.Fatalf("expected missing resolution for %q to fail, got ok=%v, handle=%v", k, ok, v)
		}
	}
}

func TestSnapshotResolveSuccessAndProvenance(t *testing.T) {
	s := localServer(t)

	sessRecord, err := s.decodeSession(sessionFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	winRecord, _, err := s.decodeWindow(windowFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	clientRecord, err := s.decodeClient(clientFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	expectedOrigin := fixtureIdentity(s)

	//nolint:exhaustruct_v5 // testing snapshot resolve on decoded records
	snap := Snapshot{
		sessions: []SessionInfo{sessRecord},
		windows:  []WindowInfo{winRecord},
		clients:  []ClientInfo{clientRecord},
	}

	assertResolvedWindow(t, snap, "@2", winRecord.Handle(), expectedOrigin)
	assertResolvedSession(t, snap, "$0", sessRecord.Handle(), expectedOrigin)
	assertResolvedClient(t, snap, "/dev/pts/1", clientRecord.Handle(), expectedOrigin)
}

func TestSnapshotResolveMissing(t *testing.T) {
	s := localServer(t)

	sessRecord, err := s.decodeSession(sessionFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	winRecord, _, err := s.decodeWindow(windowFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	clientRecord, err := s.decodeClient(clientFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	//nolint:exhaustruct_v5 // testing snapshot resolve on decoded records
	snap := Snapshot{
		sessions: []SessionInfo{sessRecord},
		windows:  []WindowInfo{winRecord},
		clients:  []ClientInfo{clientRecord},
	}

	assertResolveMissing(t, snap.ResolveWindow, []WindowID{"@999", ""})
	assertResolveMissing(t, snap.ResolveSession, []SessionID{"$999", ""})
	assertResolveMissing(t, snap.ResolveClient, []ClientName{"/dev/pts/nonexistent", ""})
}

func TestResolveClientEqualityRecyclingAndDaemonIsolation(t *testing.T) {
	s1 := localServer(t)

	c1, err := s1.decodeClient(clientFixture(s1), nil)
	if err != nil {
		t.Fatal(err)
	}

	h1 := c1.Handle()

	// Different PID (same name and daemon)
	mDiffPID := clientFixture(s1)
	mDiffPID["client_pid"] = "9999"

	cDiffPID, err := s1.decodeClient(mDiffPID, nil)
	if err != nil {
		t.Fatal(err)
	}

	if h1.Equal(cDiffPID.Handle()) {
		t.Fatal("expected different client PID to break handle equality")
	}

	// Different creation timestamp (same name, PID, and daemon)
	mDiffCreated := clientFixture(s1)
	mDiffCreated["client_created"] = "500"

	cDiffCreated, err := s1.decodeClient(mDiffCreated, nil)
	if err != nil {
		t.Fatal(err)
	}

	if h1.Equal(cDiffCreated.Handle()) {
		t.Fatal("expected different client creation timestamp to break handle equality")
	}

	// Different daemon origin (different localServer)
	s2 := localServer(t)
	mDiffDaemon := clientFixture(s2)

	cDiffDaemon, err := s2.decodeClient(mDiffDaemon, nil)
	if err != nil {
		t.Fatal(err)
	}

	if h1.Equal(cDiffDaemon.Handle()) {
		t.Fatal("expected different daemon origin to break handle equality")
	}
}
