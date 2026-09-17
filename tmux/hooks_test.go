package tmux

import (
	"strings"
	"testing"
)

func TestHookAndBindingPayloadsNeverEvaluate(t *testing.T) {
	p := parsePayload(`send-keys "literal;#{pane_id}" ; display-message "done"`)

	commands, ok := p.Commands()
	if !ok || len(commands) != 2 || commands[0].Args()[0] != "literal;#{pane_id}" {
		t.Fatal(commands, ok)
	}

	p = parsePayload(`if-shell 1 { display-message nested }`)
	if _, ok = p.Commands(); ok || p.Raw() == "" {
		t.Fatal("unknown syntax not preserved")
	}

	b := parseBinding(`bind-key -r -T prefix C-a send-keys "literal;" ; display-message done`, PrefixTable)
	if !b.Parsed || !b.Repeat || b.Key != "C-a" {
		t.Fatal(b)
	}

	commands, ok = b.Payload.Commands()
	if !ok || len(commands) != 2 {
		t.Fatal(commands, ok)
	}
}

func TestKeyValidCaseAndAliases(t *testing.T) {
	keys := map[string]bool{
		"Enter":                 true,
		"enter":                 true,
		"ENTER":                 true,
		"Escape":                true,
		"escape":                true,
		"Space":                 true,
		"space":                 true,
		"Tab":                   true,
		"tab":                   true,
		"PgUp":                  true,
		"pgup":                  true,
		"PgDn":                  true,
		"pgdn":                  true,
		"KP0":                   true,
		"kp0":                   true,
		"KPEnter":               true,
		"C-a":                   true,
		"c-a":                   true,
		"M-x":                   true,
		"m-x":                   true,
		"F1":                    true,
		"f1":                    true,
		"F12":                   true,
		"f12":                   true,
		"F63":                   true,
		"Any":                   true,
		"any":                   true,
		"None":                  true,
		"none":                  true,
		"User0":                 true,
		"user0":                 true,
		"User9":                 true,
		"User63":                true,
		"MouseDown1Pane":        true,
		"mousedown1pane":        true,
		"MouseUp1Pane":          true,
		"MouseDrag1Pane":        true,
		"MouseDragEnd1Pane":     true,
		"WheelUpPane":           true,
		"WheelDownPane":         true,
		"DoubleClick1Pane":      true,
		"TripleClick1Pane":      true,
		"SecondClick1Pane":      true,
		"MouseDown1Status":      true,
		"MouseDown1StatusLeft":  true,
		"MouseDown1StatusRight": true,
		"MouseDown1Border":      true,
		"MouseDown1Empty":       true,
		"MouseDown1ScrollbarUp": true,
		"MouseDown1Control7":    true,
		"M-MouseDown1Pane":      true,
		"C-MouseDown1Status":    true,
		"S-WheelUpPane":         true,
		"C-M-x":                 true,
		"^a":                    true,
		"":                      false,
		"C-":                    false,
		"M-":                    false,
		"User":                  false,
		"User-1":                false,
		"MouseDown1":            false,
		"WheelUp":               false,
		"InvalidKey":            false,
		"a\x00b":                false,
		"\x00":                  false,
		"\x01":                  false,
		"\x7f":                  false,
	}
	for k, want := range keys {
		got := Key(k).Valid()
		if got != want {
			t.Errorf("Key(%q).Valid() = %v, want %v", k, got, want)
		}
	}
}

func FuzzParseBinding(f *testing.F) {
	seeds := []string{
		`bind-key -r -T prefix C-a send-keys "literal;" ; display-message done`,
		`bind-key -r -T prefix C-o rotate-window`,
		`bind-key -T root F12 set-option -t $0 @e1 1 \; set-option -t $0 @e2 2`,
		`bind-key -T copy-mode-vi 'y' send-keys -X copy-selection-and-cancel`,
		`bind-key -N "custom note" -T prefix x run-shell "echo 'hello; world'"`,
		`bind-key -- key cmd`,
		`bind-key -T root Enter send-keys Enter`,
		`bind-key`,
		`bind-key -T`,
		`bind-key -N`,
		``,
		`bind-key -T prefix "M-x" split-window -h`,
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 2048 || strings.IndexByte(raw, 0) >= 0 {
			return
		}

		b := parseBinding(raw, PrefixTable)
		if b.Parsed {
			assertValidParsedBinding(t, b)
		}
	})
}

func FuzzParseBindingNote(f *testing.F) {
	seeds := []string{
		"C-b Tab     Switch to a window",
		"C-b Space   Select next layout",
		"C-b !       Break pane to a new window",
		"C-b F12 Note with multiple words",
		"F12 Note",
		"MYPREFIX Tab Some Note",
		"",
		"NoSpaces",
		"Single Space",
		"   Leading and trailing   ",
		"Multiple    Spaces    Everywhere",
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 2048 {
			return
		}

		note := parseBindingNote(raw, BindingsOptions{
			Table:      "",
			Key:        "",
			FirstMatch: false,
			NotesOnly:  true,
			Prefix:     "",
		})

		if note.Raw != raw {
			t.Fatalf("parseBindingNote corrupted Raw: got %q, want %q", note.Raw, raw)
		}
	})
}

func assertValidParsedBinding(t *testing.T, b BindingInfo) {
	t.Helper()

	if !b.Key.Valid() || !b.Table.Valid() {
		t.Fatalf("parseBinding marked invalid key (%q) or table (%q) as parsed: %+v", b.Key, b.Table, b)
	}

	cmds, ok := b.Payload.Commands()
	if !ok || len(cmds) == 0 {
		t.Fatalf("parseBinding marked parsed but has empty/missing commands: %+v", b)
	}

	for i, c := range cmds {
		if !c.Valid() {
			t.Fatalf("parseBinding command %d is invalid: %+v", i, c)
		}
	}
}
