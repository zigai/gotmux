package tmux

import "strconv"

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
