#!/bin/sh
# Build and execute an external consumer through an isolated local module proxy.
# Verifies that downstream projects can import and consume the module with GOWORK=off
# and without replace directives.
set -eu

# --- Phase 1: Environment & Scratch Directory Setup ---

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)

trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM

cd "$root"

module=$(go list -m)
go_version=$(awk '$1 == "go" { print $2 }' go.mod)
escaped=$(printf '%s' "$module" | awk '{ for (i=1; i<=length($0); i++) { c=substr($0,i,1); if (c ~ /[A-Z]/) printf "!%s",tolower(c); else printf "%s",c } }')

version="v0.0.0"
proxy="$work/proxy/$escaped/@v"
package="$work/source/$module@$version"

# --- Phase 2: Synthesize Local Module Proxy Release ---

mkdir -p "$proxy" "$package" "$work/consumer"

cp go.mod "$proxy/$version.mod"
printf '{"Version":"%s","Time":"2026-09-08T00:00:00Z"}\n' "$version" > "$proxy/$version.info"
printf '%s\n' "$version" > "$proxy/list"

cp -R go.mod go.sum tmux internal tmuxtest "$package/"
(cd "$work/source" && zip -qr "$proxy/$version.zip" "$module@$version")

# --- Phase 3: Construct Isolated Consumer Project ---

cd "$work/consumer"

printf 'module consumer.test/check\n\ngo %s\n\nrequire %s %s\n' "$go_version" "$module" "$version" > go.mod

cat > main.go <<EOF_GO
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	t "$module/tmux"
)

func main() {
	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary, _ = exec.LookPath("tmux")
	}
	if binary == "" {
		binary, _ = os.Executable()
	}

	server, err := t.New(t.Config{Binary: binary, SocketName: "consumer-check"})
	if err != nil {
		panic(err)
	}

	if !server.Support().RawCommands {
		panic("subprocess support missing")
	}

	var pane t.Pane
	if err := pane.Kill(context.Background()); !errors.Is(err, t.ErrInvalidHandle) {
		panic(fmt.Sprintf("invalid handle error: %v", err))
	}

	command, err := t.NewCommand("display-message", "literal ; #{pane_id}")
	if err != nil {
		panic(err)
	}

	sequence, err := t.Sequence(command)
	if err != nil {
		panic(err)
	}

	arguments := sequence.Commands()[0].Args()
	arguments[0] = "changed"
	if sequence.Commands()[0].Args()[0] != "literal ; #{pane_id}" {
		panic("consumer mutated owned command arguments")
	}

	value := t.PresentValue("")
	if text, ok := value.Get(); !ok || text != "" {
		panic("empty value lost presence")
	}

	fmt.Println("External consumer runtime contracts passed.")
}
EOF_GO

# --- Phase 4: Resolution, Build & Execution ---

export GOWORK=off
export GOPROXY="file://$work/proxy,https://proxy.golang.org"
export GONOSUMDB="$module"
export GOMODCACHE="$work/cache"

# Keep the isolated module cache removable by the cleanup trap.
export GOFLAGS="${GOFLAGS:-} -modcacherw"

go mod tidy
go build -o "$work/consumer-check" .
"$work/consumer-check"

printf '%s\n' 'Clean external consumer build and execution passed with GOWORK=off and no replace directives.'
