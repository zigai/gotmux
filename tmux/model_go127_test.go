//go:build go1.27

package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// This model executes against the real queue implementation. It checks payload
// identity/order, retained terminal errors, and reservation conservation after
// overflow, normal closure, individual-read cancellation, and lifetime expiry.
func TestRapidEventStreamStateMachine(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		m := newRapidEventStreamModel(rt)
		defer m.cleanup()

		steps := rapid.IntRange(1, 120).Draw(rt, "steps")
		for step := range steps {
			action := rapid.IntRange(0, 5).Draw(rt, fmt.Sprintf("action-%d", step))
			switch action {
			case 0, 1:
				m.stepPush(step)
			case 2:
				m.stepNext()
			case 3:
				m.stepCancelRead()
			case 4:
				m.stepClose()
			case 5:
				m.stepExpireLifetime()
			}

			m.verifyInvariants()
		}
	})
}

type rapidEventStreamModel struct {
	rt       *rapid.T
	c        *Connection
	s        *EventStream
	end      context.CancelFunc
	expected [][]byte
	terminal error
}

func newRapidEventStreamModel(rt *rapid.T) *rapidEventStreamModel {
	_, cancel := context.WithCancelCause(context.Background())
	opts, _ := normalizeControlOptions(ControlOptions{PaneOutput: false, QueueDepth: 0, QueuedBytes: 0, FrameBytes: 0, EventBytes: 0, MaxStreams: 0})

	//nolint:exhaustruct_v5 // mock Connection test fixture intentionally omits unneeded fields
	c := &Connection{cancel: cancel, stopCh: make(chan struct{}), opts: opts, streams: map[*EventStream]struct{}{}, done: make(chan struct{})}

	life, end := context.WithCancel(context.Background())

	s, err := c.Events(life, EventOptions{MaxBytes: 4096, MaxCount: 4, Overflow: FailOnOverflow})
	if err != nil {
		rt.Fatal(err)
	}

	return &rapidEventStreamModel{
		rt:       rt,
		c:        c,
		s:        s,
		end:      end,
		expected: [][]byte{},
		terminal: nil,
	}
}

func (m *rapidEventStreamModel) cleanup() {
	_ = m.s.Close()
	m.end()
	m.c.stop(nil, true)
	m.c.work.Wait()
}

func (m *rapidEventStreamModel) stepPush(step int) {
	payload := []byte(strings.Repeat("x", rapid.IntRange(0, 32).Draw(m.rt, fmt.Sprintf("size-%d", step))))
	//nolint:modernize // reason: embedlit conflicts with exhaustruct_v5 requiring explicit embedded struct field
	event := PaneOutputEvent{eventBase: eventBase{name: "output", received: time.Now()}, PaneID: "%1", Age: UnavailableValue[time.Duration](), data: payload}
	m.s.push(event)

	if m.terminal != nil {
		return
	}

	if len(m.expected) == 4 {
		m.terminal = ErrEventsLost
		m.expected = nil

		return
	}

	m.expected = append(m.expected, append([]byte{}, payload...))
}

func (m *rapidEventStreamModel) stepNext() {
	if m.terminal == nil && len(m.expected) == 0 {
		return
	}

	got, err := m.s.Next(context.Background())
	if m.terminal != nil {
		if !errors.Is(err, m.terminal) {
			m.rt.Fatalf("terminal %v != %v", err, m.terminal)
		}

		return
	}

	if err != nil {
		m.rt.Fatal(err)
	}

	poe, ok := got.(PaneOutputEvent)
	if !ok {
		m.rt.Fatal("not PaneOutputEvent")
	}

	if diff := cmp.Diff(m.expected[0], poe.Data()); diff != "" {
		m.rt.Fatalf("payload mismatch (-want +got):\n%s", diff)
	}

	m.expected = m.expected[1:]
}

func (m *rapidEventStreamModel) stepCancelRead() {
	readCtx, stop := context.WithCancel(context.Background())
	stop()

	_, err := m.s.Next(readCtx)

	want := m.terminal
	if want == nil {
		want = context.Canceled
	}

	if !errors.Is(err, want) {
		m.rt.Fatalf("read cancellation %v != %v", err, want)
	}
}

func (m *rapidEventStreamModel) stepClose() {
	_ = m.s.Close()

	if m.terminal == nil {
		m.terminal = io.EOF
		m.expected = nil
	}
}

func (m *rapidEventStreamModel) stepExpireLifetime() {
	m.end()

	select {
	case <-m.s.done:
	case <-time.After(time.Second):
		m.rt.Fatal("lifetime cancellation stalled")
	}

	if m.terminal == nil {
		m.terminal = context.Canceled
		m.expected = nil
	}
}

func (m *rapidEventStreamModel) verifyInvariants() {
	m.s.mu.Lock()
	got := make([][]byte, 0, len(m.s.queue))

	var retained int64

	for _, ev := range m.s.queue {
		poe, ok := ev.(PaneOutputEvent)
		if !ok {
			m.rt.Fatal("not PaneOutputEvent")
		}

		got = append(got, poe.Data())
		retained += ev.eventBytes()
	}

	actualBytes := m.s.bytes
	actualTerminal := m.s.terminal
	m.s.mu.Unlock()

	want := m.expected
	if want == nil {
		want = [][]byte{}
	}

	if diff := cmp.Diff(want, got); diff != "" {
		m.rt.Fatalf("queue mismatch (-want +got):\n%s", diff)
	}

	if retained != actualBytes || actualBytes > 4096 {
		m.rt.Fatal("byte conservation violated")
	}

	if m.terminal != nil && !errors.Is(actualTerminal, m.terminal) {
		m.rt.Fatalf("terminal state changed: %v", actualTerminal)
	}

	m.c.mu.Lock()
	reservation := m.c.eventReserved
	m.c.mu.Unlock()

	if m.terminal != nil && reservation != 0 {
		m.rt.Fatal("terminal stream retained reservation")
	}
}

func TestCmpSnapshotCloneSemantics(t *testing.T) {
	s := localServer(t)

	record, err := s.decodePane(paneFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}

	//nolint:exhaustruct_v5 // testing snapshot field projection on partial test fixture
	snapshot := Snapshot{panes: []PaneInfo{record}}

	type view struct {
		ID        PaneID
		Window    WindowID
		Title     string
		Raw       []byte
		Mode      string
		ModeState ValueState
		Identity  ServerIdentity
	}

	project := func(p PaneInfo) view {
		raw, _ := p.Raw("pane_title")
		mode, _ := p.Mode.Get()

		return view{p.ID, p.WindowID, p.Title, raw, mode, p.Mode.State(), p.Handle().Identity()}
	}
	want := project(record)

	got := project(snapshot.Panes()[0])
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("snapshot clone (-want +got):\n%s", diff)
	}
}
