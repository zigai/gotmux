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
