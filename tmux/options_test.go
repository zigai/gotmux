package tmux

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestArrayUpdateOutcome(t *testing.T) {
	for _, effect := range []Effect{NotSent, Unknown, Confirmed} {
		t.Run(effect.String(), func(t *testing.T) {
			cause := &OperationError{Operation: "set-option", Outcome: Outcome{Effect: effect, Steps: nil, Created: nil}, Err: ErrProtocol}
			steps := []StepOutcome{{Index: 0, Effect: Confirmed}, {Index: 1, Effect: NotSent}, {Index: 2, Effect: NotSent}}

			err := arrayUpdateError(cause, 1, steps)
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("lost cause: %v", err)
			}

			got, ok := errors.AsType[*OperationError](err)
			if !ok {
				t.Fatalf("expected OperationError: %v", err)
			}

			want := []StepOutcome{{Index: 0, Effect: Confirmed}, {Index: 1, Effect: effect}, {Index: 2, Effect: NotSent}}
			if diff := cmp.Diff(want, got.Outcome.Steps); diff != "" {
				t.Fatal(diff)
			}

			aggregate := effect
			if effect == NotSent {
				aggregate = Unknown
			}

			if got.Outcome.Effect != aggregate {
				t.Fatalf("aggregate effect = %v, want %v", got.Outcome.Effect, aggregate)
			}
		})
	}
}

func TestTypedOptionValidation(t *testing.T) {
	server := localServer(t)
	ctx := t.Context()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "negative escape time", call: func() error { return server.Options().SetEscapeTime(ctx, -1) }},
		{name: "buffer lower bound", call: func() error { return server.Options().SetBufferLimit(ctx, 0) }},
		{name: "invalid enum", call: func() error { return server.Options().SetClipboard(ctx, ClipboardMode("invalid")) }},
		{name: "invalid key", call: func() error { return server.GlobalSessionOptions().SetPrefix(ctx, Key("not-a-key")) }},
		{name: "NUL string", call: func() error { return server.GlobalSessionOptions().SetStatusLeft(ctx, "\x00") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("invalid value reached transport: %v", err)
			}
		})
	}
}

func TestParseOptionName(t *testing.T) {
	tests := []struct {
		input       string
		wantBase    string
		wantIndex   int
		wantIndexed bool
		wantErr     bool
	}{
		{input: "escape-time", wantBase: "escape-time", wantIndex: 0, wantIndexed: false, wantErr: false},
		{input: "status-format[0]", wantBase: "status-format", wantIndex: 0, wantIndexed: true, wantErr: false},
		{input: "codepoint-widths[500]", wantBase: "codepoint-widths", wantIndex: 500, wantIndexed: true, wantErr: false},
		{input: "@my_opt[42]", wantBase: "@my_opt", wantIndex: 42, wantIndexed: true, wantErr: false},
		{input: "@user", wantBase: "@user", wantIndex: 0, wantIndexed: false, wantErr: false},
		{input: "", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "@", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "[0]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[-1]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[--1]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[abc]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[0x10]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[ 1 ]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
		{input: "opt[1073741824]", wantBase: "opt", wantIndex: 1073741824, wantIndexed: true, wantErr: false}, // 1<<30 max bound
		{input: "opt[1073741825]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},             // > 1<<30
		{input: "opt[99999999999999999999999]", wantBase: "", wantIndex: 0, wantIndexed: false, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			base, idx, indexed, err := parseOptionName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseOptionName(%q) err = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}

			if !tt.wantErr {
				if base != tt.wantBase || idx != tt.wantIndex || indexed != tt.wantIndexed {
					t.Fatalf("parseOptionName(%q) = (%q, %d, %v), want (%q, %d, %v)",
						tt.input, base, idx, indexed, tt.wantBase, tt.wantIndex, tt.wantIndexed)
				}
			}
		})
	}
}

func TestOptionMutationOptionsValidation(t *testing.T) {
	server := localServer(t)
	ctx := t.Context()

	// Invalid option name
	if err := server.Options().SetWith(ctx, "invalid name with spaces", SetOptionOptions{
		Value:        PresentValue("1"),
		Append:       false,
		ExpandFormat: false,
		OnlyIfUnset:  false,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid option name, got %v", err)
	}

	// Invalid option value (NUL byte)
	if err := server.Options().SetWith(ctx, "escape-time", SetOptionOptions{
		Value:        PresentValue("1\x002"),
		Append:       false,
		ExpandFormat: false,
		OnlyIfUnset:  false,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for NUL byte in value, got %v", err)
	}

	// Invalid unset option name
	if err := server.Options().UnsetWith(ctx, "invalid name", UnsetOptionOptions{Cascade: true}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid option name in UnsetWith, got %v", err)
	}
}
