package tmux

import (
	"bytes"
	"context"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	// KeyEnter represents the Return/Enter key.
	KeyEnter Key = "Enter"

	// KeyCtrlC represents the Control-C interrupt signal key.
	KeyCtrlC Key = "C-c"

	// KeyEscape represents the Escape key.
	KeyEscape Key = "Escape"

	// KeyTab represents the Tab key.
	KeyTab Key = "Tab"

	// KeyBackspace represents the Backspace key.
	KeyBackspace Key = "BSpace"

	// KeyUp, KeyDown, KeyLeft, KeyRight represent directional arrow keys.
	KeyUp    Key = "Up"
	KeyDown  Key = "Down"
	KeyLeft  Key = "Left"
	KeyRight Key = "Right"

	// KeySpace represents the spacebar key.
	KeySpace Key = "Space"
)

const (
	// CurrentScreen captures from whichever screen buffer is currently active (normal or alternate).
	CurrentScreen CaptureScreen = iota

	// AlternateScreen captures specifically from the alternate screen buffer (-a flag).
	AlternateScreen

	// ModeScreen captures from the pane's active copy-mode buffer.
	ModeScreen
)

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

type (
	// Key represents a keyboard key or key combination in tmux syntax.
	// Supports named keys ("Enter", "Tab"), single characters ("a", "?"), function keys ("F1"-"F12"),
	// and modifier prefixes ("C-" for Control, "M-" for Alt/Meta, "S-" for Shift).
	Key string

	// CaptureScreen selects which terminal screen buffer to read during pane capture.
	CaptureScreen uint8

	// CopyAction identifies a copy-mode sub-command executed via send-keys -X.
	CopyAction string

	// CaptureOptions configures capturing terminal output and scrollback history from a pane.
	CaptureOptions struct {
		// Start specifies the starting line offset (negative values index into history).
		Start *int

		// End specifies the ending line offset.
		End *int

		// EntireHistory captures the pane's entire scrollback history (-S -).
		EntireHistory bool

		// JoinWrapped joins lines that were soft-wrapped by terminal dimensions (-J flag).
		JoinWrapped bool

		// IncludeEscapes retains ANSI formatting and color escape sequences (-e flag).
		IncludeEscapes bool

		// PreserveSpaces preserves trailing spaces on each line (-N flag).
		PreserveSpaces bool

		// Screen selects which terminal screen buffer to capture.
		Screen CaptureScreen

		// MaxBytes optionally tightens the maximum bytes captured below [Limits.OutputBytes].
		MaxBytes int64
	}

	// CopyModeOptions configures entering or exiting copy mode on a pane.
	CopyModeOptions struct {
		// PageUp immediately scrolls up one page upon entering copy mode (-u flag).
		PageUp bool

		// Mouse enters copy mode and handles mouse dragging (-M flag).
		Mouse bool

		// Exit exits/cancels copy mode if currently active (-q flag).
		Exit bool
	}

	// CaptureResult holds the captured terminal screen content and pane title.
	CaptureResult struct {
		// Output is the raw captured terminal bytes.
		Output []byte

		// Title is the pane title string at capture time (#{pane_title}).
		Title string
	}
)

// Valid reports whether this key string represents a syntactically valid tmux key name.
func (k Key) Valid() bool {
	s := string(k)
	if s == "" || !utf8.ValidString(s) {
		return false
	}

	s = trimModifiers(s)

	if utf8.RuneCountInString(s) == 1 {
		r, _ := utf8.DecodeRuneInString(s)
		return r >= 32 && r != 127
	}

	return isNamedKey(s) || isFunctionKey(s)
}

// SendText transmits literal text into the target pane without appending a newline.
// Text is sent with -l to bypass key name translation. Cannot contain embedded NUL bytes.
func (p Pane) SendText(ctx context.Context, text string) error {
	if err := p.h.check(); err != nil {
		return opError("SendText", err)
	}

	if !wire.ValidString(text) {
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

// SendKeys transmits one or more key symbols (e.g. [KeyCtrlC], [KeyEnter]) into the target pane.
func (p Pane) SendKeys(ctx context.Context, keys ...Key) error {
	if err := p.h.check(); err != nil {
		return opError("SendKeys", err)
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
	if err := p.h.check(); err != nil {
		return opError("Submit", err)
	}

	if !wire.ValidString(text) {
		return opError("Submit", invalid("text contains NUL"))
	}

	opCtx, op, err := p.h.server.begin(ctx)
	if err != nil {
		return opError("Submit", err)
	}
	defer op.close()

	nodes := []wireNode{}
	if text != "" {
		nodes = append(nodes, leaf(command("send-keys", "-t", p.h.id, "-l", "--", text)))
	}

	nodes = append(nodes, leaf(command("send-keys", "-t", p.h.id, "--", string(KeyEnter))))
	_, err = p.h.server.execute(opCtx, op, plan{nodes: nodes, mode: replyEmpty, allowStart: false}, p.h.guard(), nil)

	return opError("Submit", err)
}

// Capture returns owned bytes without trimming or decoding. Control mode cannot
// unambiguously frame arbitrary captured terminal output; explicitly select
// p.UsingSubprocess() for that operation on a control-bound pane.
func (p Pane) Capture(ctx context.Context, o CaptureOptions) ([]byte, error) {
	if err := p.h.check(); err != nil {
		return nil, opError("Capture", err)
	}

	args, err := captureArgs(p.h.id, o)
	if err != nil {
		return nil, opError("Capture", err)
	}

	if o.MaxBytes > p.h.server.config.Limits.OutputBytes {
		return nil, opError("Capture", invalid("MaxBytes may only tighten the configured limit"))
	}

	opCtx, op, err := p.h.server.begin(ctx)
	if err != nil {
		return nil, opError("Capture", err)
	}
	defer op.close()

	if o.MaxBytes > 0 {
		op.output = min(op.output, o.MaxBytes+int64(len(guardOK)))
	}

	r, err := p.h.server.execute(opCtx, op, plainPlan(command("capture-pane", args...)), p.h.guard(), nil)
	if o.MaxBytes > 0 && int64(len(r.Stdout)) > o.MaxBytes {
		r.Stdout = r.Stdout[:o.MaxBytes]

		if err == nil {
			err = afterError("Capture", ErrOutputLimit)
		}
	}

	return r.Stdout, opError("Capture", err)
}

// CaptureWithTitle captures the pane's visible text or scrollback and returns its title in a single round-trip.
func (p Pane) CaptureWithTitle(ctx context.Context, o CaptureOptions) (CaptureResult, error) {
	if err := p.h.check(); err != nil {
		return CaptureResult{}, opError("CaptureWithTitle", err)
	}

	capArgs, err := captureArgs(p.h.id, o)
	if err != nil {
		return CaptureResult{}, opError("CaptureWithTitle", err)
	}

	if o.MaxBytes > p.h.server.config.Limits.OutputBytes {
		return CaptureResult{}, opError("CaptureWithTitle", invalid("MaxBytes may only tighten the configured limit"))
	}

	const marker = "___GOTMUX_CAPTURE_TITLE___"

	titleCmd := command("display-message", "-p", "-t", p.h.id, marker+"\n#{pane_title}\n"+marker)
	capCmd := command("capture-pane", capArgs...)

	opCtx, op, err := p.h.server.begin(ctx)
	if err != nil {
		return CaptureResult{}, opError("CaptureWithTitle", err)
	}
	defer op.close()

	pl := plan{
		nodes:      []wireNode{leaf(titleCmd), leaf(capCmd)},
		mode:       replyRaw,
		allowStart: false,
	}

	r, err := p.h.server.execute(opCtx, op, pl, p.h.guard(), nil)
	if err != nil {
		return CaptureResult{}, opError("CaptureWithTitle", err)
	}

	raw := r.Stdout
	startMarker := []byte(marker + "\n")
	endMarker := []byte("\n" + marker + "\n")

	_, after, ok := bytes.Cut(raw, startMarker)
	if !ok {
		return CaptureResult{Output: raw, Title: ""}, nil
	}

	afterStart := after

	before0, after0, ok0 := bytes.Cut(afterStart, endMarker)
	if !ok0 {
		return CaptureResult{Output: raw, Title: ""}, nil
	}

	title := string(before0)
	output := after0

	if o.MaxBytes > 0 && int64(len(output)) > o.MaxBytes {
		output = output[:o.MaxBytes]
		err = afterError("CaptureWithTitle", ErrOutputLimit)
	}

	return CaptureResult{
		Output: output,
		Title:  title,
	}, opError("CaptureWithTitle", err)
}

// CopyMode enters or exits copy mode on this pane according to opts.
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

// CopyAction executes a copy-mode sub-command (via send-keys -X) on this pane.
// The pane should already be in copy mode.
func (p Pane) CopyAction(ctx context.Context, action CopyAction, args ...string) error {
	if !wire.ValidCommand(string(action)) {
		return opError("CopyAction", invalid("copy-mode action"))
	}

	for _, arg := range args {
		if !wire.ValidString(arg) {
			return opError("CopyAction", invalid("copy-mode argument"))
		}
	}

	argv := append([]string{"-X", "-t", p.h.id, "--", string(action)}, args...)

	return p.h.act(ctx, "send-keys", argv...)
}

func trimModifiers(s string) string {
	for {
		if len(s) >= 2 && s[1] == '-' {
			switch s[0] {
			case 'C', 'c', 'M', 'm', 'S', 's':
				s = s[2:]
				continue
			}
		}

		return s
	}
}

func isNamedKey(s string) bool {
	switch strings.ToLower(s) {
	case "enter", "escape", "tab", "btab", "bspace", "space", "up", "down", "left", "right",
		"home", "end", "pageup", "pagedown", "ppage", "npage", "pgup", "pgdn",
		"insert", "ic", "delete", "dc",
		"kpenter", "kpmul", "kpplus", "kpminus", "kpdiv", "kpdel",
		"kp0", "kp1", "kp2", "kp3", "kp4", "kp5", "kp6", "kp7", "kp8", "kp9":
		return true
	default:
		return false
	}
}

func isFunctionKey(s string) bool {
	if len(s) < 2 || (s[0] != 'F' && s[0] != 'f') {
		return false
	}

	n, e := strconv.Atoi(s[1:])

	return e == nil && n >= 1 && n <= 63
}

func captureArgs(id string, o CaptureOptions) ([]string, error) {
	if o.Screen > ModeScreen || o.MaxBytes < 0 || (o.EntireHistory && o.Start != nil) {
		return nil, invalid("capture options")
	}

	rangeArgs, err := captureRangeArgs(o)
	if err != nil {
		return nil, err
	}

	args := append([]string{"-p", "-t", id}, rangeArgs...)
	args = append(args, captureFlagArgs(o)...)

	return args, nil
}

func captureRangeArgs(o CaptureOptions) ([]string, error) {
	var args []string

	if o.EntireHistory {
		args = append(args, "-S", "-")
	} else if o.Start != nil {
		if *o.Start < math.MinInt32 || *o.Start > math.MaxInt32 {
			return nil, invalid("capture start")
		}

		args = append(args, "-S", strconv.Itoa(*o.Start))
	}

	if o.End != nil {
		if *o.End < math.MinInt32 || *o.End > math.MaxInt32 {
			return nil, invalid("capture end")
		}

		args = append(args, "-E", strconv.Itoa(*o.End))
	}

	return args, nil
}

func captureFlagArgs(o CaptureOptions) []string {
	var args []string
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

	return args
}
