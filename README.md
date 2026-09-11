# gotmux

[![Tests](https://img.shields.io/github/actions/workflow/status/zigai/gotmux/test.yml?label=Tests)](https://github.com/zigai/gotmux/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/zigai/gotmux/tmux.svg)](https://pkg.go.dev/github.com/zigai/gotmux/tmux)
[![Go version](https://img.shields.io/github/go-mod/go-version/zigai/gotmux)](https://github.com/zigai/gotmux/blob/master/go.mod)
[![License: MIT](https://img.shields.io/github/license/zigai/gotmux)](https://github.com/zigai/gotmux/blob/master/LICENSE)

`gotmux` lets you automate, control, and inspect [tmux](https://github.com/tmux/tmux) from Go.

## Features

- **Typed options:** Get and set tmux and `@user` options with native Go types.
- **Current session detection:** Auto-discovers active sessions and panes from the environment.
- **Live streaming:** Stream terminal output and window events in real time using tmux control mode (`-C`).
- **Safe handles:** Protects against sending commands to the wrong session or pane if tmux restarts.
- **Testing support:** Built-in `tmuxtest` helper launches temporary, isolated tmux instances for tests.

## Installation

```sh
go get github.com/zigai/gotmux
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/zigai/gotmux/tmux"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server, err := tmux.New(tmux.Config{SocketName: "work"})
	if err != nil {
		log.Fatal(err)
	}

	session, err := server.FindSession(ctx, "work")
	if err != nil {
		session, err = server.NewSession(ctx, tmux.NewSessionOptions{
			Name: "work",
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name: "build",
	})
	if err != nil {
		log.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if err := pane.Submit(ctx, "echo 'Hello from gotmux'"); err != nil {
		log.Fatal(err)
	}

	output, err := pane.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s", output)
}
```

## License

[MIT](https://github.com/zigai/gotmux/blob/master/LICENSE)
