//go:build integration

package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationLiteral(t *testing.T) {
	_, session, ctx := apiFixture(t)
	dir := t.TempDir()
	script := `dir=$1
: > "$dir/ready"
for record in 1 2 3 4 5 6 7 8 9; do
 IFS= read -r value || exit 1
 printf '%s\000' "$value" >> "$dir/pending"
done
mv "$dir/pending" "$dir/result"
printf '\nTGO-CAPTURE-DONE\n'
IFS= read -r hold`

	var options tmux.NewWindowOptions

	options.Program = tmux.Exec("/bin/sh", "-c", script, "receiver", dir)

	link, err := session.NewWindow(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sentinelDir := t.TempDir()
	options.Program = tmux.Exec("/bin/sh", "-c", script, "receiver", sentinelDir)

	sentinel, err := session.NewWindow(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{dir, sentinelDir} {
		awaitObservation(t, ctx, "receiver readiness", func() bool { _, err := os.Stat(filepath.Join(path, "ready")); return err == nil })
	}

	payload := " space ' \" ; $(touch " + filepath.Join(dir, "executed") + ") #{pane_id} \\ ž "
	sendLiteralRecords(t, ctx, pane, payload)

	result := filepath.Join(dir, "result")

	awaitObservation(t, ctx, "literal receiver completion", func() bool { _, err := os.Stat(result); return err == nil })

	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{payload, "barrier", "", "barrier", "Enter", "barrier", payload, "", "final"}, "\x00") + "\x00"
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("pane %s received %q, want %q", pane.ID(), got, want)
	}

	for _, path := range []string{filepath.Join(dir, "executed"), filepath.Join(sentinelDir, "pending")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("forbidden effect at %s: %v", path, err)
		}
	}

	if _, err := sentinel.Info(ctx); err != nil {
		t.Fatalf("sentinel window changed: %v", err)
	}

	var capture tmux.CaptureOptions

	awaitObservation(t, ctx, "acknowledged capture marker", func() bool {
		data, err := pane.Capture(ctx, capture)
		if err != nil {
			t.Fatal(err)
		}

		return bytes.Contains(data, []byte("TGO-CAPTURE-DONE"))
	})
}

func sendLiteralRecords(t *testing.T, ctx context.Context, pane tmux.Pane, payload string) {
	t.Helper()

	for _, value := range []string{payload, "", "Enter"} {
		if err := pane.SendText(ctx, value); err != nil {
			t.Fatal(err)
		}

		if err := pane.SendKeys(ctx, tmux.KeyEnter); err != nil {
			t.Fatal(err)
		}

		if err := pane.Submit(ctx, "barrier"); err != nil {
			t.Fatal(err)
		}
	}

	if err := pane.Submit(ctx, payload); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, ""); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "final"); err != nil {
		t.Fatal(err)
	}
}
