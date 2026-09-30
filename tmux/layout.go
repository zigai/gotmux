package tmux

import (
	"context"
	"strconv"
)

const (
	// ResizeDirectionUp increases or decreases pane height upwards (-U flag).
	ResizeDirectionUp ResizeDirection = iota

	// ResizeDirectionDown increases or decreases pane height downwards (-D flag).
	ResizeDirectionDown

	// ResizeDirectionLeft increases or decreases pane width to the left (-L flag).
	ResizeDirectionLeft

	// ResizeDirectionRight increases or decreases pane width to the right (-R flag).
	ResizeDirectionRight
)

const (
	// DirectionVertical splits the pane vertically (-v flag), placing the new pane below the current one.
	DirectionVertical Direction = iota

	// DirectionHorizontal splits the pane horizontally (-h flag), placing the new pane beside the current one.
	DirectionHorizontal
)

const (
	// Vertical and Horizontal are retained as shorthand aliases for DirectionVertical and DirectionHorizontal.
	Vertical   = DirectionVertical
	Horizontal = DirectionHorizontal
)

const (
	// LayoutEvenHorizontal arranges panes in equal-width vertical columns side by side.
	LayoutEvenHorizontal Layout = "even-horizontal"

	// LayoutEvenVertical arranges panes in equal-height horizontal rows stacked on top of each other.
	LayoutEvenVertical Layout = "even-vertical"

	// LayoutMainHorizontal arranges a large primary pane on top with remaining panes tiled below.
	LayoutMainHorizontal Layout = "main-horizontal"

	// LayoutMainVertical arranges a large primary pane on the left with remaining panes tiled on the right.
	LayoutMainVertical Layout = "main-vertical"

	// LayoutTiled arranges panes evenly in a 2D rectangular grid.
	LayoutTiled Layout = "tiled"
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

	// SwapPaneOptions configures [Pane.Swap].
	SwapPaneOptions struct {
		// Select makes the swapped pane active; otherwise focus stays put (-d flag when false).
		Select bool
	}

	// SwapWindowOptions configures [WindowLink.Swap].
	SwapWindowOptions struct {
		// Select makes the swapped window current; otherwise focus stays put (-d flag when false).
		Select bool
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

// SwapUp swaps this pane with the previous pane (-U flag).
func (p Pane) SwapUp(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("Pane.SwapUp", err)
	}

	return p.h.act(ctx, "Pane.SwapUp", "swap-pane", "-U", "-t", p.h.id)
}

// SwapDown swaps this pane with the next pane (-D flag).
func (p Pane) SwapDown(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("Pane.SwapDown", err)
	}

	return p.h.act(ctx, "Pane.SwapDown", "swap-pane", "-D", "-t", p.h.id)
}

// Mark sets the marked pane flag on this pane (-m flag).
func (p Pane) Mark(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("Pane.Mark", err)
	}

	return p.h.act(ctx, "Pane.Mark", "select-pane", "-m", "-t", p.h.id)
}

// Unmark clears the marked pane flag on this pane (-M flag).
func (p Pane) Unmark(ctx context.Context) error {
	if err := p.h.check(); err != nil {
		return opError("Pane.Unmark", err)
	}

	return p.h.act(ctx, "Pane.Unmark", "select-pane", "-M", "-t", p.h.id)
}

// ResizeRelative adjusts the pane dimensions relative to its current size by adj cells in direction dir.
func (p Pane) ResizeRelative(ctx context.Context, adj int, dir ResizeDirection) error {
	if err := p.h.check(); err != nil {
		return opError("Pane.ResizeRelative", err)
	}

	if adj <= 0 {
		return opError("Pane.ResizeRelative", invalid("adjustment must be positive"))
	}

	var flag string

	switch dir {
	case ResizeDirectionUp:
		flag = "-U"
	case ResizeDirectionDown:
		flag = "-D"
	case ResizeDirectionLeft:
		flag = "-L"
	case ResizeDirectionRight:
		flag = "-R"
	default:
		return opError("Pane.ResizeRelative", invalid("resize direction"))
	}

	return p.h.act(ctx, "Pane.ResizeRelative", "resize-pane", "-t", p.h.id, flag, strconv.Itoa(adj))
}
