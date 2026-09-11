package wire

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	values := []string{"", "a\tb\r\nc", "\\\"'$;#{pane_id}", "\x00\xff\xfe", "%end 1 9 1\n%begin 1 10 1\n", strings.Repeat("x", 10000)}
	wire := EncodeRecord(values)

	gotWire, got, err := ReadRecord(bytes.NewReader(wire), int64(len(wire)))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(got, values) || !bytes.Equal(gotWire, wire) {
		t.Fatalf("round-trip mismatch: %#v", got)
	}

	gotWireOnly, err := ReadRecordWire(bytes.NewReader(wire), int64(len(wire)))
	if err != nil || !bytes.Equal(gotWireOnly, wire) {
		t.Fatalf("ReadRecordWire mismatch: %v", err)
	}

	gotFieldsOnly, err := ReadRecordFields(bytes.NewReader(wire), int64(len(wire)))
	if err != nil || !reflect.DeepEqual(gotFieldsOnly, values) {
		t.Fatalf("ReadRecordFields mismatch: %v", err)
	}

	rows, err := ParseRecords(append(wire, wire...), len(values))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}

	if _, _, err = ReadRecord(bytes.NewReader(wire), int64(len(wire)-1)); err == nil {
		t.Fatal("accepted over-limit record")
	}
}

func TestMalformedRecords(t *testing.T) {
	for _, data := range []string{"", "TGO2:0:\n", "TGO1:01:0:,\n", "TGO1:1:00:,\n", "TGO1:1:9999999999999999999999999:", "TGO1:1025:\n", "TGO1:1:3:ab,\n", "TGO1:1:0:;\n", "TGO1:0:\r\n", "TGO1:-1:\n", "TGO1:1:1:a,"} {
		t.Run(data, func(t *testing.T) {
			if _, _, err := ReadRecord(bytes.NewReader([]byte(data)), int64(len(data))); err == nil {
				t.Fatal("accepted malformed record")
			}
		})
	}

	rows, err := ParseRecords(nil, 2)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty result: %#v %v", rows, err)
	}

	if _, err = ParseRecords(EncodeRecord([]string{"x"}), 2); !errors.Is(err, ErrRecord) {
		t.Fatal(err)
	}
}

func TestQuotedWords(t *testing.T) {
	for _, text := range []string{"", "a b", "a\tb\n", `;'"\$#{}[]`, "\\;", "--", "\xff\xfe", "${HOME}", "$(touch never)", "`date`"} {
		var b strings.Builder
		if err := Quoted(&b, text); err != nil {
			t.Fatal(err)
		}

		words, err := ParseWords(b.String())
		if err != nil || len(words) != 1 || words[0] != text {
			t.Fatalf("%q -> %q -> %#v (%v)", text, b.String(), words, err)
		}
	}

	var b strings.Builder
	if err := Quoted(&b, "x\x00y"); err == nil {
		t.Fatal("NUL accepted")
	}
}

func TestArgvTrailingSemicolon(t *testing.T) {
	// Reference cmd_parse_from_arguments behavior from the pinned tmux parser.
	for _, input := range []string{";", "x;", `x\;`, `x\\;`, `a;b`, `\`, "", ";;"} {
		encoded := Argv(input)

		decoded := encoded
		if strings.HasSuffix(encoded, ";") {
			if len(encoded) < 2 || encoded[len(encoded)-2] != '\\' {
				t.Fatal("argument became command separator")
			}

			decoded = encoded[:len(encoded)-2] + ";"
		}

		if decoded != input {
			t.Fatalf("%q -> %q -> %q", input, encoded, decoded)
		}
	}
}

func TestOctal(t *testing.T) {
	got, err := Octal([]byte(`a\000\012\015\134\377`), 6)
	if err != nil || !bytes.Equal(got, []byte{'a', 0, '\n', '\r', '\\', 255}) {
		t.Fatalf("%q %v", got, err)
	}

	for _, s := range []string{`\`, `\12`, `\12x`, `\400`, `\999`} {
		if _, err := Octal([]byte(s), 100); err == nil {
			t.Errorf("accepted %q", s)
		}
	}

	if _, err := Octal([]byte("abc"), 2); err == nil {
		t.Fatal("overflow accepted")
	}

	if _, err := Octal(nil, -1); err == nil {
		t.Fatal("negative limit")
	}
}

func TestSequenceParsing(t *testing.T) {
	parts, err := SplitSequence(`send-keys "a;b" ; display-message 'c;d'`)
	if err != nil || len(parts) != 2 {
		t.Fatalf("%#v %v", parts, err)
	}

	for _, s := range []string{`{display}`, `x ; ; y`, `x "unterminated`, "x\ny"} {
		if _, err := SplitSequence(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func FuzzRecords(f *testing.F) {
	for _, s := range []string{"", "x\ny", "\x00\xff", "%end 8 10 1\nTGO1:1:0:,\n"} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8192 {
			return
		}

		wire := EncodeRecord([]string{string(data), ""})

		got, err := ParseRecords(wire, 2)
		if err != nil || got[0][0] != string(data) || got[0][1] != "" {
			t.Fatalf("roundtrip: %v", err)
		}

		_, _, _ = ReadRecord(bytes.NewReader(data), int64(len(data)))
	})
}

func FuzzQuoted(f *testing.F) {
	f.Add(`a;$HOME "x"`)
	f.Add("\xff\n")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 4096 || strings.IndexByte(s, 0) >= 0 {
			return
		}

		var b strings.Builder
		if err := Quoted(&b, s); err != nil {
			t.Fatal(err)
		}

		w, err := ParseWords(b.String())
		if err != nil || len(w) != 1 || w[0] != s {
			t.Fatalf("quote round-trip: %v", err)
		}
	})
}

func FuzzOctal(f *testing.F) {
	f.Add([]byte(`\000\377`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 8192 {
			return
		}

		out, _ := Octal(b, 2048)
		if len(out) > 2048 {
			t.Fatal("limit")
		}
	})
}
