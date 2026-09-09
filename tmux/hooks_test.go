package tmux

import "testing"

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

func TestSequenceCopies(t *testing.T) {
	c, _ := NewCommand("display-message", "one")

	seq, e := Sequence(c)
	if e != nil {
		t.Fatal(e)
	}

	commands := seq.Commands()
	commands[0], _ = NewCommand("kill-server")

	if seq.Commands()[0].Name() != "display-message" {
		t.Fatal("mutable sequence")
	}
}
