package tmux_test

import (
	"context"
	"fmt"

	tmux "github.com/zigai/gotmux/tmux"
)

//nolint:testableexamples // compile-only example requires live tmux server socket; covered by integration_test.go
func Example_inspectCapture() {
	ctx := context.Background()

	server, err := tmux.New(tmux.Config{SocketName: "work"}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}

	panes, err := server.Panes(ctx)
	if err != nil {
		return
	}

	for _, info := range panes {
		data, err := info.Handle().Capture(ctx, tmux.CaptureOptions{EntireHistory: true}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
		if err != nil {
			return
		}

		fmt.Printf("%s: %d bytes\n", info.ID, len(data))
	}
}

func ExampleValue() {
	present := tmux.PresentValue("")
	value, ok := present.Get()
	fmt.Printf("present=%v, empty=%v\n", ok, value == "")

	var absent tmux.Value[string]

	_, ok = absent.Get()
	fmt.Printf("present=%v\n", ok)
	// Output:
	// present=true, empty=true
	// present=false
}

//nolint:testableexamples // compile-only example requires live tmux server socket; covered by integration_test.go
func ExampleSession_NewWindow() {
	ctx := context.Background()

	server, err := tmux.New(tmux.Config{SocketName: "work"}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}

	session, err := server.FindSession(ctx, "work")
	if err != nil {
		return
	}

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "logs", Program: tmux.Exec("tail", "-f", "/var/log/app.log")}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		return
	}
	// Capture observes what is available now; it does not await program completion.
	_, _ = pane.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
}

//nolint:testableexamples // compile-only example requires live tmux server socket; covered by integration_test.go
func ExamplePaneOptions_SetUser() {
	ctx := context.Background()

	server, err := tmux.New(tmux.Config{SocketName: "work"}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}

	pane, err := server.Pane(ctx, "%1")
	if err != nil {
		return
	}

	if err = pane.Options().SetUser(ctx, "@annotation", ""); err != nil {
		return
	}

	value, err := pane.Options().User(ctx, "@annotation")
	if err != nil {
		return
	}

	text, present := value.Effective.Get()
	fmt.Printf("present=%v bytes=%d\n", present, len(text))
}

//nolint:testableexamples // compile-only example requires live tmux server socket; covered by integration_test.go
func ExampleConnection_Events() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := tmux.New(tmux.Config{SocketName: "work"}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}

	session, err := server.FindSession(ctx, "work")
	if err != nil {
		return
	}

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{PaneOutput: true}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	events, err := conn.Events(ctx, tmux.EventOptions{}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}
	defer func() { _ = events.Close() }()

	event, err := events.Next(ctx)
	if err != nil {
		return
	}

	if output, ok := event.(tmux.PaneOutputEvent); ok {
		fmt.Printf("%s: %d terminal bytes\n", output.PaneID, len(output.Data()))
	}
}

//nolint:testableexamples // compile-only example requires live tmux server socket; covered by integration_test.go
func ExampleServer_Run() {
	ctx := context.Background()

	server, err := tmux.New(tmux.Config{SocketName: "anno-fork"}) //nolint:exhaustruct_v5 // example demonstrates minimal caller options
	if err != nil {
		return
	}
	// An example extension command, not a claimed stock tmux feature.
	command, err := tmux.NewCommand("anno-provider", "--json")
	if err != nil {
		return
	}

	result, err := server.Run(ctx, command)
	_ = result // The application's adapter owns any provider-specific decoding.
	_ = err
}
