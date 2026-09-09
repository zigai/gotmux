package tmux

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zigai/gotmux/internal/codec"
	"github.com/zigai/gotmux/internal/schema"
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
	m["pane_active"] = "1"
	m["pane_width"] = "80"
	m["pane_height"] = "24"
	m["pane_title"] = "tabs\tnewline\n;#{pane_id}\xff"

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

	words, e := codec.ParseWords(strings.TrimSuffix(text, "\n"))
	if e != nil {
		t.Fatal(e)
	}

	branches, e := codec.SplitSequence(words[len(words)-2])
	if e != nil || len(branches) != 2 {
		t.Fatal(branches, e)
	}

	w, e := codec.ParseWords(branches[1])
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

	objects := recoverCreated(codec.EncodeRecord(values), PaneKind)
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
