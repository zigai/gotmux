package tmux

import (
	"context"
	"strconv"
)

const (
	// ResizeUp increases or decreases pane height upwards (-U flag).
	ResizeUp ResizeDirection = iota

	// ResizeDown increases or decreases pane height downwards (-D flag).
	ResizeDown

	// ResizeLeft increases or decreases pane width to the left (-L flag).
	ResizeLeft

	// ResizeRight increases or decreases pane width to the right (-R flag).
	ResizeRight
)

const (
	// Vertical splits the pane vertically (-v flag), placing the new pane below the current one.
	Vertical Direction = iota

	// Horizontal splits the pane horizontally (-h flag), placing the new pane beside the current one.
	Horizontal
)

const (
	// EvenHorizontal arranges panes in equal-width vertical columns side by side.
	EvenHorizontal Layout = "even-horizontal"

	// EvenVertical arranges panes in equal-height horizontal rows stacked on top of each other.
	EvenVertical Layout = "even-vertical"

	// MainHorizontal arranges a large primary pane on top with remaining panes tiled below.
	MainHorizontal Layout = "main-horizontal"

	// MainVertical arranges a large primary pane on the left with remaining panes tiled on the right.
	MainVertical Layout = "main-vertical"

	// Tiled arranges panes evenly in a 2D rectangular grid.
	Tiled Layout = "tiled"
)

type (
	// Direction indicates whether a split occurs vertically (top/bottom) or horizontally (side-by-side).
	Direction uint8

	// ResizeDirection specifies the direction for relative pane resizing.
	ResizeDirection uint8

	// Layout identifies a named tmux window pane layout geometry.
	Layout string

	// Size specifies terminal dimensions in character cells. Zero lets tmux choose.
	Size struct {
		Width  int
		Height int
	}

	// SplitSize specifies an exact cell count or percentage (1-100) for a pane split.
	SplitSize struct {
		Cells   int
		Percent int
	}

	// SwapPaneOptions configures pane swapping behavior.
	SwapPaneOptions struct {
		// Up swaps with the previous pane (-U flag).
		Up bool

		// Down swaps with the next pane (-D flag).
		Down bool
	}
)

func (s Size) valid() bool {
	return s.Width >= 0 && s.Height >= 0 && s.Width <= 1<<20 && s.Height <= 1<<20
}

func (s SplitSize) args() ([]string, error) {
	if s.Cells < 0 || s.Cells > 1<<20 || s.Percent < 0 || s.Percent > 100 || s.Cells != 0 && s.Percent != 0 {
		return nil, invalid("split size")
	}

	if s.Cells != 0 {
		return []string{"-l", strconv.Itoa(s.Cells)}, nil
	}

	if s.Percent != 0 {
		return []string{"-l", strconv.Itoa(s.Percent) + "%"}, nil
	}

	return nil, nil
}

// SwapWith exchanges this pane with another pane according to opts.
func (p Pane) SwapWith(ctx context.Context, other Pane, o SwapPaneOptions) error {
	if err := p.h.check(); err != nil {
		return opError("SwapWith", err)
	}

	if err := other.h.check(); err != nil {
		return opError("SwapWith", err)
	}

	args := []string{"-s", p.h.id, "-t", other.h.id}
	if o.Up {
		args = append(args, "-U")
	}

	if o.Down {
		args = append(args, "-D")
	}

	return p.h.act(ctx, "swap-pane", args...)
}

// SwapUp swaps this pane with the previous pane (-U flag).
func (p Pane) SwapUp(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("SwapUp", err)
	}

	return p.h.act(ctx, "swap-pane", "-U", "-t", p.h.id)
}

// SwapDown swaps this pane with the next pane (-D flag).
func (p Pane) SwapDown(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("SwapDown", err)
	}

	return p.h.act(ctx, "swap-pane", "-D", "-t", p.h.id)
}

// Mark sets the marked pane flag on this pane (-m flag).
func (p Pane) Mark(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("Mark", err)
	}

	return p.h.act(ctx, "select-pane", "-m", "-t", p.h.id)
}

// Unmark clears the marked pane flag on this pane (-M flag).
func (p Pane) Unmark(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("Unmark", err)
	}

	return p.h.act(ctx, "select-pane", "-M", "-t", p.h.id)
}

// ResizeRelative adjusts the pane dimensions relative to its current size by adj cells in direction dir.
func (p Pane) ResizeRelative(ctx context.Context, adj int, dir ResizeDirection) error {
	if err := p.h.check(); err != nil {
		return opError("ResizeRelative", err)
	}

	if adj <= 0 {
		return opError("ResizeRelative", invalid("adjustment must be positive"))
	}

	var flag string

	switch dir {
	case ResizeUp:
		flag = "-U"
	case ResizeDown:
		flag = "-D"
	case ResizeLeft:
		flag = "-L"
	case ResizeRight:
		flag = "-R"
	default:
		return opError("ResizeRelative", invalid("resize direction"))
	}

	return p.h.act(ctx, "resize-pane", "-t", p.h.id, flag, strconv.Itoa(adj))
}
