package tmux

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"example.com/tmux/internal/codec"
)

type Key string

const (
	KeyEnter     Key = "Enter"
	KeyCtrlC     Key = "C-c"
	KeyEscape    Key = "Escape"
	KeyTab       Key = "Tab"
	KeyBackspace Key = "BSpace"
	KeyUp        Key = "Up"
	KeyDown      Key = "Down"
	KeyLeft      Key = "Left"
	KeyRight     Key = "Right"
	KeySpace     Key = "Space"
)

func (k Key) Valid() bool {
	s := string(k)
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for {
		if strings.HasPrefix(s, "C-") || strings.HasPrefix(s, "M-") || strings.HasPrefix(s, "S-") {
			s = s[2:]
		} else {
			break
		}
	}
	if utf8.RuneCountInString(s) == 1 {
		r, _ := utf8.DecodeRuneInString(s)
		return r >= 32 && r != 127
	}
	switch s {
	case "Enter", "Escape", "Tab", "BTab", "BSpace", "Space", "Up", "Down", "Left", "Right", "Home", "End", "PageUp", "PageDown", "PPage", "NPage", "Insert", "IC", "Delete", "DC", "KPEnter", "KPMul", "KPPlus", "KPMinus", "KPDiv", "KPDel":
		return true
	}
	if strings.HasPrefix(s, "F") {
		n, e := strconv.Atoi(s[1:])
		return e == nil && n >= 1 && n <= 63 && s == "F"+strconv.Itoa(n)
	}
	return false
}
func (p Pane) SendText(ctx context.Context, text string) error {
	if e := p.h.check(); e != nil {
		return opError("SendText", e)
	}
	if !codec.ValidString(text) {
		return opError("SendText", invalid("text contains NUL; use a binary buffer"))
	}
	if text == "" {
		if ctx == nil {
			return opError("SendText", invalid("nil context"))
		}
		return opError("SendText", ctx.Err())
	}
	return p.h.act(ctx, "send-keys", "-t", p.h.id, "-l", "--", text)
}
func (p Pane) SendKeys(ctx context.Context, keys ...Key) error {
	if e := p.h.check(); e != nil {
		return opError("SendKeys", e)
	}
	args := []string{"-t", p.h.id, "--"}
	for _, k := range keys {
		if !k.Valid() {
			return opError("SendKeys", invalid("key name"))
		}
		args = append(args, string(k))
	}
	if len(keys) == 0 {
		if ctx == nil {
			return opError("SendKeys", invalid("nil context"))
		}
		return opError("SendKeys", ctx.Err())
	}
	return p.h.act(ctx, "send-keys", args...)
}

// Submit sends literal text and a final Enter in one ordered tmux command queue.
// It does not wait for shell completion; cancellation may leave a partial input.
func (p Pane) Submit(ctx context.Context, text string) error {
	if e := p.h.check(); e != nil {
		return opError("Submit", e)
	}
	if !codec.ValidString(text) {
		return opError("Submit", invalid("text contains NUL"))
	}
	op, e := p.h.server.begin(ctx)
	if e != nil {
		return opError("Submit", e)
	}
	defer op.close()
	nodes := []wireNode{}
	if text != "" {
		nodes = append(nodes, leaf(command("send-keys", "-t", p.h.id, "-l", "--", text)))
	}
	nodes = append(nodes, leaf(command("send-keys", "-t", p.h.id, "--", string(KeyEnter))))
	_, e = p.h.server.execute(op, plan{nodes: nodes, mode: replyEmpty}, p.h.guard(), nil)
	return opError("Submit", e)
}

type CaptureScreen uint8

const (
	CurrentScreen CaptureScreen = iota
	AlternateScreen
	ModeScreen
)

type CaptureOptions struct {
	Start          *int
	End            *int
	EntireHistory  bool
	JoinWrapped    bool
	IncludeEscapes bool
	PreserveSpaces bool
	Screen         CaptureScreen
	MaxBytes       int64
}

func captureArgs(id string, o CaptureOptions) ([]string, error) {
	if o.Screen > ModeScreen || o.MaxBytes < 0 || o.EntireHistory && o.Start != nil {
		return nil, invalid("capture options")
	}
	args := []string{"-p", "-t", id}
	if o.EntireHistory {
		args = append(args, "-S", "-")
	} else if o.Start != nil {
		if *o.Start < -(1<<31) || *o.Start > 1<<31-1 {
			return nil, invalid("capture start")
		}
		args = append(args, "-S", strconv.Itoa(*o.Start))
	}
	if o.End != nil {
		if *o.End < -(1<<31) || *o.End > 1<<31-1 {
			return nil, invalid("capture end")
		}
		args = append(args, "-E", strconv.Itoa(*o.End))
	}
	if o.JoinWrapped {
		args = append(args, "-J")
	}
	if o.IncludeEscapes {
		args = append(args, "-e")
	}
	if o.PreserveSpaces {
		args = append(args, "-N")
	}
	if o.Screen == AlternateScreen {
		args = append(args, "-a")
	}
	if o.Screen == ModeScreen {
		args = append(args, "-M")
	}
	return args, nil
}

// Capture returns owned bytes without trimming or decoding. Control mode cannot
// unambiguously frame arbitrary captured terminal output; explicitly select
// p.UsingSubprocess() for that operation on a control-bound pane.
func (p Pane) Capture(ctx context.Context, o CaptureOptions) ([]byte, error) {
	if e := p.h.check(); e != nil {
		return nil, opError("Capture", e)
	}
	args, e := captureArgs(p.h.id, o)
	if e != nil {
		return nil, opError("Capture", e)
	}
	if o.MaxBytes > p.h.server.config.Limits.OutputBytes {
		return nil, opError("Capture", invalid("MaxBytes may only tighten the configured limit"))
	}
	op, e := p.h.server.begin(ctx)
	if e != nil {
		return nil, opError("Capture", e)
	}
	defer op.close()
	if o.MaxBytes > 0 {
		op.output = min(op.output, o.MaxBytes+int64(len(guardOK)))
	}
	r, e := p.h.server.execute(op, plainPlan(command("capture-pane", args...)), p.h.guard(), nil)
	if o.MaxBytes > 0 && int64(len(r.Stdout)) > o.MaxBytes {
		r.Stdout = r.Stdout[:o.MaxBytes]
		if e == nil {
			e = afterError("Capture", ErrOutputLimit)
		}
	}
	return r.Stdout, opError("Capture", e)
}

type CopyModeOptions struct {
	PageUp bool
	Mouse  bool
	Exit   bool
}

func (p Pane) CopyMode(ctx context.Context, o CopyModeOptions) error {
	args := []string{"-t", p.h.id}
	if o.Exit {
		args = append(args, "-q")
	}
	if o.PageUp {
		args = append(args, "-u")
	}
	if o.Mouse {
		args = append(args, "-M")
	}
	return p.h.act(ctx, "copy-mode", args...)
}

type CopyAction string

const (
	CopyBeginSelection     CopyAction = "begin-selection"
	CopyClearSelection     CopyAction = "clear-selection"
	CopyCancel             CopyAction = "cancel"
	CopyCursorUp           CopyAction = "cursor-up"
	CopyCursorDown         CopyAction = "cursor-down"
	CopyCursorLeft         CopyAction = "cursor-left"
	CopyCursorRight        CopyAction = "cursor-right"
	CopyPageUp             CopyAction = "page-up"
	CopyPageDown           CopyAction = "page-down"
	CopyHistoryTop         CopyAction = "history-top"
	CopyHistoryBottom      CopyAction = "history-bottom"
	CopySelectLine         CopyAction = "select-line"
	CopyToggleRectangle    CopyAction = "rectangle-toggle"
	CopySelection          CopyAction = "copy-selection"
	CopySelectionAndCancel CopyAction = "copy-selection-and-cancel"
)

func (p Pane) CopyAction(ctx context.Context, action CopyAction) error {
	switch action {
	case CopyBeginSelection, CopyClearSelection, CopyCancel, CopyCursorUp, CopyCursorDown, CopyCursorLeft, CopyCursorRight, CopyPageUp, CopyPageDown, CopyHistoryTop, CopyHistoryBottom, CopySelectLine, CopyToggleRectangle, CopySelection, CopySelectionAndCancel:
	default:
		return opError("CopyAction", invalid("copy-mode action"))
	}
	return p.h.act(ctx, "send-keys", "-X", "-t", p.h.id, string(action))
}
