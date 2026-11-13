# <a href="https://github.com/zigai/gotmux"><img src="assets/logo.svg" align="right" width="130" alt="gotmux gopher mascot"></a> gotmux

gotmux is a Go library for controlling, inspecting, and automating [tmux](https://github.com/tmux/tmux) sessions, windows, panes, and buffers.

[![tests](https://img.shields.io/github/actions/workflow/status/zigai/gotmux/test.yml?branch=master&label=tests&logo=githubactions&logoColor=white)](https://github.com/zigai/gotmux/actions/workflows/test.yml)
[![release](https://img.shields.io/github/v/release/zigai/gotmux?label=release&color=blue&logo=github)](https://github.com/zigai/gotmux/releases/latest)
[![reference](https://img.shields.io/badge/reference-pkg.go.dev-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/zigai/gotmux/tmux)
[![go](https://img.shields.io/github/go-mod/go-version/zigai/gotmux?label=go&logo=go&logoColor=white)](go.mod)
[![license](https://img.shields.io/github/license/zigai/gotmux?label=license&logo=opensourceinitiative&logoColor=white)](LICENSE)

## Features

- **Deep tmux coverage:** Controls everything from sessions and panes to paste buffers and history.
- **Control mode streaming:** Connects via control mode (`-C`/`-CC`) for push-based automation without polling.
- **Typed options:** Reads and sets over 100 tmux options as native Go types.
- **Environment discovery:** Detects the current session, window, and pane from `$TMUX` and `$TMUX_PANE`.
- **Testing fixtures:** Launches isolated tmux instances via `tmuxtest` for integration tests.

## Installation

```sh
go get github.com/zigai/gotmux
```

Requires Go 1.26+ and tmux 3.6+.

## Examples

### Session, window, and pane automation

```go
ctx := context.Background()

server, err := tmux.New(tmux.Config{SocketName: "dev"})
if err != nil {
	log.Fatal(err)
}

session, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "dev"})
if err != nil {
	log.Fatal(err)
}

if err := session.Options().SetMouse(ctx, true); err != nil {
	log.Fatal(err)
}

link, err := session.NewWindow(ctx, tmux.NewWindowOptions{
	Name:    "editor",
	Program: tmux.Exec("vim", "main.go"),
})
if err != nil {
	log.Fatal(err)
}

activePane, err := link.Window().ActivePane(ctx)
if err != nil {
	log.Fatal(err)
}

_, err = activePane.Split(ctx, tmux.SplitOptions{
	Direction: tmux.Horizontal,
	Size:      tmux.SplitSize{Percent: 30},
})
if err != nil {
	log.Fatal(err)
}
```

### Real-time control mode streaming

```go
ctx := context.Background()

server, err := tmux.New(tmux.Config{SocketName: "dev"})
if err != nil {
	log.Fatal(err)
}

session, err := server.FindSession(ctx, "dev")
if err != nil {
	session, err = server.NewSession(ctx, tmux.NewSessionOptions{Name: "dev"})
	if err != nil {
		log.Fatal(err)
	}
}

conn, err := server.OpenControl(ctx, session.ID(), tmux.ControlOptions{PaneOutput: false})
if err != nil {
	log.Fatal(err)
}
defer conn.Close()

stream, err := conn.Events(ctx, tmux.EventOptions{})
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

for {
	event, err := stream.Next(ctx)
	if err != nil {
		break
	}
	fmt.Printf("Notification: %s\n", event.RawName())
}
```

### Finding the current pane

```go
ctx := context.Background()

current, err := tmux.Current(ctx)
if err != nil {
	if errors.Is(err, tmux.ErrNotInsideTmux) {
		fmt.Println("Process is not running inside tmux")
	}
	return
}

fmt.Printf("Current pane %s in window %s\n", current.Pane.ID, current.Window.Name)
```

### Testing fixtures (tmuxtest)

```go
package myapp_test

import (
	"testing"

	"github.com/zigai/gotmux/tmuxtest"
)

func TestMyTmuxIntegration(t *testing.T) {
	// Starts an isolated tmux daemon and registers cleanup via t.Cleanup.
	server := tmuxtest.NewServer(t)

	sessions, err := server.Sessions(t.Context())
	if err != nil {
		t.Fatalf("listing sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one fixture session")
	}
}
```

## License

[MIT](LICENSE)
