package tmux

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/zigai/gotmux/internal/wire"
)

func TestControlFramingAdversarialRecord(t *testing.T) {
	data := []string{"\n%end 5 99 1\n%begin 7 123 1\nTGO-DONE:fake\n\xff", ""}
	payload := wire.EncodeRecord(data)
	wire := append([]byte("%begin 5 99 1\n"), payload...)
	wire = append(wire, []byte("%end 5 99 1\n")...)

	u, e := readControlUnit(bufio.NewReaderSize(bytes.NewReader(wire), 16), 4096, func(Event) { t.Fatal("field misread as event") })
	if e != nil || u.frame == nil || !bytes.Equal(u.frame.data, payload) {
		t.Fatalf("%#v %v", u, e)
	}
}

func TestControlFramingMalformed(t *testing.T) {
	for _, wire := range []string{"%begin 1 3 1\n%end 1 4 1\n", "%begin 1 3 1\n%end 2 3 1\n", "%begin 1 3 1\n%end 1 3 0\n", "%begin 1 3 2\n%end 1 3 2\n", "%begin 1 3 1\n%begin 1 4 1\n", "%end 1 3 1\n", "%begin 1 3 1\nordinary unframed output\n%end 1 3 1\n", "%begin 1 3 1\nTGO1:1:99999999999999:x,\n"} {
		_, e := readControlUnit(bufio.NewReader(strings.NewReader(wire)), 4096, func(Event) {})
		if e == nil {
			t.Fatalf("accepted %q", wire)
		}
	}
}

func TestControlCommandNumbersMayHaveGaps(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("%begin 100 42 1\n%end 100 42 1\n%begin 100 928 1\n%end 100 928 1\n"))
	for _, n := range []uint64{42, 928} {
		u, e := readControlUnit(r, 4096, func(Event) {})
		if e != nil || u.frame.id.number != n {
			t.Fatal(u, e)
		}
	}
}

func TestControlErrorPreservesDiagnostic(t *testing.T) {
	u, e := readControlUnit(bufio.NewReader(strings.NewReader("%begin 1 4 1\nfailed command\n%error 1 4 1\n")), 4096, func(Event) {})
	if e != nil || !u.frame.failed || string(u.frame.data) != "failed command\n" {
		t.Fatal(u, e)
	}
}

func TestControlNotificationsAndLimits(t *testing.T) {
	var got []Event

	u, e := readControlUnit(bufio.NewReader(strings.NewReader("%begin 1 2 1\n%output %7 a\\012b\n%end 1 2 1\n")), 4096, func(e Event) { got = append(got, e) })
	if e != nil || u.frame == nil || len(got) != 1 {
		t.Fatal(u, e, got)
	}

	poe, ok := got[0].(PaneOutputEvent)
	if !ok || string(poe.Data()) != "a\nb" {
		t.Fatal(u, e, got)
	}

	_, e = readControlUnit(bufio.NewReader(strings.NewReader("%future-event "+strings.Repeat("x", 100)+"\n")), 20, func(Event) {})
	if !errors.Is(e, ErrOutputLimit) {
		t.Fatal(e)
	}
}

func TestControlFragmentation(t *testing.T) {
	r, w := io.Pipe()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer func() { _ = w.Close() }()

		for _, b := range []byte("%begin 123 456 1\nTGO1:1:3:abc,\n%end 123 456 1\n") {
			if _, e := w.Write([]byte{b}); e != nil {
				return
			}
		}
	}()

	u, e := readControlUnit(bufio.NewReaderSize(r, 16), 4096, func(Event) {})
	_ = r.Close()

	<-finished

	if e != nil || string(u.frame.data) != "TGO1:1:3:abc,\n" {
		t.Fatal(u, e)
	}
}

func TestEventByteOwnership(t *testing.T) {
	e, err := decodeEvent([]byte("%output %7 a\\000\\377\n"), 4096)
	if err != nil {
		t.Fatal(err)
	}

	p, ok := e.(PaneOutputEvent)
	if !ok {
		t.Fatal("expected PaneOutputEvent")
	}

	want := []byte{'a', 0, 255}
	if !bytes.Equal(p.Data(), want) {
		t.Fatal(p.Data())
	}

	b := p.Data()
	b[0] = 'x'

	if !bytes.Equal(p.Data(), want) {
		t.Fatal("payload alias")
	}

	e, err = decodeEvent([]byte("%future-event untouched payload\n"), 4096)

	ue, ok := e.(UnknownEvent)
	if err != nil || !ok || string(ue.Payload()) != "untouched payload" {
		t.Fatal(e, err)
	}
}

func TestControlEmptyOutputNotification(t *testing.T) {
	e, err := decodeEvent([]byte("%output %1\n"), 4096)
	if err != nil {
		t.Fatalf("decodeEvent(%%output %%1) failed: %v", err)
	}

	poe, ok := e.(PaneOutputEvent)
	if !ok || poe.PaneID != "%1" || len(poe.Data()) != 0 {
		t.Fatalf("unexpected event: %+v", e)
	}
}

func TestControlLayoutChangeNotifications(t *testing.T) {
	const layout = "a87f,100x30,0,0,2"

	for _, flags := range []string{"", "*Z"} {
		e, err := decodeEvent([]byte("%layout-change @1 "+layout+" "+layout+" "+flags+"\n"), 4096)
		if err != nil {
			t.Fatal(err)
		}

		changed, ok := e.(LayoutChangedEvent)
		if !ok || changed.WindowID != "@1" || changed.Layout != layout || changed.VisibleLayout != layout || changed.Flags != flags {
			t.Fatalf("layout notification lost fields: %+v", e)
		}
	}

	for _, line := range []string{
		"%layout-change @1 " + layout + " " + layout + "\n",
		"%layout-change @1  " + layout + " *\n",
		"%layout-change @1 " + layout + "  *\n",
	} {
		if _, err := decodeEvent([]byte(line), 4096); !errors.Is(err, ErrProtocol) {
			t.Fatalf("incomplete layout %q: %v", line, err)
		}
	}
}

func TestControlCommandErrorStartingWithPercent(t *testing.T) {
	wire := "%begin 1 4 1\n%: not a real event\n%error 1 4 1\n"

	u, err := readControlUnit(bufio.NewReader(strings.NewReader(wire)), 4096, func(Event) {})
	if err != nil {
		t.Fatalf("readControlUnit failed on percent-prefixed error: %v", err)
	}

	if u.frame == nil || !u.frame.failed || string(u.frame.data) != "%: not a real event\n" {
		t.Fatalf("unexpected frame: %+v", u.frame)
	}
}

func TestControlInterleavedEventsDoNotExhaustFrameBytes(t *testing.T) {
	var events []Event

	// Frame bytes limit is 256 bytes.
	// We send 10 interleaved %output events of 100 bytes each (total 1000 bytes).
	var b bytes.Buffer
	b.WriteString("%begin 1 10 1\n")

	for i := range 10 {
		fmt.Fprintf(&b, "%%output %%1 %s\n", strings.Repeat("a", 50))

		rec := wire.EncodeRecord([]string{fmt.Sprintf("data-%d", i)})
		b.Write(rec)
	}

	b.WriteString("%end 1 10 1\n")

	u, err := readControlUnit(bufio.NewReader(&b), 512, func(e Event) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("readControlUnit failed with interleaved events: %v", err)
	}

	if u.frame == nil || u.frame.failed {
		t.Fatalf("frame failed: %+v", u.frame)
	}

	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}
}

func TestControlTornRecordFramePreserved(t *testing.T) {
	// A torn record (e.g. from concurrent process fork/exec during format expansion)
	// has a length prefix mismatch, e.g. "TGO1:2:10:mismatched,5:extra,\n".
	// readControlUnit should align on the line boundary and preserve the frame output
	// so higher-level query retry (parseOrRetry) can inspect the torn record and retry,
	// rather than killing the entire control connection with a fatal protocol error.
	wire := "%begin 1 10 1\nTGO1:2:10:mismatched,5:extra,\n%end 1 10 1\n"

	u, err := readControlUnit(bufio.NewReader(strings.NewReader(wire)), 4096, func(Event) {})
	if err != nil {
		t.Fatalf("expected torn record line to be preserved within frame, got err: %v", err)
	}

	if u.frame == nil || u.frame.failed {
		t.Fatalf("expected successful frame holding raw output, got: %+v", u.frame)
	}

	if !strings.Contains(string(u.frame.data), "TGO1:2:10:mismatched") {
		t.Fatalf("expected frame data to contain torn record, got: %q", string(u.frame.data))
	}
}

func FuzzControlFrames(f *testing.F) {
	f.Add([]byte("%begin 1 4 1\n%end 1 4 1\n"))
	f.Add([]byte("%output %1 foo\\012bar\n"))
	f.Add([]byte("%extended-output %1 1250 : payload\\012data\n"))
	f.Add([]byte("%layout-change @1 100x30,0,0,0 [100x30,0,0,0] 0\n"))
	f.Add([]byte("%window-add @2 my-window\n"))
	f.Add([]byte("%window-close @2\n"))
	f.Add([]byte("%window-renamed @2 new-name\n"))
	f.Add([]byte("%session-changed $1 work\n"))
	f.Add([]byte("%sessions-changed\n"))
	f.Add([]byte("%subscription-changed sub1 $1 @2 %3 : payload data\n"))
	f.Add([]byte("%client-session-changed /dev/pts/1 $1\n"))
	f.Add([]byte("%client-detached /dev/pts/1\n"))
	f.Add([]byte("%exit\n"))
	f.Add([]byte("%error 1 2 1\nunknown command\n%end 1 2 1\n"))
	f.Add([]byte("%config-error bad syntax in line 10\n"))
	f.Add([]byte("%message server message\n"))
	f.Add([]byte("%client-flags-changed /dev/pts/1 read-only\n"))
	f.Add([]byte("%pane-mode-changed %2\n"))
	f.Add([]byte("%paste-buffer-changed mybuf\n"))
	f.Add([]byte("%paste-buffer-deleted mybuf\n"))
	f.Add([]byte("%window-pane-changed @1 %2\n"))
	f.Add([]byte("%pause %0\n"))
	f.Add([]byte("%continue %0\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 8192 {
			return
		}

		_, _ = readControlUnit(bufio.NewReader(bytes.NewReader(b)), 8192, func(Event) {})
	})
}

func FuzzEmbeddedFrameDelimiters(f *testing.F) {
	f.Add("%end 1 2 1\n")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 2048 {
			return
		}

		rawWire := fmt.Sprintf("%%begin 1 2 1\n%s%%end 1 2 1\n", wire.EncodeRecord([]string{s}))

		u, e := readControlUnit(bufio.NewReader(strings.NewReader(rawWire)), 8192, func(Event) {})
		if e != nil {
			t.Fatal(e)
		}

		rows, e := wire.ParseRecords(u.frame.data, 1)
		if e != nil || rows[0][0] != s {
			t.Fatal(e)
		}
	})
}

func TestControlCodecNoEchoPreambleAndTrailer(t *testing.T) {
	// Test that \x1bP1000p prefix before a frame is stripped cleanly
	raw := "\x1bP1000p%begin 1 10 1\nTGO-READY:hello\n%end 1 10 1\n"

	u, err := readControlUnit(bufio.NewReader(strings.NewReader(raw)), 4096, func(Event) {})
	if err != nil {
		t.Fatalf("expected readControlUnit to succeed after stripping DSC preamble, got: %v", err)
	}

	if u.frame == nil || string(u.frame.data) != "TGO-READY:hello\n" {
		t.Fatalf("unexpected frame data: %#v", u.frame)
	}

	// Test boundedLine with trailing \r
	r := bufio.NewReader(strings.NewReader("%session-changed $0 s1\r\n"))

	line, err := boundedLine(r, 4096)
	if err != nil {
		t.Fatalf("boundedLine failed on CRLF: %v", err)
	}

	if string(line) != "%session-changed $0 s1\n" {
		t.Fatalf("expected \\r to be stripped, got: %q", string(line))
	}

	// Test boundedLine EOF with exit trailer \x1b\
	r = bufio.NewReader(strings.NewReader("\x1b\\"))

	_, err = boundedLine(r, 4096)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF on trailing \\x1b\\, got %v", err)
	}
}

func TestControlStreamReader(t *testing.T) {
	// Stream contains DSC preamble, CRLF lines, a wire record, and exit trailer
	raw := "\x1bP1000p%begin 1 1 0\r\nTGO1:1:5:hello,\r\n%end 1 1 0\r\n%exit\r\n\x1b\\"
	cr := newControlStreamReader(strings.NewReader(raw))

	data, err := io.ReadAll(cr)
	if err != nil {
		t.Fatalf("io.ReadAll on controlStreamReader failed: %v", err)
	}

	expected := "%begin 1 1 0\nTGO1:1:5:hello,\n%end 1 1 0\n%exit\n\x1b\\"
	if string(data) != expected {
		t.Fatalf("stream normalization mismatch:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

func TestWireCodec_BufferBoundarySplits(t *testing.T) {
	// Reader buffer size is 32KB. We place a multi-byte UTF-8 rune exactly across the 32768 boundary.
	const bufSize = 32 << 10

	prefixLen := bufSize - len("%begin 1 1 0\n") - 1 // 1 byte before boundary
	padding := strings.Repeat("a", prefixLen)

	// 3-byte UTF-8 check mark: \xe2\x9c\x93
	checkMark := "✓"
	rawRecord := wire.EncodeRecord([]string{padding + checkMark + "suffix"})
	frame := fmt.Sprintf("%%begin 1 1 0\n%s%%end 1 1 0\n", rawRecord)

	reader := bufio.NewReaderSize(strings.NewReader(frame), bufSize)

	u, err := readControlUnit(reader, int64(len(frame)+1024), func(Event) {})
	if err != nil {
		t.Fatalf("readControlUnit failed across buffer boundary: %v", err)
	}

	if u.frame == nil {
		t.Fatal("expected non-nil frame")
	}

	rows, err := wire.ParseRecords(u.frame.data, 1)
	if err != nil {
		t.Fatalf("ParseRecords failed: %v", err)
	}

	if rows[0][0] != padding+checkMark+"suffix" {
		t.Fatalf("boundary split corrupted data: got %q, want %q", rows[0][0], padding+checkMark+"suffix")
	}
}

func TestControlStreamReader_CorruptedDSCAndTerminalNoise(t *testing.T) {
	// Terminal noise: incomplete ESC sequence, cursor movement, window title, raw bytes
	noise := "\x1b[?1049h\x1b[2J\x1b]0;terminal title\a\x1bP1000p"
	payload := "%begin 1 1 0\r\nTGO1:1:4:done,\r\n%end 1 1 0\r\n"
	trailer := "\x1b[?1049l\x1b\\"

	cr := newControlStreamReader(strings.NewReader(noise + payload + trailer))

	data, err := io.ReadAll(cr)
	if err != nil {
		t.Fatalf("io.ReadAll failed: %v", err)
	}

	expected := "%begin 1 1 0\nTGO1:1:4:done,\n%end 1 1 0\n"
	if !strings.Contains(string(data), expected) {
		t.Fatalf("corrupted noise was not cleaned properly:\ngot:  %q\nwant contains: %q", string(data), expected)
	}
}

type chunkReader struct{ chunks [][]byte }

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.chunks) > 0 && len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
	}
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	return n, nil
}

func TestNoEchoFramingBoundarySplits(t *testing.T) {
	record := wire.EncodeRecord([]string{"title with \x1b\\ inside"})
	// Model ordinary PTY ONLCR conversion (LF -> CRLF) before decoding -CC.
	raw := []byte("\x1bP1000p" + strings.ReplaceAll("%begin 1 1 1\n"+string(record)+"%end 1 1 1\n", "\n", "\r\n") + "\x1b\\")
	bad := 0
	for cut := 1; cut < len(raw); cut++ {
		r := &chunkReader{chunks: [][]byte{raw[:cut], raw[cut:]}}
		u, err := readControlUnit(bufio.NewReader(newControlStreamReader(r)), 4096, func(Event) {})
		if err != nil || u.frame == nil || !bytes.Equal(u.frame.data, record) {
			bad++
			t.Errorf("transport split at byte %d corrupts record: err=%v frame=%+v", cut, err, u.frame)
		}
	}
	if bad > 0 {
		t.Fatalf("bad boundaries: %d / %d", bad, len(raw)-1)
	}
}

func TestNoEchoFragmentedPreamble(t *testing.T) {
	record := wire.EncodeRecord([]string{"ordinary title"})
	raw := []byte("\x1bP1000p" + strings.ReplaceAll("%begin 1 1 1\n"+string(record)+"%end 1 1 1\n", "\n", "\r\n") + "\x1b\\")
	chunks := make([][]byte, len(raw))
	for i := range raw {
		chunks[i] = raw[i : i+1]
	}
	u, err := readControlUnit(bufio.NewReader(newControlStreamReader(&chunkReader{chunks: chunks})), 4096, func(Event) {})
	if err != nil || u.frame == nil || !bytes.Equal(u.frame.data, record) {
		t.Fatalf("fragmented preamble: %v %+v", err, u.frame)
	}
}
