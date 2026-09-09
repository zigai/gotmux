//go:build go1.24

package tmux

import (
	"bufio"
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/zigai/gotmux/internal/codec"
)

func BenchmarkMetadataDecode(b *testing.B) {
	data := codec.EncodeRecord([]string{"%42", "path\twith\nseparators", "\xff", ""})
	if _, err := codec.ParseRecords(data, 4); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))

	for b.Loop() {
		if _, err := codec.ParseRecords(data, 4); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkControlFramedMetadata(b *testing.B) {
	wire := append([]byte("%begin 1 8 1\n"), codec.EncodeRecord([]string{"%end 1 8 1\nnot a delimiter"})...)
	wire = append(wire, []byte("%end 1 8 1\n")...)

	b.ReportAllocs()
	b.SetBytes(int64(len(wire)))

	for b.Loop() {
		if _, err := readControlUnit(bufio.NewReader(bytes.NewReader(wire)), 4096, func(Event) {}); err != nil {
			b.Fatal(err)
		}
	}
}

// Measures local snapshot copies, not daemon collection or application speedup.
func BenchmarkSnapshotLocalCopies(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("panes-%d", count), func(b *testing.B) {
			snapshot := Snapshot{
				sessions:     nil,
				windows:      nil,
				panes:        make([]PaneInfo, count),
				clients:      nil,
				links:        nil,
				Identity:     ServerIdentity{PID: 0, Started: time.Time{}, ReportedSocket: "", Endpoint: Endpoint{SocketPath: "", SocketName: "", TempDir: "", UID: 0}, Generation: 0},
				Started:      time.Time{},
				Finished:     time.Time{},
				Consistency:  0,
				MissingCount: 0,
				missing:      nil,
			}

			b.ReportAllocs()

			for b.Loop() {
				got := snapshot.Panes()
				if len(got) != count {
					b.Fatal("copy failed")
				}
			}
		})
	}
}

func BenchmarkGuardEncoding(b *testing.B) {
	g := newGuard(ServerIdentity{PID: 1, ReportedSocket: "/tmp/socket", Started: time.Unix(100, 0), Endpoint: Endpoint{SocketPath: "", SocketName: "", TempDir: "", UID: 0}, Generation: 0})

	p := g.wrap(emptyPlan(command("send-keys", "-t", "%1", "-l", "literal\n;$#")))
	if _, err := p.size(1 << 20); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := p.text(); err != nil {
			b.Fatal(err)
		}
	}
}
