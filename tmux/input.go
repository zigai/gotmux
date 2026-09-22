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

		// ScrollbackEnd captures to the end of the scrollback buffer (-E -).
		ScrollbackEnd bool

		// JoinWrapped joins lines that were soft-wrapped by terminal dimensions (-J flag).
		JoinWrapped bool

		// IncludeEscapes retains escape sequences for text attributes, colors, and OSC 8 hyperlinks (-e flag).
		IncludeEscapes bool

		// PreserveSpaces preserves trailing spaces on each line (-N flag).
		PreserveSpaces bool

		// PaneState captures the pane state as well as the content (-P flag).
		PaneState bool

		// Quiet suppresses errors if the target pane cannot be captured (-q flag).
		Quiet bool

		// TrimEmptyCells omits trailing cells without characters (-T flag).
		TrimEmptyCells bool

		// Screen selects which terminal screen buffer to capture.
		Screen CaptureScreen

		// Buffer specifies the target buffer name to store captured text into (-b flag).
		Buffer string

		// MaxBytes optionally tightens the maximum bytes captured below [Limits.OutputBytes].
		MaxBytes int64

		// EscapeNonPrintable escapes non-printable characters as octal \xxx (-C flag).
		EscapeNonPrintable bool

		// AlternateScreenOnly captures from the alternate screen buffer only if active (-F flag).
		AlternateScreenOnly bool
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

	// SendKeysOptions configures sending key presses or text input to a pane.
	SendKeysOptions struct {
		// ExpandFormat expands format specifiers in key or text arguments (-F flag).
		ExpandFormat bool

		// Hex treats key arguments or text as hexadecimal byte values (-H flag).
		Hex bool

		// KeyName looks up keys in the client's key table rather than target pane (-K flag).
		KeyName bool

		// Reset resets the terminal state before sending keys (-R flag).
		Reset bool

		// RepeatCount specifies how many times to repeat each key press (-N flag).
		// Must be non-negative.
		RepeatCount int

		// MouseForward forwards mouse events to the pane (-M flag).
		MouseForward bool

		// Client specifies the target client terminal to send keys to (-c flag).
		Client string
	}
)

// Valid reports whether this key string represents a syntactically valid tmux key name.
func (k Key) Valid() bool {
	s := string(k)
	if s == "" || !utf8.ValidString(s) || strings.ContainsRune(s, '\x00') {
		return false
	}

	s = trimModifiers(s)
	if s == "" {
		return false
	}

	if utf8.RuneCountInString(s) == 1 {
		r, _ := utf8.DecodeRuneInString(s)
		return r >= 32 && r != 127
	}

	return isNamedKey(s) || isFunctionKey(s) || isUserKey(s) || isMouseKey(s)
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

// SendKeysWith transmits key symbols into the target pane using the provided options.
func (p Pane) SendKeysWith(ctx context.Context, opts SendKeysOptions, keys ...Key) error {
	if err := p.h.check(); err != nil {
		return opError("SendKeysWith", err)
	}

	flagArgs, err := sendKeysFlagArgs(opts)
	if err != nil {
		return opError("SendKeysWith", err)
	}

	if len(keys) == 0 && !opts.Reset && !opts.MouseForward {
		if ctx == nil {
			return opError("SendKeysWith", invalid("nil context"))
		}

		return opError("SendKeysWith", ctx.Err())
	}

	args := append([]string{"-t", p.h.id}, flagArgs...)

	if len(keys) > 0 {
		if err := validateKeyList(keys, opts.Hex); err != nil {
			return opError("SendKeysWith", err)
		}

		args = append(args, "--")
		for _, k := range keys {
			args = append(args, string(k))
		}
	}

	return p.h.act(ctx, "send-keys", args...)
}

// SendTextWith transmits text into the target pane using the provided options.
// Text is sent with -l to bypass key name translation unless Hex or ExpandFormat is set.
// Cannot contain embedded NUL bytes.
func (p Pane) SendTextWith(ctx context.Context, opts SendKeysOptions, text string) error {
	if err := p.h.check(); err != nil {
		return opError("SendTextWith", err)
	}

	if !wire.ValidString(text) {
		return opError("SendTextWith", invalid("text contains NUL; use a binary buffer"))
	}

	flagArgs, err := sendKeysFlagArgs(opts)
	if err != nil {
		return opError("SendTextWith", err)
	}

	if text == "" && !opts.Reset && !opts.MouseForward {
		if ctx == nil {
			return opError("SendTextWith", invalid("nil context"))
		}

		return opError("SendTextWith", ctx.Err())
	}

	textArgs, err := sendTextArgs(text, opts.Hex)
	if err != nil {
		return opError("SendTextWith", err)
	}

	args := append([]string{"-t", p.h.id}, flagArgs...)
	if !opts.Hex && !opts.ExpandFormat {
		args = append(args, "-l")
	}

	args = append(args, textArgs...)

	return p.h.act(ctx, "send-keys", args...)
}

// SendPrefix sends the tmux prefix key (or secondary prefix if secondary is true) to this pane (send-prefix).
func (p Pane) SendPrefix(ctx context.Context, secondary bool) error {
	if err := p.h.check(); err != nil {
		return opError("SendPrefix", err)
	}

	args := []string{"-t", p.h.id}
	if secondary {
		args = append(args, "-2")
	}

	return p.h.act(ctx, "send-prefix", args...)
}

// ClockMode displays the digital clock in this pane (clock-mode).
func (p Pane) ClockMode(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("ClockMode", err)
	}

	return p.h.act(ctx, "clock-mode", "-t", p.h.id)
}

func validateKeyList(keys []Key, hex bool) error {
	for _, k := range keys {
		if hex {
			if !wire.ValidString(string(k)) || string(k) == "" {
				return invalid("hex key")
			}

			continue
		}

		if !k.Valid() {
			return invalid("key name")
		}
	}

	return nil
}

func sendTextArgs(text string, hex bool) ([]string, error) {
	if text == "" {
		return nil, nil
	}

	if hex {
		parts := strings.Fields(text)
		if len(parts) == 0 {
			return nil, invalid("hex text")
		}

		return append([]string{"--"}, parts...), nil
	}

	return []string{"--", text}, nil
}

// Capture returns owned bytes without trimming or decoding. Control mode cannot
// unambiguously frame arbitrary captured terminal output; explicitly select
// p.UsingSubprocess() for that operation on a control-bound pane.
func (p Pane) Capture(ctx context.Context, opts CaptureOptions) ([]byte, error) {
	if err := p.h.check(); err != nil {
		return nil, opError("Capture", err)
	}

	args, err := captureArgs(p.h.id, opts)
	if err != nil {
		return nil, opError("Capture", err)
	}

	if opts.MaxBytes > p.h.server.config.Limits.OutputBytes {
		return nil, opError("Capture", invalid("MaxBytes may only tighten the configured limit"))
	}

	opCtx, op, err := p.h.server.begin(ctx)
	if err != nil {
		return nil, opError("Capture", err)
	}
	defer op.close()

	if opts.MaxBytes > 0 {
		op.output = min(op.output, opts.MaxBytes+int64(len(guardOK)))
	}

	r, err := p.h.server.execute(opCtx, op, plainPlan(command("capture-pane", args...)), p.h.guard(), nil)
	if opts.MaxBytes > 0 && int64(len(r.Stdout)) > opts.MaxBytes {
		r.Stdout = r.Stdout[:opts.MaxBytes]

		if err == nil {
			err = afterError("Capture", ErrOutputLimit)
		}
	}

	return r.Stdout, opError("Capture", err)
}

// CaptureWithTitle captures the pane's visible text or scrollback and returns its title in a single round-trip.
func (p Pane) CaptureWithTitle(ctx context.Context, opts CaptureOptions) (CaptureResult, error) {
	if err := p.h.check(); err != nil {
		return CaptureResult{}, opError("CaptureWithTitle", err)
	}

	capArgs, err := captureArgs(p.h.id, opts)
	if err != nil {
		return CaptureResult{}, opError("CaptureWithTitle", err)
	}

	if opts.MaxBytes > p.h.server.config.Limits.OutputBytes {
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
		return CaptureResult{}, afterError("CaptureWithTitle", ErrProtocol)
	}

	lastIdx := bytes.LastIndex(after, endMarker)
	if lastIdx < 0 {
		return CaptureResult{}, afterError("CaptureWithTitle", ErrProtocol)
	}

	title := string(after[:lastIdx])
	output := after[lastIdx+len(endMarker):]

	if opts.MaxBytes > 0 && int64(len(output)) > opts.MaxBytes {
		output = output[:opts.MaxBytes]
		err = afterError("CaptureWithTitle", ErrOutputLimit)
	}

	return CaptureResult{
		Output: output,
		Title:  title,
	}, opError("CaptureWithTitle", err)
}

// CaptureToBuffer captures the pane's visible text or scrollback directly into a tmux paste buffer.
// The target buffer name must be specified via [CaptureOptions.Buffer].
func (p Pane) CaptureToBuffer(ctx context.Context, opts CaptureOptions) error {
	if err := p.h.check(); err != nil {
		return opError("CaptureToBuffer", err)
	}

	if opts.Buffer == "" {
		return opError("CaptureToBuffer", invalid("buffer name"))
	}

	args, err := captureArgs(p.h.id, opts)
	if err != nil {
		return opError("CaptureToBuffer", err)
	}

	return p.h.act(ctx, "capture-pane", args...)
}

// CopyMode enters or exits copy mode on this pane according to opts.
func (p Pane) CopyMode(ctx context.Context, opts CopyModeOptions) error {
	args := []string{"-t", p.h.id}
	if opts.Exit {
		args = append(args, "-q")
	}

	if opts.PageUp {
		args = append(args, "-u")
	}

	if opts.Mouse {
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

		if len(s) >= 2 && s[0] == '^' {
			s = s[1:]
			continue
		}

		return s
	}
}

func isNamedKey(s string) bool {
	switch strings.ToLower(s) {
	case "enter", "escape", "tab", "btab", "bspace", "space", "up", "down", "left", "right",
		"home", "end", "pageup", "pagedown", "ppage", "npage", "pgup", "pgdn",
		"insert", "ic", "delete", "dc",
		"any", "none",
		"kpenter", "kpmul", "kpplus", "kpminus", "kpdiv", "kpdel",
		"kp0", "kp1", "kp2", "kp3", "kp4", "kp5", "kp6", "kp7", "kp8", "kp9":
		return true
	default:
		return false
	}
}

func isUserKey(s string) bool {
	if len(s) < 5 || (!strings.HasPrefix(s, "User") && !strings.HasPrefix(s, "user")) {
		return false
	}

	n, err := strconv.Atoi(s[4:])

	return err == nil && n >= 0 && n <= 1024
}

func isMouseKey(s string) bool {
	var (
		rest string
		ok   bool
	)

	for _, event := range []string{
		"MouseDragEnd1", "MouseDragEnd2", "MouseDragEnd3",
		"MouseDrag1", "MouseDrag2", "MouseDrag3",
		"MouseDown1", "MouseDown2", "MouseDown3",
		"MouseUp1", "MouseUp2", "MouseUp3",
		"SecondClick1", "SecondClick2", "SecondClick3",
		"DoubleClick1", "DoubleClick2", "DoubleClick3",
		"TripleClick1", "TripleClick2", "TripleClick3",
		"WheelDown", "WheelUp",
	} {
		if len(s) > len(event) && strings.EqualFold(s[:len(event)], event) {
			rest = s[len(event):]
			ok = true

			break
		}
	}

	if !ok || rest == "" {
		return false
	}

	switch strings.ToLower(rest) {
	case "pane", "border", "status", "statusleft", "statusright", "statusdefault",
		"scrollbarslider", "scrollbarup", "scrollbardown", "empty":
		return true
	}

	if strings.HasPrefix(strings.ToLower(rest), "control") {
		n, err := strconv.Atoi(rest[7:])

		return err == nil && n >= 0 && n <= 1024
	}

	return false
}

func isFunctionKey(s string) bool {
	if len(s) < 2 || (s[0] != 'F' && s[0] != 'f') {
		return false
	}

	n, e := strconv.Atoi(s[1:])

	return e == nil && n >= 1 && n <= 63
}

func captureArgs(id string, opts CaptureOptions) ([]string, error) {
	if opts.Screen > ModeScreen || opts.MaxBytes < 0 || (opts.EntireHistory && opts.Start != nil) || (opts.ScrollbackEnd && opts.End != nil) {
		return nil, invalid("capture options")
	}

	if opts.Buffer != "" && !wire.ValidString(opts.Buffer) {
		return nil, invalid("buffer name")
	}

	rangeArgs, err := captureRangeArgs(opts)
	if err != nil {
		return nil, err
	}

	var args []string
	if opts.Buffer == "" {
		args = append(args, "-p")
	}

	args = append(args, "-t", id)
	args = append(args, rangeArgs...)
	args = append(args, captureFlagArgs(opts)...)

	return args, nil
}

func captureRangeArgs(opts CaptureOptions) ([]string, error) {
	var args []string

	if opts.EntireHistory {
		args = append(args, "-S", "-")
	} else if opts.Start != nil {
		if *opts.Start < math.MinInt32 || *opts.Start > math.MaxInt32 {
			return nil, invalid("capture start")
		}

		args = append(args, "-S", strconv.Itoa(*opts.Start))
	}

	if opts.ScrollbackEnd && opts.End != nil {
		return nil, invalid("capture options")
	}

	if opts.ScrollbackEnd {
		args = append(args, "-E", "-")
	} else if opts.End != nil {
		if *opts.End < math.MinInt32 || *opts.End > math.MaxInt32 {
			return nil, invalid("capture end")
		}

		args = append(args, "-E", strconv.Itoa(*opts.End))
	}

	return args, nil
}

func captureFlagArgs(opts CaptureOptions) []string {
	var args []string
	if opts.JoinWrapped {
		args = append(args, "-J")
	}

	if opts.IncludeEscapes {
		args = append(args, "-e")
	}

	if opts.PreserveSpaces {
		args = append(args, "-N")
	}

	if opts.PaneState {
		args = append(args, "-P")
	}

	if opts.Quiet {
		args = append(args, "-q")
	}

	if opts.TrimEmptyCells {
		args = append(args, "-T")
	}

	if opts.Screen == AlternateScreen {
		args = append(args, "-a")
	}

	if opts.Screen == ModeScreen {
		args = append(args, "-M")
	}

	if opts.Buffer != "" {
		args = append(args, "-b", opts.Buffer)
	}

	if opts.EscapeNonPrintable {
		args = append(args, "-C")
	}

	if opts.AlternateScreenOnly {
		args = append(args, "-F")
	}

	return args
}

func sendKeysFlagArgs(opts SendKeysOptions) ([]string, error) {
	if opts.RepeatCount < 0 {
		return nil, invalid("repeat count")
	}

	if opts.Client != "" && !wire.ValidString(opts.Client) {
		return nil, invalid("client")
	}

	var args []string
	if opts.ExpandFormat {
		args = append(args, "-F")
	}

	if opts.Hex {
		args = append(args, "-H")
	}

	if opts.KeyName {
		args = append(args, "-K")
	}

	if opts.Reset {
		args = append(args, "-R")
	}

	if opts.MouseForward {
		args = append(args, "-M")
	}

	if opts.RepeatCount > 0 {
		args = append(args, "-N", strconv.Itoa(opts.RepeatCount))
	}

	if opts.Client != "" {
		args = append(args, "-c", opts.Client)
	}

	return args, nil
}
