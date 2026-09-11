# gotmux

[![Tests](https://img.shields.io/github/actions/workflow/status/zigai/gotmux/test.yml?label=Tests)](https://github.com/zigai/gotmux/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/zigai/gotmux/tmux.svg)](https://pkg.go.dev/github.com/zigai/gotmux/tmux)
[![Go version](https://img.shields.io/github/go-mod/go-version/zigai/gotmux)](https://github.com/zigai/gotmux/blob/master/go.mod)
[![License: MIT](https://img.shields.io/github/license/zigai/gotmux)](https://github.com/zigai/gotmux/blob/master/LICENSE)

`gotmux` is a Go library for controlling, automating, and inspecting
[tmux](https://github.com/tmux/tmux) servers through daemon-bound handles,
bounded subprocesses, and real-time control-mode streams.

## Features

- **Type-safe options**: Strongly typed accessors for server, session, window, and pane configurations, plus custom `@user-options`.
- **Context detection**: Automatically resolves `$TMUX` and `$TMUX_PANE` into verified session and pane handles.
- **Dual transports**: Run concurrency-safe bounded subprocesses or open persistent control connections (`-C`) for real-time event streaming (`%output`, layouts, hooks).
- **Daemon-bound handles**: `Session`, `Window`, and `Pane` handles verify daemon lifetimes to prevent accidental operations on recycled IDs after restarts.
- **Testing fixtures**: Built-in `tmuxtest` package provides isolated, temporary tmux daemons with automatic cleanup for integration testing.

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
