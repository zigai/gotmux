package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	ClientIgnoreSize ClientFlag = "ignore-size"
	ClientNoOutput   ClientFlag = "no-output"
	ClientReadOnly   ClientFlag = "read-only"
	ClientActivePane ClientFlag = "active-pane"

	PopupBorderSingle  PopupBorder = "single"
	PopupBorderDouble  PopupBorder = "double"
	PopupBorderHeavy   PopupBorder = "heavy"
	PopupBorderRounded PopupBorder = "rounded"
	PopupBorderSimple  PopupBorder = "simple"
	PopupBorderPadded  PopupBorder = "padded"
	PopupBorderNone    PopupBorder = "none"
)

type (
	// ClientFlag specifies an operational flag for an attached client terminal (-f flag to refresh-client).
	ClientFlag string

	// SwitchOptions configures switching an attached client to a different session.
	SwitchOptions struct {
		// ReadOnly attaches the client in read-only mode (-r flag).
		ReadOnly bool

		// PreserveEnvironment prevents updating the client's environment variables (-E flag).
		PreserveEnvironment bool
	}

	// RefreshOptions configures refreshing an attached client's terminal screen or status line.
	RefreshOptions struct {
		// StatusOnly refreshes only the status line without repainting pane contents (-S flag).
		StatusOnly bool

		// Size optionally overrides the client's terminal dimensions (-C flag).
		Size Size

		// Flags sets client status flags (-f flag).
		Flags []ClientFlag
	}

	// PopupBorder specifies the border style for a popup overlay (-b flag).
	PopupBorder string

	// PopupOptions configures displaying an interactive modal popup overlay on a client or pane.
	PopupOptions struct {
		// Program specifies the command to execute inside the popup.
		Program Program

		// Dir specifies the working directory for the popup process.
		Dir string

		// Env specifies environment overrides for the popup process.
		Env map[string]string

		// Size specifies the popup dimensions in character cells.
		Size Size

		// Width specifies the popup width (e.g. "80", "75%"). Wins over Size.Width if non-empty.
		Width string

		// Height specifies the popup height (e.g. "24", "80%"). Wins over Size.Height if non-empty.
		Height string

		// X specifies the popup horizontal position (e.g. "C", "R", "M", "W", "50%").
		X string

		// Y specifies the popup vertical position (e.g. "C", "M", "W", "50%").
		Y string

		// Border specifies the popup border style (-b flag).
		Border PopupBorder

		// Style specifies popup text/background style (-s flag).
		Style string

		// BorderStyle specifies popup border style (-S flag).
		BorderStyle string

		// Title is an optional title string shown in the popup border (-T flag).
		Title string

		// CloseOnExit closes the popup when the process exits (-E flag).
		CloseOnExit bool

		// CloseOnSuccess closes the popup only if the process exits with status 0 (-EE flag).
		CloseOnSuccess bool

		// Borderless draws the popup without a surrounding border (-B flag).
		Borderless bool
	}

	// MenuItem defines a single entry in an interactive popup menu.
	MenuItem struct {
		Label string

		// Key is the shortcut key that selects this item.
		Key Key

		// Commands is the sequence of commands executed when this item is selected.
		Commands CommandSequence

		// Command is an optional raw command string executed when selected, used if Commands is empty.
		Command string
		// Separator indicates this item is a visual separator line rather than a selectable option.
		Separator bool

		// Disabled indicates the item is visible but greyed out and unselectable.
		Disabled bool
	}

	// MenuOptions configures displaying an interactive popup menu on a client.
	MenuOptions struct {
		Title string

		// Mouse opens the menu at the current mouse cursor position (-m flag).
		Mouse bool

		// StayOpen keeps the menu open after an item is selected. (Unsupported in stock tmux).
		StayOpen bool

		// X specifies the menu horizontal position (-x flag).
		X string

		// Y specifies the menu vertical position (-y flag).
		Y string
	}
	// PromptTemplate specifies the template command executed by tmux when a prompt is submitted.
	// In tmux, "%%" in the template is replaced by the user's entered text.
	PromptTemplate string

	// PromptOptions configures an interactive user prompt on a client's status line.
	PromptOptions struct {
		// Label is the prompt string displayed before the input field (e.g. "Rename session: ").
		Label string

		// Initial is the pre-filled default input text (-I flag).
		Initial string

		// SingleCharacter returns immediately after a single keypress without waiting for Enter (-1 flag).
		SingleCharacter bool

		// Numeric restricts user input to numeric digits (-N flag).
		Numeric bool
	}
)

// Valid reports whether this client flag is one of the recognized flags.
func (f ClientFlag) valid() bool {
	switch f {
	case ClientIgnoreSize, ClientNoOutput, ClientReadOnly, ClientActivePane:
		return true
	}

	return false
}

// Switch switches this client terminal to display session s.
func (c Client) Switch(ctx context.Context, s Session, o SwitchOptions) error {
	if err := sameHandles(c.h, s.h); err != nil {
		return opError("SwitchClient", err)
	}

	args := []string{"-c", c.h.id, "-t", s.h.id}
	if o.ReadOnly {
		args = append(args, "-r")
	}

	if o.PreserveEnvironment {
		args = append(args, "-E")
	}

	return c.h.act(ctx, "switch-client", args...)
}

// Detach detaches this client terminal from the tmux server (detach-client).
func (c Client) Detach(ctx context.Context) error { return c.h.act(ctx, "detach-client", "-t", c.h.id) }

// Refresh sends a redraw request to the client terminal, updating its status line or dimensions.
func (c Client) Refresh(ctx context.Context, o RefreshOptions) error {
	if !o.Size.valid() {
		return opError("RefreshClient", invalid("size"))
	}

	args := []string{"-t", c.h.id}
	if o.StatusOnly {
		args = append(args, "-S")
	}

	if o.Size.Width != 0 || o.Size.Height != 0 {
		if o.Size.Width == 0 || o.Size.Height == 0 {
			return opError("RefreshClient", invalid("complete size required"))
		}

		args = append(args, "-C", strconv.Itoa(o.Size.Width)+","+strconv.Itoa(o.Size.Height))
	}

	flags := []string{}

	for _, f := range o.Flags {
		if !f.valid() {
			return opError("RefreshClient", invalid("client flag"))
		}

		flags = append(flags, string(f))
	}

	if len(flags) > 0 {
		args = append(args, "-f", strings.Join(flags, ","))
	}

	return c.h.act(ctx, "refresh-client", args...)
}

// Message requests a status message on this exact client. Completion means tmux
// accepted the display request, not that the user read or dismissed it.
func (c Client) Message(ctx context.Context, text string) error {
	if !wire.ValidString(text) {
		return opError("Message", invalid("message"))
	}

	if c.h.server != nil && c.h.server.conn != nil {
		return opError("Message", ErrTransportUnsupported)
	}

	return c.h.act(ctx, "display-message", "-c", c.h.id, "--", wire.LiteralFormat(text))
}

// Message requests a status message on the client currently viewing this pane (display-message -t).
// Completion means tmux accepted the display request, not that the user read or dismissed it.
func (p Pane) Message(ctx context.Context, text string) error {
	if !wire.ValidString(text) {
		return opError("Message", invalid("message"))
	}

	if p.h.server != nil && p.h.server.conn != nil {
		return opError("Message", ErrTransportUnsupported)
	}

	if err := p.h.check(); err != nil {
		return opError("Message", err)
	}

	return p.h.act(ctx, "display-message", "-t", p.h.id, "--", wire.LiteralFormat(text))
}

// Message requests a status message on the active client attached to this server (display-message).
// Completion means tmux accepted the display request, not that the user read or dismissed it.
func (s *Server) Message(ctx context.Context, text string) error {
	if s == nil {
		return opError("Message", ErrInvalidHandle)
	}

	if !wire.ValidString(text) {
		return opError("Message", invalid("message"))
	}

	if s.conn != nil {
		return opError("Message", ErrTransportUnsupported)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError("Message", err)
	}
	defer op.close()

	_, err = s.execute(opCtx, op, emptyPlan(command("display-message", "--", wire.LiteralFormat(text))), nil, nil)

	return opError("Message", err)
}

// Popup waits for tmux's popup command queue to resume (normally dismissal).
// A deadline is required. Cancellation ends the local waiter, not necessarily
// the server-side popup. No exit status or user choice is inferred.
func (c Client) Popup(ctx context.Context, o PopupOptions) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Popup", err)
	}

	if err := c.h.check(); err != nil {
		return opError("Popup", err)
	}

	if c.h.server.conn != nil {
		return opError("Popup", ErrTransportUnsupported)
	}

	args, err := popupArgs(c.h.id, o)
	if err != nil {
		return opError("Popup", err)
	}

	return c.h.act(ctx, "display-popup", args...)
}

// Popup displays an interactive modal popup overlay targeting this pane (-t flag).
func (p Pane) Popup(ctx context.Context, o PopupOptions) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Popup", err)
	}

	if err := p.h.check(); err != nil {
		return opError("Popup", err)
	}

	if p.h.server.conn != nil {
		return opError("Popup", ErrTransportUnsupported)
	}

	args, err := popupArgsWithTarget("-t", p.h.id, o)
	if err != nil {
		return opError("Popup", err)
	}

	return p.h.act(ctx, "display-popup", args...)
}

// Menu displays an interactive popup menu on this client and blocks until dismissal.
//
// A caller deadline is required. Cancellation ends the local waiter but does not guarantee
// immediate dismissal of the menu on the client.
// Fails with [ErrTransportUnsupported] over control mode.
func (c Client) Menu(ctx context.Context, items []MenuItem, o MenuOptions) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Menu", err)
	}

	if err := c.h.check(); err != nil {
		return opError("Menu", err)
	}

	if c.h.server.conn != nil {
		return opError("Menu", ErrTransportUnsupported)
	}

	if o.StayOpen {
		return opError("Menu", unsupported("persistent menu completion policy"))
	}

	args, err := menuArgs(c.h.id, o)
	if err != nil {
		return opError("Menu", err)
	}

	node := leaf(command("display-menu", args...))

	for _, item := range items {
		itemArgs, itemErr := menuItemArg(item)
		if itemErr != nil {
			return opError("Menu", itemErr)
		}

		node.args = append(node.args, itemArgs...)
	}

	opCtx, op, err := c.h.server.begin(ctx)
	if err != nil {
		return opError("Menu", err)
	}
	defer op.close()

	_, err = c.h.server.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, c.h.guard(), nil)

	return opError("Menu", err)
}

// Menu displays an interactive popup menu targeting this pane (-t flag).
func (p Pane) Menu(ctx context.Context, items []MenuItem, o MenuOptions) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Menu", err)
	}

	if err := p.h.check(); err != nil {
		return opError("Menu", err)
	}

	if p.h.server.conn != nil {
		return opError("Menu", ErrTransportUnsupported)
	}

	if o.StayOpen {
		return opError("Menu", unsupported("persistent menu completion policy"))
	}

	args, err := menuArgsWithTarget("-t", p.h.id, o)
	if err != nil {
		return opError("Menu", err)
	}

	node := leaf(command("display-menu", args...))

	for _, item := range items {
		itemArgs, itemErr := menuItemArg(item)
		if itemErr != nil {
			return opError("Menu", itemErr)
		}

		node.args = append(node.args, itemArgs...)
	}

	opCtx, op, err := p.h.server.begin(ctx)
	if err != nil {
		return opError("Menu", err)
	}
	defer op.close()

	_, err = p.h.server.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, p.h.guard(), nil)

	return opError("Menu", err)
}

// Prompt displays an interactive command prompt in this client's status line.
//
// A caller deadline is required. Fails with [ErrTransportUnsupported] over control mode.
func (c Client) Prompt(ctx context.Context, template PromptTemplate, o PromptOptions) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Prompt", err)
	}

	if err := c.h.check(); err != nil {
		return opError("Prompt", err)
	}

	if c.h.server.conn != nil {
		return opError("Prompt", ErrTransportUnsupported)
	}

	args, err := promptArgs(c.h.id, template, o)
	if err != nil {
		return opError("Prompt", err)
	}

	return c.h.act(ctx, "command-prompt", args...)
}

// ChooseTree enters tmux's interactive chooser on a verified target pane; its
// acceptance only means the mode was installed, not that a choice was made.
func (p Pane) ChooseTree(ctx context.Context) error {
	if p.h.server != nil && p.h.server.conn != nil {
		return opError("ChooseTree", ErrTransportUnsupported)
	}

	return p.h.act(ctx, "choose-tree", "-t", p.h.id)
}

// ChooseBuffer enters tmux's interactive buffer selection menu on this pane.
// Acceptance means the mode was installed; it does not indicate whether a buffer was chosen.
func (p Pane) ChooseBuffer(ctx context.Context) error {
	if p.h.server != nil && p.h.server.conn != nil {
		return opError("ChooseBuffer", ErrTransportUnsupported)
	}

	return p.h.act(ctx, "choose-buffer", "-t", p.h.id)
}

// DisplayPanes temporarily overlays numeric indicators on all panes in this client's window,
// allowing the user to select a pane by number.
func (c Client) DisplayPanes(ctx context.Context) error {
	if c.h.server != nil && c.h.server.conn != nil {
		return opError("DisplayPanes", ErrTransportUnsupported)
	}

	return c.h.act(ctx, "display-panes", "-t", c.h.id)
}

// WatchFormat registers a format subscription watch on this control connection.
// When the evaluated format expression changes on the target pane, tmux delivers a
// [SubscriptionEvent] notification over the event stream.
//
// Note on cadence: this reflects tmux's internal update intervals and timer ticks;
// it is a sampling observation mechanism, not a lossless audit log of every intermediate state.
func (c *Connection) WatchFormat(ctx context.Context, name string, pane Pane, expr Format) error {
	if c == nil || !validFormatName(name) || !wire.ValidString(string(expr)) {
		return opError("WatchFormat", invalid("subscription"))
	}

	if err := pane.h.check(); err != nil {
		return opError("WatchFormat", err)
	}

	if !pane.h.origin.Equal(c.identity) {
		return opError("WatchFormat", ErrInvalidHandle)
	}

	opCtx, op, err := c.server.begin(ctx)
	if err != nil {
		return opError("WatchFormat", err)
	}
	defer op.close()

	_, err = c.server.execute(opCtx, op, emptyPlan(command("refresh-client", "-B", name+":"+pane.h.id+":"+string(expr))), pane.h.guard(), nil)

	return opError("WatchFormat", err)
}

// UnwatchFormat cancels a format subscription watch previously registered via [Connection.WatchFormat].
func (c *Connection) UnwatchFormat(ctx context.Context, name string) error {
	if c == nil || !validFormatName(name) {
		return opError("UnwatchFormat", invalid("subscription"))
	}

	return c.server.endpointAction(ctx, "refresh-client", "-B", name)
}

// SetPaneOutput controls whether terminal output events ([PaneOutputEvent]) for the specified pane
// are forwarded over this control connection (refresh-client -A).
func (c *Connection) SetPaneOutput(ctx context.Context, pane Pane, enabled bool) error {
	if c == nil {
		return opError("SetPaneOutput", ErrInvalidHandle)
	}

	if !pane.h.origin.Equal(c.identity) {
		return opError("SetPaneOutput", ErrInvalidHandle)
	}

	flag := "off"
	if enabled {
		flag = "on"
	}

	return c.server.endpointAction(ctx, "refresh-client", "-A", pane.h.id+":"+flag)
}

func popupArgs(clientID string, o PopupOptions) ([]string, error) {
	return popupArgsWithTarget("-c", clientID, o)
}

func popupArgsWithTarget(targetFlag, targetID string, o PopupOptions) ([]string, error) {
	if !o.Size.valid() || (o.CloseOnExit && o.CloseOnSuccess) {
		return nil, invalid("popup options")
	}

	extra, argv, err := programArgs(o.Dir, o.Env, o.Program)
	if err != nil {
		return nil, err
	}

	args := []string{targetFlag, targetID}
	if o.CloseOnExit {
		args = append(args, "-E")
	}

	if o.CloseOnSuccess {
		args = append(args, "-E", "-E")
	}

	args = append(args, popupStyleArgs(o)...)

	args = append(args, popupGeometryArgs(o)...)
	if o.Title != "" {
		v, err := literal(o.Title)
		if err != nil {
			return nil, err
		}

		args = append(args, "-T", v)
	}

	args = append(args, popupExtraArgs(extra)...)

	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}

	return args, nil
}

func menuArgs(clientID string, o MenuOptions) ([]string, error) {
	return menuArgsWithTarget("-c", clientID, o)
}

func menuArgsWithTarget(targetFlag, targetID string, o MenuOptions) ([]string, error) {
	args := []string{targetFlag, targetID}
	if !o.Mouse {
		args = append(args, "-M")
	}

	if o.X != "" {
		args = append(args, "-x", o.X)
	}

	if o.Y != "" {
		args = append(args, "-y", o.Y)
	}

	if o.Title != "" {
		v, err := literal(o.Title)
		if err != nil {
			return nil, err
		}

		args = append(args, "-T", v)
	}

	args = append(args, "--")

	return args, nil
}

func menuItemArg(item MenuItem) ([]wireArg, error) {
	if item.Separator {
		return []wireArg{{text: "", nested: nil}}, nil
	}

	hasCommands := len(item.Commands.commands) > 0
	hasCmd := item.Command != "" && wire.ValidString(item.Command)

	if item.Label == "" || !item.Key.Valid() || !wire.ValidString(item.Label) || (!hasCommands && !hasCmd) {
		return nil, invalid("menu item")
	}

	label := wire.LiteralFormat(item.Label)
	if strings.HasPrefix(label, "-") && !item.Disabled {
		return nil, invalid("menu label would be disabled")
	}

	if item.Disabled {
		label = "-" + label
	}

	var cmdArg wireArg
	if hasCommands {
		cmdArg = wireArg{text: "", nested: item.Commands.nodes()}
	} else {
		cmdArg = wireArg{text: item.Command, nested: nil}
	}

	return []wireArg{
		{text: label, nested: nil},
		{text: string(item.Key), nested: nil},
		cmdArg,
	}, nil
}

func promptArgs(clientID string, template PromptTemplate, o PromptOptions) ([]string, error) {
	if template == "" || !wire.ValidString(string(template)) || !wire.ValidString(o.Label) || !wire.ValidString(o.Initial) || (o.Numeric && o.SingleCharacter) {
		return nil, invalid("prompt")
	}

	args := []string{"-t", clientID}
	if o.Label != "" {
		args = append(args, "-p", wire.LiteralFormat(o.Label))
	}

	if o.Initial != "" {
		args = append(args, "-I", wire.LiteralFormat(o.Initial))
	}

	if o.SingleCharacter {
		args = append(args, "-1")
	}

	if o.Numeric {
		args = append(args, "-N")
	}

	args = append(args, "--", string(template))

	return args, nil
}

func popupGeometryArgs(o PopupOptions) []string {
	var args []string
	if o.Width != "" {
		args = append(args, "-w", o.Width)
	} else if o.Size.Width > 0 {
		args = append(args, "-w", strconv.Itoa(o.Size.Width))
	}

	if o.Height != "" {
		args = append(args, "-h", o.Height)
	} else if o.Size.Height > 0 {
		args = append(args, "-h", strconv.Itoa(o.Size.Height))
	}

	if o.X != "" {
		args = append(args, "-x", o.X)
	}

	if o.Y != "" {
		args = append(args, "-y", o.Y)
	}

	return args
}

func popupStyleArgs(o PopupOptions) []string {
	var args []string
	if o.Borderless {
		args = append(args, "-B")
	}

	if o.Border != "" {
		args = append(args, "-b", string(o.Border))
	}

	if o.Style != "" {
		args = append(args, "-s", o.Style)
	}

	if o.BorderStyle != "" {
		args = append(args, "-S", o.BorderStyle)
	}

	return args
}

func popupExtraArgs(extra []string) []string {
	args := make([]string, 0, len(extra))
	for i := 0; i < len(extra); i += 2 {
		flag := extra[i]
		if flag == "-c" {
			flag = "-d"
		}

		args = append(args, flag, extra[i+1])
	}

	return args
}
