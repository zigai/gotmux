package tmux

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	// ClientFlagIgnoreSize specifies that the client does not affect the size of other clients.
	ClientFlagIgnoreSize ClientFlag = "ignore-size"

	// ClientFlagNoOutput disables receiving pane output in control mode.
	ClientFlagNoOutput ClientFlag = "no-output"

	// ClientFlagReadOnly marks the client as read-only.
	ClientFlagReadOnly ClientFlag = "read-only"

	// ClientFlagActivePane specifies that the client has an independent active pane.
	ClientFlagActivePane ClientFlag = "active-pane"

	// ClientFlagNoDetachOnDestroy prevents detaching the client when the attached session is destroyed.
	ClientFlagNoDetachOnDestroy ClientFlag = "no-detach-on-destroy"

	// ClientFlagWaitExit waits for an empty line input before exiting in control mode.
	ClientFlagWaitExit ClientFlag = "wait-exit"

	// ClientFlagNewLayouts enables new layout framing notifications in control mode.
	ClientFlagNewLayouts ClientFlag = "new-layouts"

	// ClientIgnoreSize and sibling Client* aliases match legacy short names.
	ClientIgnoreSize                    = ClientFlagIgnoreSize
	ClientNoOutput                      = ClientFlagNoOutput
	ClientReadOnly                      = ClientFlagReadOnly
	ClientActivePane                    = ClientFlagActivePane
	ClientNoDetachOnDestroy             = ClientFlagNoDetachOnDestroy
	ClientWaitExit                      = ClientFlagWaitExit
	ClientNewLayouts                    = ClientFlagNewLayouts
	PopupBorderSingle       PopupBorder = "single"
	PopupBorderDouble       PopupBorder = "double"
	PopupBorderHeavy        PopupBorder = "heavy"
	PopupBorderRounded      PopupBorder = "rounded"
	PopupBorderSimple       PopupBorder = "simple"
	PopupBorderPadded       PopupBorder = "padded"
	PopupBorderNone         PopupBorder = "none"

	actionArgMultiplier = 2
)

const (
	// ScrollNone indicates no manual viewport adjustment.
	ScrollNone ScrollDirection = iota

	// ScrollUp moves the visible portion of the window up (-U flag).
	ScrollUp

	// ScrollDown moves the visible portion of the window down (-D flag).
	ScrollDown

	// ScrollLeft moves the visible portion of the window left (-L flag).
	ScrollLeft

	// ScrollRight moves the visible portion of the window right (-R flag).
	ScrollRight
)

const (
	// PaneOutputOn enables output forwarding for the pane.
	PaneOutputOn PaneOutputAction = "on"

	// PaneOutputOff disables output forwarding for the pane.
	PaneOutputOff PaneOutputAction = "off"

	// PaneOutputPause temporarily pauses output forwarding for the pane (%pause).
	PaneOutputPause PaneOutputAction = "pause"

	// PaneOutputContinue resumes output forwarding for the pane (%continue).
	PaneOutputContinue PaneOutputAction = "continue"
)

type (
	// ClientFlag specifies an operational flag for an attached client terminal (-f flag to attach-session or refresh-client).
	ClientFlag    string
	SwitchOptions struct {
		// ToggleReadOnly toggles the client's read-only flag (-r flag to switch-client).
		ToggleReadOnly bool

		// PreserveEnvironment prevents updating the client's environment variables (-E flag).
		PreserveEnvironment bool
	}

	// ScrollDirection indicates viewport scrolling direction for oversized windows.
	ScrollDirection int

	// ScrollAdjustment configures manual viewport scrolling on windows larger than the client terminal.
	ScrollAdjustment struct {
		Direction ScrollDirection
		Amount    int // Number of rows or columns to pan (defaults to 1 if <= 0)
	}

	// WindowSizeOverride configures or clears per-window dimensions on control mode clients (-C @win:size).
	WindowSizeOverride struct {
		// Window is the window ID whose size override is being modified.
		Window WindowID

		// Size is the target dimension. If Size.Width == 0 && Size.Height == 0, the per-window
		// override is cleared (-C @win:).
		Size Size
	}

	// PaneReport forwards terminal escape reports (such as OSC 10/11 color reports) for a pane (-r flag).
	PaneReport struct {
		PaneID PaneID
		Report string
	}

	// PaneOutputAction specifies a flow-control action for pane output in control mode (refresh-client -A).
	PaneOutputAction string

	// PaneOutputTarget pairs a target pane with a flow-control action for batch operations.
	PaneOutputTarget struct {
		Pane   Pane
		Action PaneOutputAction
	}

	// RefreshOptions configures refreshing an attached client's terminal screen or status line.
	RefreshOptions struct {
		// StatusOnly refreshes only the status line without repainting pane contents (-S flag).
		StatusOnly bool

		// Size optionally overrides the client's terminal dimensions (-C flag).
		Size Size

		// WindowSizes optionally sets or clears per-window dimensions on control clients (-C @id:w,h or -C @id:).
		WindowSizes []WindowSizeOverride

		// Flags sets client status flags (-f flag), supporting negation (e.g. "!read-only") and parameters (e.g. "pause-after=2").
		Flags []ClientFlag

		// Clipboard requests the client terminal clipboard via an OSC 52 query (-l flag).
		Clipboard bool

		// ClipboardPane optionally specifies a destination pane when requesting the clipboard.
		ClipboardPane PaneID

		// ResetCursorTracking restores automatic cursor tracking for oversized windows (-c flag).
		ResetCursorTracking bool

		// Scroll adjusts the visible portion of an oversized window (-U, -D, -L, -R flags).
		Scroll ScrollAdjustment

		// PaneActions triggers flow-control actions on control clients (-A flag).
		PaneActions []PaneOutputTarget

		// PaneReports forwards terminal feature/color reports from control clients (-r flag).
		PaneReports []PaneReport
	}

	// SubscriptionTarget specifies an entity or wildcard scope for format evaluation in a control mode subscription (refresh-client -B).
	SubscriptionTarget interface {
		subscriptionTarget(c *Connection) (spec string, g *guard, err error)
	}

	wildcardTarget string

	// PopupBorder specifies the border style for a popup overlay (-b flag).
	PopupBorder string

	// PopupOptions configures displaying an interactive modal popup overlay on a client or pane.
	PopupOptions struct {
		// Program specifies the command to execute inside the popup.
		Program Program

		// Dir specifies the working directory for the popup process.
		Dir string

		// Env specifies environment variable overrides for the popup process via an execution wrapper.
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

		// CloseOnKey closes the popup on key press (-k flag).
		CloseOnKey bool

		// DisableDismiss prevents dismissing the popup with Escape or clicking outside (-N flag).
		DisableDismiss bool

		// Borderless draws the popup without a surrounding border (-B flag).
		Borderless bool

		// TmuxEnv specifies environment variables passed to the popup via tmux (-e KEY=VAL).
		TmuxEnv map[string]string
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

		// Mouse tells tmux the menu should handle mouse events (-M flag).
		// By default, only menus opened from mouse key bindings handle mouse events.
		Mouse bool

		// RequireClick prevents the menu from closing when the mouse button is released without an item selected (-O flag).
		// A mouse button must then be clicked to choose an item.
		RequireClick bool

		// X specifies the menu horizontal position (-x flag, e.g. "C", "R", "P", "M", "W", or a column/format).
		X string
		// Y specifies the menu vertical position (-y flag, e.g. "C", "P", "M", "W", "S", or a row/format).
		Y string

		// Style specifies menu text/background style (-s flag).
		Style string

		// SelectedStyle specifies selected menu item style (-S flag).
		SelectedStyle string
	}

	// MessageOptions configures displaying a status line message on a client.
	MessageOptions struct {
		// Duration specifies how long the message is displayed in milliseconds (-d flag).
		// Must be greater than or equal to 0. A zero value uses tmux's default display time.
		Duration int
	}

	// DisplayMessageOptions is retained as an alias for [MessageOptions].
	DisplayMessageOptions = MessageOptions

	// MessagesOptions configures querying server or client log messages (show-messages).
	MessagesOptions struct {
		// Jobs displays server background jobs instead of log messages (-J flag).
		Jobs bool

		// Terminal displays terminal capabilities/features instead of log messages (-T flag).
		Terminal bool
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

// Valid reports whether the action is one of the recognized tmux pane output actions.
func (a PaneOutputAction) Valid() bool {
	switch a {
	case PaneOutputOn, PaneOutputOff, PaneOutputPause, PaneOutputContinue:
		return true
	default:
		return false
	}
}

// Valid reports whether the popup border style is a recognized tmux border style.
func (b PopupBorder) Valid() bool {
	switch b {
	case PopupBorderSingle, PopupBorderDouble, PopupBorderHeavy, PopupBorderRounded, PopupBorderSimple, PopupBorderPadded, PopupBorderNone:
		return true
	default:
		return false
	}
}

// ClientFlagPauseAfter creates a pause-after client flag with the given duration in seconds.
func ClientFlagPauseAfter(seconds int) ClientFlag {
	if seconds <= 0 {
		return "pause-after"
	}

	return ClientFlag("pause-after=" + strconv.Itoa(seconds))
}

// Negate returns the negated form of this client flag (prefixed with !),
// or removes the leading ! if already negated.
func (f ClientFlag) Negate() ClientFlag {
	if rest, ok := strings.CutPrefix(string(f), "!"); ok {
		return ClientFlag(rest)
	}

	return ClientFlag("!" + string(f))
}

func MenuSeparator() MenuItem {
	return MenuItem{
		Label:     "",
		Key:       "",
		Commands:  CommandSequence{commands: nil},
		Command:   "",
		Separator: true,
		Disabled:  false,
	}
}

// Valid reports whether this client flag is one of the recognized flags.
func (f ClientFlag) Valid() bool {
	raw := strings.TrimPrefix(string(f), "!")
	if strings.HasPrefix(raw, "pause-after") {
		if raw == "pause-after" {
			return true
		}

		if val, ok := strings.CutPrefix(raw, "pause-after="); ok {
			n, err := strconv.Atoi(val)
			return err == nil && n >= 0
		}

		return false
	}

	switch ClientFlag(raw) {
	case ClientFlagIgnoreSize, ClientFlagNoOutput, ClientFlagReadOnly, ClientFlagActivePane,
		ClientFlagNoDetachOnDestroy, ClientFlagWaitExit, ClientFlagNewLayouts:
		return true
	default:
		return false
	}
}

// Switch switches this client terminal to display session s.
func (c Client) Switch(ctx context.Context, s Session, opts SwitchOptions) error {
	opCtx, op, err := beginHandles(ctx, &c.h, &s.h)
	if err != nil {
		return opError("SwitchClient", err)
	}
	defer op.close()

	args := []string{"-c", c.h.id, "-t", s.h.id}
	if opts.ToggleReadOnly {
		args = append(args, "-r")
	}

	if opts.PreserveEnvironment {
		args = append(args, "-E")
	}

	_, err = c.h.server.execute(opCtx, op, emptyPlan(command("switch-client", args...)), c.h.guard(), nil)

	return opError("switch-client", err)
}

// Detach detaches this client terminal from the tmux server (detach-client).
func (c Client) Detach(ctx context.Context) error { return c.h.act(ctx, "detach-client", "-t", c.h.id) }

// LockScreen locks this client terminal (lock-client).
func (c Client) LockScreen(ctx context.Context) error {
	return c.h.act(ctx, "lock-client", "-t", c.h.id)
}

// Suspend suspends this client terminal (suspend-client).
func (c Client) Suspend(ctx context.Context) error {
	return c.h.act(ctx, "suspend-client", "-t", c.h.id)
}

func (c Client) refreshScrollArgs(scroll ScrollAdjustment) ([]string, error) {
	if scroll.Direction == ScrollNone {
		return nil, nil
	}

	var flag string

	switch scroll.Direction {
	case ScrollNone:
	case ScrollUp:
		flag = "-U"
	case ScrollDown:
		flag = "-D"
	case ScrollLeft:
		flag = "-L"
	case ScrollRight:
		flag = "-R"
	default:
		return nil, invalid("scroll direction")
	}

	args := []string{flag}
	if scroll.Amount > 1 {
		args = append(args, strconv.Itoa(scroll.Amount))
	}

	return args, nil
}

func (c Client) refreshWindowSizeArgs(sizes []WindowSizeOverride) ([]string, error) {
	if len(sizes) == 0 {
		return nil, nil
	}

	const windowSizeArgMultiplier = 2

	args := make([]string, 0, len(sizes)*windowSizeArgMultiplier)
	for _, ws := range sizes {
		if !ws.Window.Valid() {
			return nil, invalid("window id")
		}

		if !ws.Size.valid() {
			return nil, invalid("window size")
		}

		if ws.Size.Width == 0 && ws.Size.Height == 0 {
			args = append(args, "-C", string(ws.Window)+":")
		} else {
			if ws.Size.Width == 0 || ws.Size.Height == 0 {
				return nil, invalid("complete window size required")
			}

			args = append(args, "-C", fmt.Sprintf("%s:%d,%d", ws.Window, ws.Size.Width, ws.Size.Height))
		}
	}

	return args, nil
}

func (c Client) refreshActionsAndReportsArgs(actions []PaneOutputTarget, reports []PaneReport) ([]string, error) {
	var args []string

	for _, pa := range actions {
		if err := pa.Pane.h.check(); err != nil {
			return nil, err
		}

		if !pa.Action.Valid() {
			return nil, invalid("pane action")
		}

		args = append(args, "-A", pa.Pane.h.id+":"+string(pa.Action))
	}

	for _, pr := range reports {
		if !pr.PaneID.Valid() {
			return nil, invalid("pane id")
		}

		if !wire.ValidString(pr.Report) {
			return nil, invalid("pane report")
		}

		args = append(args, "-r", string(pr.PaneID)+":"+pr.Report)
	}

	return args, nil
}

func (c Client) refreshClipboardArgs(clipboard bool, pane PaneID) ([]string, error) {
	if !clipboard {
		return nil, nil
	}

	if pane != "" {
		if !pane.Valid() {
			return nil, invalid("clipboard pane")
		}

		return []string{"-l", string(pane)}, nil
	}

	return []string{"-l"}, nil
}

func (c Client) refreshSizeArgs(size Size) ([]string, error) {
	if size.Width != 0 || size.Height != 0 {
		if size.Width == 0 || size.Height == 0 {
			return nil, invalid("complete size required")
		}

		return []string{"-C", strconv.Itoa(size.Width) + "," + strconv.Itoa(size.Height)}, nil
	}

	return nil, nil
}

func (c Client) Refresh(ctx context.Context, opts RefreshOptions) error {
	if !opts.Size.valid() {
		return opError("RefreshClient", invalid("size"))
	}

	args := []string{"-t", c.h.id}
	if opts.StatusOnly {
		args = append(args, "-S")
	}

	if opts.ResetCursorTracking {
		args = append(args, "-c")
	}

	scrollArgs, err := c.refreshScrollArgs(opts.Scroll)
	if err != nil {
		return opError("RefreshClient", err)
	}

	args = append(args, scrollArgs...)

	clipArgs, err := c.refreshClipboardArgs(opts.Clipboard, opts.ClipboardPane)
	if err != nil {
		return opError("RefreshClient", err)
	}

	args = append(args, clipArgs...)

	szArgs, err := c.refreshSizeArgs(opts.Size)
	if err != nil {
		return opError("RefreshClient", err)
	}

	args = append(args, szArgs...)

	winArgs, err := c.refreshWindowSizeArgs(opts.WindowSizes)
	if err != nil {
		return opError("RefreshClient", err)
	}

	args = append(args, winArgs...)

	for _, f := range opts.Flags {
		if !f.Valid() {
			return opError("RefreshClient", invalid("client flag"))
		}
	}

	if len(opts.Flags) > 0 {
		args = append(args, "-f", formatClientFlags(opts.Flags))
	}

	actArgs, err := c.refreshActionsAndReportsArgs(opts.PaneActions, opts.PaneReports)
	if err != nil {
		return opError("RefreshClient", err)
	}

	args = append(args, actArgs...)

	return c.h.act(ctx, "refresh-client", args...)
}

// SetWindowSize overrides the dimensions of a specific window on this control client (-C @win:size).
func (c Client) SetWindowSize(ctx context.Context, window Window, size Size) error {
	//nolint:exhaustruct_v5 // convenience wrapper populates target fields
	return c.Refresh(ctx, RefreshOptions{
		WindowSizes: []WindowSizeOverride{{Window: WindowID(window.h.id), Size: size}},
	})
}

// ClearWindowSize clears the per-window size override for a specific window on this control client (-C @win:).
func (c Client) ClearWindowSize(ctx context.Context, window Window) error {
	//nolint:exhaustruct_v5 // convenience wrapper populates target fields
	return c.Refresh(ctx, RefreshOptions{
		WindowSizes: []WindowSizeOverride{{Window: WindowID(window.h.id), Size: Size{Width: 0, Height: 0}}},
	})
}

// RequestClipboard requests the client's clipboard using the xterm OSC 52 escape sequence (-l flag).
func (c Client) RequestClipboard(ctx context.Context) error {
	//nolint:exhaustruct_v5 // convenience wrapper populates target fields
	return c.Refresh(ctx, RefreshOptions{Clipboard: true})
}

// ResetCursorTracking resets the visible portion of oversized windows to track the cursor automatically (-c flag).
func (c Client) ResetCursorTracking(ctx context.Context) error {
	//nolint:exhaustruct_v5 // convenience wrapper populates target fields
	return c.Refresh(ctx, RefreshOptions{ResetCursorTracking: true})
}

// Scroll pans the visible portion of an oversized window in the specified direction (-U, -D, -L, -R flags).
func (c Client) Scroll(ctx context.Context, dir ScrollDirection, amount int) error {
	//nolint:exhaustruct_v5 // convenience wrapper populates target fields
	return c.Refresh(ctx, RefreshOptions{
		Scroll: ScrollAdjustment{Direction: dir, Amount: amount},
	})
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

// MessageWith requests a status message on this client with custom display options.
// Completion means tmux accepted the display request, not that the user read or dismissed it.
func (c Client) MessageWith(ctx context.Context, text string, opts MessageOptions) error {
	if !wire.ValidString(text) {
		return opError("MessageWith", invalid("message"))
	}

	if opts.Duration < 0 {
		return opError("MessageWith", invalid("duration"))
	}

	if c.h.server != nil && c.h.server.conn != nil {
		return opError("MessageWith", ErrTransportUnsupported)
	}

	args := []string{"-c", c.h.id}
	if opts.Duration > 0 {
		args = append(args, "-d", strconv.Itoa(opts.Duration))
	}

	args = append(args, "--", wire.LiteralFormat(text))

	return c.h.act(ctx, "display-message", args...)
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

// Messages returns log messages, background jobs, or terminal capabilities from the server (show-messages).
func (s *Server) Messages(ctx context.Context, opts MessagesOptions) ([]string, error) {
	if s == nil {
		return nil, opError("Messages", ErrInvalidHandle)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Messages", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Messages", err)
	}

	var args []string
	if opts.Jobs {
		args = append(args, "-J")
	}

	if opts.Terminal {
		args = append(args, "-T")
	}

	r, err := s.execute(opCtx, op, plainPlan(command("show-messages", args...)), newGuard(info.Identity), nil)
	if err != nil {
		return nil, opError("Messages", err)
	}

	var lines []string
	for line := range bytes.SplitSeq(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		lines = append(lines, string(line))
	}

	return lines, nil
}

// Messages returns log messages or terminal capabilities for this specific client (show-messages -t).
func (c Client) Messages(ctx context.Context, opts MessagesOptions) ([]string, error) {
	if err := c.h.check(); err != nil {
		return nil, opError("Messages", err)
	}

	opCtx, op, err := c.h.server.begin(ctx)
	if err != nil {
		return nil, opError("Messages", err)
	}
	defer op.close()

	args := []string{"-t", c.h.id}
	if opts.Jobs {
		args = append(args, "-J")
	}

	if opts.Terminal {
		args = append(args, "-T")
	}

	r, err := c.h.server.execute(opCtx, op, plainPlan(command("show-messages", args...)), c.h.guard(), nil)
	if err != nil {
		return nil, opError("Messages", err)
	}

	var lines []string
	for line := range bytes.SplitSeq(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		lines = append(lines, string(line))
	}

	return lines, nil
}

// Popup waits for tmux's popup command queue to resume (normally dismissal).
// Cancellation ends the local waiter, not necessarily the server-side popup.
// No exit status or user choice is inferred.
func (c Client) Popup(ctx context.Context, opts PopupOptions) error {
	if ctx == nil {
		return opError("Popup", invalid("nil context"))
	}

	if err := c.h.check(); err != nil {
		return opError("Popup", err)
	}

	if c.h.server.conn != nil {
		return opError("Popup", ErrTransportUnsupported)
	}

	args, err := popupArgs(c.h.id, opts)
	if err != nil {
		return opError("Popup", err)
	}

	return c.h.act(ctx, "display-popup", args...)
}

func (c Client) ClosePopup(ctx context.Context) error {
	return c.h.act(ctx, "display-popup", "-C", "-c", c.h.id)
}

// Popup displays an interactive modal popup overlay targeting this pane (-t flag).
// Cancellation ends the local waiter, not necessarily the server-side popup.
// No exit status or user choice is inferred.
func (p Pane) Popup(ctx context.Context, opts PopupOptions) error {
	if ctx == nil {
		return opError("Popup", invalid("nil context"))
	}

	if err := p.h.check(); err != nil {
		return opError("Popup", err)
	}

	if p.h.server.conn != nil {
		return opError("Popup", ErrTransportUnsupported)
	}

	args, err := popupArgsWithTarget("-t", p.h.id, opts)
	if err != nil {
		return opError("Popup", err)
	}

	return p.h.act(ctx, "display-popup", args...)
}

// Menu displays an interactive popup menu on this client and blocks until dismissal.
//
// Cancellation ends the local waiter but does not guarantee immediate dismissal of the menu on the client.
// Fails with [ErrTransportUnsupported] over control mode.
func (c Client) Menu(ctx context.Context, items []MenuItem, opts MenuOptions) error {
	if ctx == nil {
		return opError("Menu", invalid("nil context"))
	}

	if err := c.h.check(); err != nil {
		return opError("Menu", err)
	}

	if c.h.server.conn != nil {
		return opError("Menu", ErrTransportUnsupported)
	}

	args, err := menuArgs(c.h.id, opts)
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
//
// Cancellation ends the local waiter but does not guarantee immediate dismissal of the menu on the client.
// Fails with [ErrTransportUnsupported] over control mode.
func (p Pane) Menu(ctx context.Context, items []MenuItem, opts MenuOptions) error {
	if ctx == nil {
		return opError("Menu", invalid("nil context"))
	}

	if err := p.h.check(); err != nil {
		return opError("Menu", err)
	}

	if p.h.server.conn != nil {
		return opError("Menu", ErrTransportUnsupported)
	}

	args, err := menuArgsWithTarget("-t", p.h.id, opts)
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
// Fails with [ErrTransportUnsupported] over control mode.
func (c Client) Prompt(ctx context.Context, template PromptTemplate, opts PromptOptions) error {
	if ctx == nil {
		return opError("Prompt", invalid("nil context"))
	}

	if err := c.h.check(); err != nil {
		return opError("Prompt", err)
	}

	if c.h.server.conn != nil {
		return opError("Prompt", ErrTransportUnsupported)
	}

	args, err := promptArgs(c.h.id, template, opts)
	if err != nil {
		return opError("Prompt", err)
	}

	return c.h.act(ctx, "command-prompt", args...)
}

// PromptHistory returns the prompt history lines from tmux (show-prompt-history).
// promptType optionally restricts history to a specific prompt type ("command", "search").
func (s *Server) PromptHistory(ctx context.Context, promptType string) ([]string, error) {
	if s == nil {
		return nil, opError("PromptHistory", ErrInvalidHandle)
	}

	if promptType != "" && !wire.ValidString(promptType) {
		return nil, opError("PromptHistory", invalid("prompt type"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("PromptHistory", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("PromptHistory", err)
	}

	var args []string
	if promptType != "" {
		args = append(args, "-T", promptType)
	}

	r, err := s.execute(opCtx, op, plainPlan(command("show-prompt-history", args...)), newGuard(info.Identity), nil)
	if err != nil {
		return nil, opError("PromptHistory", err)
	}

	var lines []string
	for line := range bytes.SplitSeq(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		lines = append(lines, string(line))
	}

	return lines, nil
}

// ClearPromptHistory clears the prompt history in tmux (clear-prompt-history).
// promptType optionally restricts clearing to a specific prompt type ("command", "search").
func (s *Server) ClearPromptHistory(ctx context.Context, promptType string) error {
	if s == nil {
		return opError("ClearPromptHistory", ErrInvalidHandle)
	}

	if promptType != "" && !wire.ValidString(promptType) {
		return opError("ClearPromptHistory", invalid("prompt type"))
	}

	var args []string
	if promptType != "" {
		args = append(args, "-T", promptType)
	}

	return s.endpointAction(ctx, "clear-prompt-history", args...)
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
// SubscriptionTarget specifies an entity or wildcard scope for format evaluation in a control mode subscription (refresh-client -B).

func (p Pane) subscriptionTarget(c *Connection) (string, *guard, error) {
	if err := p.h.check(); err != nil {
		return "", nil, err
	}

	if !p.h.origin.Equal(c.identity) {
		return "", nil, ErrInvalidHandle
	}

	return p.h.id, p.h.guard(), nil
}

func (w Window) subscriptionTarget(c *Connection) (string, *guard, error) {
	if err := w.h.check(); err != nil {
		return "", nil, err
	}

	if !w.h.origin.Equal(c.identity) {
		return "", nil, ErrInvalidHandle
	}

	return w.h.id, w.h.guard(), nil
}

//nolint:unparam // session subscription uses empty spec string
func (s Session) subscriptionTarget(c *Connection) (string, *guard, error) {
	if err := s.h.check(); err != nil {
		return "", nil, err
	}

	if !s.h.origin.Equal(c.identity) {
		return "", nil, ErrInvalidHandle
	}

	return "", s.h.guard(), nil
}

func (w wildcardTarget) subscriptionTarget(c *Connection) (string, *guard, error) {
	return string(w), nil, nil
}

// TargetAllPanes returns a [SubscriptionTarget] matching all panes in the attached session (%*).
func TargetAllPanes() SubscriptionTarget { return wildcardTarget("%*") }

// TargetAllWindows returns a [SubscriptionTarget] matching all windows in the attached session (@*).
func TargetAllWindows() SubscriptionTarget { return wildcardTarget("@*") }

// TargetSession returns a [SubscriptionTarget] matching the attached session (empty target between colons).
func TargetSession() SubscriptionTarget { return wildcardTarget("") }

// WatchFormatWith registers a format subscription watch on any supported target:
// a [Pane], [Window], [Session], [TargetAllPanes], [TargetAllWindows], or [TargetSession].
//
// Changes to the evaluated format expression are emitted asynchronously as
// [SubscriptionEvent] notifications over the event stream.
func (c *Connection) WatchFormatWith(ctx context.Context, name string, target SubscriptionTarget, expr Format) error {
	if c == nil || !validFormatName(name) || !wire.ValidString(string(expr)) {
		return opError("WatchFormatWith", invalid("subscription"))
	}

	if target == nil {
		return opError("WatchFormatWith", invalid("subscription target"))
	}

	spec, g, err := target.subscriptionTarget(c)
	if err != nil {
		return opError("WatchFormatWith", err)
	}

	opCtx, op, err := c.server.begin(ctx)
	if err != nil {
		return opError("WatchFormatWith", err)
	}
	defer op.close()

	arg := name + ":" + spec + ":" + string(expr)
	_, err = c.server.execute(opCtx, op, emptyPlan(command("refresh-client", "-B", arg)), g, nil)

	return opError("WatchFormatWith", err)
}

// WatchFormat registers a format subscription watch on the specified pane.
// It is equivalent to WatchFormatWith(ctx, name, pane, expr).
func (c *Connection) WatchFormat(ctx context.Context, name string, pane Pane, expr Format) error {
	return c.WatchFormatWith(ctx, name, pane, expr)
}

// UnwatchFormat cancels a format subscription watch previously registered via [Connection.WatchFormat] or [Connection.WatchFormatWith].
func (c *Connection) UnwatchFormat(ctx context.Context, name string) error {
	if c == nil || !validFormatName(name) {
		return opError("UnwatchFormat", invalid("subscription"))
	}

	return c.server.endpointAction(ctx, "refresh-client", "-B", name)
}

// SetPaneOutputAction applies a specific flow-control action (on, off, pause, continue)
// to the specified pane on this control connection (refresh-client -A).
func (c *Connection) SetPaneOutputAction(ctx context.Context, pane Pane, action PaneOutputAction) error {
	if c == nil {
		return opError("SetPaneOutputAction", ErrInvalidHandle)
	}

	if !pane.h.origin.Equal(c.identity) {
		return opError("SetPaneOutputAction", ErrInvalidHandle)
	}

	if !action.Valid() {
		return opError("SetPaneOutputAction", invalid("pane output action"))
	}

	return c.server.endpointAction(ctx, "refresh-client", "-A", pane.h.id+":"+string(action))
}

// SetPaneOutput controls whether terminal output events ([PaneOutputEvent]) for the specified pane
// are forwarded over this control connection (refresh-client -A).
func (c *Connection) SetPaneOutput(ctx context.Context, pane Pane, enabled bool) error {
	if enabled {
		return c.SetPaneOutputAction(ctx, pane, PaneOutputOn)
	}

	return c.SetPaneOutputAction(ctx, pane, PaneOutputOff)
}

// SetPaneOutputActions applies flow-control actions to multiple panes in a single refresh-client command.
func (c *Connection) SetPaneOutputActions(ctx context.Context, targets ...PaneOutputTarget) error {
	if c == nil {
		return opError("SetPaneOutputActions", ErrInvalidHandle)
	}

	if len(targets) == 0 {
		return nil
	}

	args := make([]string, 0, len(targets)*actionArgMultiplier)
	for _, t := range targets {
		if !t.Pane.h.origin.Equal(c.identity) {
			return opError("SetPaneOutputActions", ErrInvalidHandle)
		}

		if !t.Action.Valid() {
			return opError("SetPaneOutputActions", invalid("pane output action"))
		}

		args = append(args, "-A", t.Pane.h.id+":"+string(t.Action))
	}

	return c.server.endpointAction(ctx, "refresh-client", args...)
}

// Refresh applies client refresh operations to this connection's owned client (refresh-client).
func (c *Connection) Refresh(ctx context.Context, opts RefreshOptions) error {
	if c == nil {
		return opError("Connection.Refresh", ErrInvalidHandle)
	}

	client, err := c.Client(ctx)
	if err != nil {
		return opError("Connection.Refresh", err)
	}

	return client.Refresh(ctx, opts)
}

// SetWindowSize overrides the dimensions of a specific window on this control connection (-C @win:size).
func (c *Connection) SetWindowSize(ctx context.Context, window Window, size Size) error {
	if c == nil {
		return opError("Connection.SetWindowSize", ErrInvalidHandle)
	}

	client, err := c.Client(ctx)
	if err != nil {
		return opError("Connection.SetWindowSize", err)
	}

	return client.SetWindowSize(ctx, window, size)
}

// ClearWindowSize clears the per-window size override for a specific window on this control connection (-C @win:).
func (c *Connection) ClearWindowSize(ctx context.Context, window Window) error {
	if c == nil {
		return opError("Connection.ClearWindowSize", ErrInvalidHandle)
	}

	client, err := c.Client(ctx)
	if err != nil {
		return opError("Connection.ClearWindowSize", err)
	}

	return client.ClearWindowSize(ctx, window)
}

// ResetCursorTracking resets the visible portion of oversized windows to track the cursor automatically (-c flag).
func (c *Connection) ResetCursorTracking(ctx context.Context) error {
	if c == nil {
		return opError("Connection.ResetCursorTracking", ErrInvalidHandle)
	}

	client, err := c.Client(ctx)
	if err != nil {
		return opError("Connection.ResetCursorTracking", err)
	}

	return client.ResetCursorTracking(ctx)
}

// Scroll pans the visible portion of an oversized window on this control connection (-U, -D, -L, -R flags).
func (c *Connection) Scroll(ctx context.Context, dir ScrollDirection, amount int) error {
	if c == nil {
		return opError("Connection.Scroll", ErrInvalidHandle)
	}

	client, err := c.Client(ctx)
	if err != nil {
		return opError("Connection.Scroll", err)
	}

	return client.Scroll(ctx, dir, amount)
}

// RequestClipboard requests the client's clipboard using the xterm OSC 52 escape sequence (-l flag).
func (c *Connection) RequestClipboard(ctx context.Context) error {
	if c == nil {
		return opError("Connection.RequestClipboard", ErrInvalidHandle)
	}

	client, err := c.Client(ctx)
	if err != nil {
		return opError("Connection.RequestClipboard", err)
	}

	return client.RequestClipboard(ctx)
}

func popupArgs(clientID string, opts PopupOptions) ([]string, error) {
	return popupArgsWithTarget("-c", clientID, opts)
}

func popupArgsWithTarget(targetFlag, targetID string, opts PopupOptions) ([]string, error) {
	if !opts.Size.valid() || (opts.CloseOnExit && opts.CloseOnSuccess) {
		return nil, invalid("popup options")
	}

	for _, s := range []string{opts.Width, opts.Height, opts.X, opts.Y, string(opts.Border), opts.Style, opts.BorderStyle} {
		if !wire.ValidString(s) {
			return nil, invalid("popup options")
		}
	}

	envArgs, err := popupEnvArgs(opts.TmuxEnv)
	if err != nil {
		return nil, err
	}

	extra, argv, err := programArgs(opts.Dir, opts.Env, opts.Program)
	if err != nil {
		return nil, err
	}

	args := []string{targetFlag, targetID}
	args = append(args, popupFlagArgs(opts)...)
	args = append(args, envArgs...)
	args = append(args, popupStyleArgs(opts)...)
	args = append(args, popupGeometryArgs(opts)...)

	if opts.Title != "" {
		v, err := literal(opts.Title)
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

func popupEnvArgs(env map[string]string) ([]string, error) {
	if len(env) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(env))
	for k, v := range env {
		if !envName(k) || !wire.ValidString(v) {
			return nil, invalid("popup options")
		}

		keys = append(keys, k)
	}

	slices.Sort(keys)

	const envFlagPair = 2

	args := make([]string, 0, len(keys)*envFlagPair)

	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}

	return args, nil
}

func popupFlagArgs(opts PopupOptions) []string {
	var args []string

	if opts.CloseOnExit {
		args = append(args, "-E")
	}

	if opts.CloseOnSuccess {
		args = append(args, "-E", "-E")
	}

	if opts.CloseOnKey {
		args = append(args, "-k")
	}

	if opts.DisableDismiss {
		args = append(args, "-N")
	}

	return args
}

func menuArgs(clientID string, opts MenuOptions) ([]string, error) {
	return menuArgsWithTarget("-c", clientID, opts)
}

func menuArgsWithTarget(targetFlag, targetID string, opts MenuOptions) ([]string, error) {
	if !wire.ValidString(opts.X) || !wire.ValidString(opts.Y) || !wire.ValidString(opts.Style) || !wire.ValidString(opts.SelectedStyle) {
		return nil, invalid("menu options")
	}

	args := []string{targetFlag, targetID}

	if opts.Mouse {
		args = append(args, "-M")
	}

	if opts.RequireClick {
		args = append(args, "-O")
	}

	args = append(args, menuPositionArgs(opts)...)
	args = append(args, menuStyleArgs(opts)...)

	if opts.Title != "" {
		title, err := literal(opts.Title)
		if err != nil {
			return nil, err
		}

		args = append(args, "-T", title)
	}

	return args, nil
}

func menuPositionArgs(opts MenuOptions) []string {
	var args []string

	if opts.X != "" {
		args = append(args, "-x", opts.X)
	}

	if opts.Y != "" {
		args = append(args, "-y", opts.Y)
	}

	return args
}

func menuStyleArgs(opts MenuOptions) []string {
	var args []string

	if opts.Style != "" {
		args = append(args, "-s", opts.Style)
	}

	if opts.SelectedStyle != "" {
		args = append(args, "-S", opts.SelectedStyle)
	}

	return args
}

func menuItemArg(item MenuItem) ([]wireArg, error) {
	if item.Separator {
		return []wireArg{{text: "", nested: nil}}, nil
	}

	if err := validateMenuItem(item); err != nil {
		return nil, err
	}

	label, err := formatMenuLabel(item)
	if err != nil {
		return nil, err
	}

	return []wireArg{
		{text: label, nested: nil},
		{text: string(item.Key), nested: nil},
		menuItemCommandArg(item),
	}, nil
}

func validateMenuItem(item MenuItem) error {
	if item.Label == "" || !wire.ValidString(item.Label) {
		return invalid("menu item")
	}

	if item.Key != "" && !item.Key.Valid() {
		return invalid("menu item key")
	}

	hasCommands := len(item.Commands.commands) > 0
	hasCmd := item.Command != "" && wire.ValidString(item.Command)

	if !item.Disabled && !hasCommands && !hasCmd {
		return invalid("menu item command")
	}

	return nil
}

func menuItemCommandArg(item MenuItem) wireArg {
	if len(item.Commands.commands) > 0 {
		return wireArg{text: "", nested: item.Commands.nodes()}
	}

	if item.Command != "" && wire.ValidString(item.Command) {
		return wireArg{text: item.Command, nested: nil}
	}

	return wireArg{text: "", nested: nil}
}

func formatMenuLabel(item MenuItem) (string, error) {
	label := wire.LiteralFormat(item.Label)
	if strings.HasPrefix(label, "-") && !item.Disabled {
		return "", invalid("menu label would be disabled")
	}

	if item.Disabled && !strings.HasPrefix(label, "-") {
		label = "-" + label
	}

	return label, nil
}

func promptArgs(clientID string, template PromptTemplate, opts PromptOptions) ([]string, error) {
	if template == "" || !wire.ValidString(string(template)) || !wire.ValidString(opts.Label) || !wire.ValidString(opts.Initial) || (opts.Numeric && opts.SingleCharacter) {
		return nil, invalid("prompt")
	}

	args := []string{"-t", clientID}
	if opts.Label != "" {
		args = append(args, "-p", wire.LiteralFormat(opts.Label))
	}

	if opts.Initial != "" {
		args = append(args, "-I", wire.LiteralFormat(opts.Initial))
	}

	if opts.SingleCharacter {
		args = append(args, "-1")
	}

	if opts.Numeric {
		args = append(args, "-N")
	}

	args = append(args, "--", string(template))

	return args, nil
}

func popupGeometryArgs(opts PopupOptions) []string {
	var args []string
	if opts.Width != "" {
		args = append(args, "-w", opts.Width)
	} else if opts.Size.Width > 0 {
		args = append(args, "-w", strconv.Itoa(opts.Size.Width))
	}

	if opts.Height != "" {
		args = append(args, "-h", opts.Height)
	} else if opts.Size.Height > 0 {
		args = append(args, "-h", strconv.Itoa(opts.Size.Height))
	}

	if opts.X != "" {
		args = append(args, "-x", opts.X)
	}

	if opts.Y != "" {
		args = append(args, "-y", opts.Y)
	}

	return args
}

func popupStyleArgs(opts PopupOptions) []string {
	var args []string
	if opts.Borderless {
		args = append(args, "-B")
	}

	if opts.Border != "" {
		args = append(args, "-b", string(opts.Border))
	}

	if opts.Style != "" {
		args = append(args, "-s", opts.Style)
	}

	if opts.BorderStyle != "" {
		args = append(args, "-S", opts.BorderStyle)
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

func formatClientFlags(flags []ClientFlag) string {
	if len(flags) == 0 {
		return ""
	}

	strs := make([]string, len(flags))
	for i, f := range flags {
		strs[i] = string(f)
	}

	return strings.Join(strs, ",")
}
