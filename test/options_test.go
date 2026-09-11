//go:build integration

package tmux_test

import (
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationTypedOptionInteger(t *testing.T) {
	server, _, ctx := apiFixture(t)
	for _, value := range []int{0, 2147483647} {
		if err := server.Options().SetEscapeTime(ctx, value); err != nil {
			t.Fatal(err)
		}

		got, err := server.Options().EscapeTime(ctx)
		if actual, ok := got.Effective.Get(); err != nil || !ok || actual != value {
			t.Fatalf("integer: %+v, %v", got, err)
		}
	}
}

func TestIntegrationTypedOptionBoolean(t *testing.T) {
	_, session, ctx := apiFixture(t)
	for _, value := range []bool{true, false} {
		if err := session.Options().SetMouse(ctx, value); err != nil {
			t.Fatal(err)
		}

		got, err := session.Options().Mouse(ctx)
		if actual, ok := got.Local.Get(); err != nil || !ok || actual != value {
			t.Fatalf("boolean: %+v, %v", got, err)
		}
	}
}

func TestIntegrationTypedOptionEnum(t *testing.T) {
	server, _, ctx := apiFixture(t)
	for _, value := range []tmux.ClipboardMode{tmux.ClipboardOff, tmux.ClipboardExternal, tmux.ClipboardOn} {
		if err := server.Options().SetClipboard(ctx, value); err != nil {
			t.Fatal(err)
		}

		got, err := server.Options().Clipboard(ctx)
		if actual, ok := got.Effective.Get(); err != nil || !ok || actual != value {
			t.Fatalf("enum: %+v, %v", got, err)
		}
	}
}

func TestIntegrationTypedOptionKey(t *testing.T) {
	_, session, ctx := apiFixture(t)
	if err := session.Options().SetPrefix(ctx, tmux.Key("C-a")); err != nil {
		t.Fatal(err)
	}

	key, err := session.Options().Prefix(ctx)
	if actual, ok := key.Local.Get(); err != nil || !ok || actual != tmux.Key("C-a") {
		t.Fatalf("key: %+v, %v", key, err)
	}

	if err := session.Options().UnsetPrefix(ctx); err != nil {
		t.Fatal(err)
	}

	key, err = session.Options().Prefix(ctx)
	if err != nil || key.Local.State() != tmux.Unavailable {
		t.Fatalf("unset key: %+v, %v", key, err)
	}
}
