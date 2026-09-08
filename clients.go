package tmux

import (
	"context"
	"strconv"
	"strings"

	"example.com/tmux/internal/codec"
)

type ClientFlag string

const (
	ClientIgnoreSize ClientFlag = "ignore-size"
	ClientNoOutput   ClientFlag = "no-output"
	ClientReadOnly   ClientFlag = "read-only"
	ClientActivePane ClientFlag = "active-pane"
)

func (f ClientFlag) valid() bool {
	switch f {
	case ClientIgnoreSize, ClientNoOutput, ClientReadOnly, ClientActivePane:
		return true
	}
	return false
}

type SwitchOptions struct {
	ReadOnly            bool
	PreserveEnvironment bool
}

func (c Client) Switch(ctx context.Context, s Session, o SwitchOptions) error {
	if e := sameHandles(c.h, s.h); e != nil {
		return opError("SwitchClient", e)
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
func (c Client) Detach(ctx context.Context) error { return c.h.act(ctx, "detach-client", "-t", c.h.id) }

type RefreshOptions struct {
	StatusOnly bool
	Size       Size
	Flags      []ClientFlag
}

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
	if !codec.ValidString(text) {
		return opError("Message", invalid("message"))
	}
	if c.h.server != nil && c.h.server.conn != nil {
		return opError("Message", ErrTransportUnsupported)
	}
	return c.h.act(ctx, "display-message", "-c", c.h.id, "--", codec.LiteralFormat(text))
}

type PopupOptions struct {
	Program        Program
	Dir            string
	Env            map[string]string
	Size           Size
	Title          string
	CloseOnExit    bool
	CloseOnSuccess bool
	Borderless     bool
}

// Popup waits for tmux's popup command queue to resume (normally dismissal).
// A deadline is required. Cancellation ends the local waiter, not necessarily
// the server-side popup. No exit status or user choice is inferred.
func (c Client) Popup(ctx context.Context, o PopupOptions) error {
	if e := requireDeadline(ctx); e != nil {
		return opError("Popup", e)
	}
	if e := c.h.check(); e != nil {
		return opError("Popup", e)
	}
	if c.h.server.conn != nil {
		return opError("Popup", ErrTransportUnsupported)
	}
	if !o.Size.valid() || o.CloseOnExit && o.CloseOnSuccess {
		return opError("Popup", invalid("popup options"))
	}
	extra, argv, e := programArgs(o.Dir, o.Env, o.Program)
	if e != nil {
		return opError("Popup", e)
	}
	args := []string{"-c", c.h.id}
	if o.CloseOnExit {
		args = append(args, "-E")
	}
	if o.CloseOnSuccess {
		args = append(args, "-E", "-E")
	}
	if o.Borderless {
		args = append(args, "-B")
	}
	if o.Size.Width > 0 {
		args = append(args, "-w", strconv.Itoa(o.Size.Width))
	}
	if o.Size.Height > 0 {
		args = append(args, "-h", strconv.Itoa(o.Size.Height))
	}
	if o.Title != "" {
		v, e := literal(o.Title)
		if e != nil {
			return opError("Popup", e)
		}
		args = append(args, "-T", v)
	}
	// Popup spells working directory -d, unlike pane creation's -c.
	for i := 0; i < len(extra); i += 2 {
		flag := extra[i]
		if flag == "-c" {
			flag = "-d"
		}
		args = append(args, flag, extra[i+1])
	}
	if len(argv) > 0 {
		args = append(args, "--")
		args = append(args, argv...)
	}
	return c.h.act(ctx, "display-popup", args...)
}

type MenuItem struct {
	Label     string
	Key       Key
	Commands  CommandSequence
	Separator bool
	Disabled  bool
}
type MenuOptions struct {
	Title    string
	Mouse    bool
	StayOpen bool
}

func (c Client) Menu(ctx context.Context, items []MenuItem, o MenuOptions) error {
	if e := requireDeadline(ctx); e != nil {
		return opError("Menu", e)
	}
	if e := c.h.check(); e != nil {
		return opError("Menu", e)
	}
	if c.h.server.conn != nil {
		return opError("Menu", ErrTransportUnsupported)
	}
	if o.StayOpen {
		return opError("Menu", &UnsupportedError{Feature: "persistent menu completion policy"})
	}
	args := []string{"-c", c.h.id}
	if !o.Mouse {
		args = append(args, "-M")
	}
	if o.Title != "" {
		v, e := literal(o.Title)
		if e != nil {
			return opError("Menu", e)
		}
		args = append(args, "-T", v)
	}
	args = append(args, "--")
	node := leaf(command("display-menu", args...))
	for _, item := range items {
		if item.Separator {
			node.args = append(node.args, wireArg{text: ""})
			continue
		}
		if !item.Key.Valid() || !codec.ValidString(item.Label) || len(item.Commands.commands) == 0 {
			return opError("Menu", invalid("menu item"))
		}
		label := codec.LiteralFormat(item.Label)
		if strings.HasPrefix(label, "-") && !item.Disabled {
			return opError("Menu", invalid("menu label would be disabled"))
		}
		if item.Disabled {
			label = "-" + label
		}
		node.args = append(node.args, wireArg{text: label}, wireArg{text: string(item.Key)}, wireArg{nested: item.Commands.nodes()})
	}
	op, e := c.h.server.begin(ctx)
	if e != nil {
		return opError("Menu", e)
	}
	defer op.close()
	_, e = c.h.server.execute(op, plan{nodes: []wireNode{node}, mode: replyEmpty}, c.h.guard(), nil)
	return opError("Menu", e)
}

// PromptTemplate is deliberately tmux command-prompt syntax. Its %%/%%N
// substitution semantics differ from literal CommandSequence arguments.
type PromptTemplate string
type PromptOptions struct {
	Label           string
	Initial         string
	SingleCharacter bool
	Numeric         bool
}

func (c Client) Prompt(ctx context.Context, template PromptTemplate, o PromptOptions) error {
	if e := requireDeadline(ctx); e != nil {
		return opError("Prompt", e)
	}
	if e := c.h.check(); e != nil {
		return opError("Prompt", e)
	}
	if c.h.server.conn != nil {
		return opError("Prompt", ErrTransportUnsupported)
	}
	if template == "" || !codec.ValidString(string(template)) || !codec.ValidString(o.Label) || !codec.ValidString(o.Initial) || o.Numeric && o.SingleCharacter {
		return opError("Prompt", invalid("prompt"))
	}
	args := []string{"-t", c.h.id}
	if o.Label != "" {
		args = append(args, "-p", codec.LiteralFormat(o.Label))
	}
	if o.Initial != "" {
		args = append(args, "-I", codec.LiteralFormat(o.Initial))
	}
	if o.SingleCharacter {
		args = append(args, "-1")
	}
	if o.Numeric {
		args = append(args, "-N")
	}
	args = append(args, "--", string(template))
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
func (p Pane) ChooseBuffer(ctx context.Context) error {
	if p.h.server != nil && p.h.server.conn != nil {
		return opError("ChooseBuffer", ErrTransportUnsupported)
	}
	return p.h.act(ctx, "choose-buffer", "-t", p.h.id)
}

func (c Client) DisplayPanes(ctx context.Context) error {
	if c.h.server != nil && c.h.server.conn != nil {
		return opError("DisplayPanes", ErrTransportUnsupported)
	}
	return c.h.act(ctx, "display-panes", "-t", c.h.id)
}

// WatchFormat uses tmux's subscription cadence, not a lossless change log.
// UnwatchFormat is explicit; the connection owns all remaining watches.
func (c *Connection) WatchFormat(ctx context.Context, name string, pane Pane, expr Format) error {
	if c == nil || !validFormatName(name) || !codec.ValidString(string(expr)) {
		return opError("WatchFormat", invalid("subscription"))
	}
	if e := pane.h.check(); e != nil {
		return opError("WatchFormat", e)
	}
	if !pane.h.origin.Equal(c.identity) {
		return opError("WatchFormat", ErrInvalidHandle)
	}
	op, e := c.server.begin(ctx)
	if e != nil {
		return opError("WatchFormat", e)
	}
	defer op.close()
	_, e = c.server.execute(op, emptyPlan(command("refresh-client", "-B", name+":"+pane.h.id+":"+string(expr))), pane.h.guard(), nil)
	return opError("WatchFormat", e)
}
func (c *Connection) UnwatchFormat(ctx context.Context, name string) error {
	if c == nil || !validFormatName(name) {
		return opError("UnwatchFormat", invalid("subscription"))
	}
	return c.server.endpointAction(ctx, "refresh-client", "-B", name)
}

// SetPaneOutput pauses/resumes output for one pane on this control client.
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
