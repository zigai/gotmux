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
		"Enter":   true,
		"enter":   true,
		"ENTER":   true,
		"Escape":  true,
		"escape":  true,
		"Space":   true,
		"space":   true,
		"Tab":     true,
		"tab":     true,
		"PgUp":    true,
		"pgup":    true,
		"PgDn":    true,
		"pgdn":    true,
		"KP0":     true,
		"kp0":     true,
		"KPEnter": true,
		"C-a":     true,
		"c-a":     true,
		"M-x":     true,
		"m-x":     true,
		"F1":      true,
		"f1":      true,
		"F12":     true,
		"f12":     true,
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
