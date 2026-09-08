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
		ctx, cancel := context.WithCancelCause(context.Background())
		opts, _ := normalizeControlOptions(ControlOptions{})
		c := &Connection{ctx: ctx, cancel: cancel, opts: opts, streams: map[*EventStream]struct{}{}}
		defer func() { c.stop(nil, true); c.work.Wait() }()
		life, end := context.WithCancel(context.Background())
		defer end()
		s, err := c.Events(life, EventOptions{MaxBytes: 4096, MaxCount: 4})
		if err != nil {
			rt.Fatal(err)
		}
		defer s.Close()
		expected := [][]byte{}
		var terminal error
		steps := rapid.IntRange(1, 120).Draw(rt, "steps")
		for step := 0; step < steps; step++ {
			action := rapid.IntRange(0, 5).Draw(rt, fmt.Sprintf("action-%d", step))
			switch action {
			case 0, 1:
				payload := []byte(strings.Repeat("x", rapid.IntRange(0, 32).Draw(rt, fmt.Sprintf("size-%d", step))))
				event := PaneOutputEvent{eventBase: eventBase{name: "output"}, PaneID: "%1", data: payload}
				s.push(event)
				if terminal == nil {
					if len(expected) == 4 {
						terminal = ErrEventsLost
						expected = nil
					} else {
						expected = append(expected, append([]byte{}, payload...))
					}
				}
			case 2:
				if terminal != nil || len(expected) > 0 {
					got, err := s.Next(context.Background())
					if terminal != nil {
						if !errors.Is(err, terminal) {
							rt.Fatalf("terminal %v != %v", err, terminal)
						}
					} else {
						if err != nil {
							rt.Fatal(err)
						}
						if diff := cmp.Diff(expected[0], got.(PaneOutputEvent).Data()); diff != "" {
							rt.Fatalf("payload mismatch (-want +got):\n%s", diff)
						}
						expected = expected[1:]
					}
				}
			case 3:
				readCtx, stop := context.WithCancel(context.Background())
				stop()
				_, err := s.Next(readCtx)
				want := terminal
				if want == nil {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					rt.Fatalf("read cancellation %v != %v", err, want)
				}
			case 4:
				s.Close()
				if terminal == nil {
					terminal = io.EOF
					expected = nil
				}
			case 5:
				end()
				select {
				case <-s.done:
				case <-time.After(time.Second):
					rt.Fatal("lifetime cancellation stalled")
				}
				if terminal == nil {
					terminal = context.Canceled
					expected = nil
				}
			}
			s.mu.Lock()
			got := [][]byte{}
			var retained int64
			for _, ev := range s.queue {
				got = append(got, ev.(PaneOutputEvent).Data())
				retained += ev.eventBytes()
			}
			actualBytes := s.bytes
			actualTerminal := s.terminal
			s.mu.Unlock()
			want := expected
			if want == nil {
				want = [][]byte{}
			}
			if diff := cmp.Diff(want, got); diff != "" {
				rt.Fatalf("queue mismatch (-want +got):\n%s", diff)
			}
			if retained != actualBytes || actualBytes > 4096 {
				rt.Fatal("byte conservation violated")
			}
			if terminal != nil && !errors.Is(actualTerminal, terminal) {
				rt.Fatalf("terminal state changed: %v", actualTerminal)
			}
			c.mu.Lock()
			reservation := c.eventReserved
			c.mu.Unlock()
			if terminal != nil && reservation != 0 {
				rt.Fatal("terminal stream retained reservation")
			}
		}
	})
}

func TestCmpSnapshotCloneSemantics(t *testing.T) {
	s := localServer(t)
	record, err := s.decodePane(paneFixture(s), nil)
	if err != nil {
		t.Fatal(err)
	}
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
