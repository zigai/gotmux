package tmux

import (
	"reflect"
	"testing"
)

func TestSizeValidation(t *testing.T) {
	tests := []struct {
		name  string
		size  Size
		valid bool
	}{
		{"zero", Size{0, 0}, true},
		{"positive", Size{80, 24}, true},
		{"negative width", Size{-1, 24}, false},
		{"negative height", Size{80, -1}, false},
		{"too large width", Size{1<<20 + 1, 24}, false},
		{"too large height", Size{80, 1<<20 + 1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.size.valid(); got != tt.valid {
				t.Errorf("Size%+v.valid() = %v, want %v", tt.size, got, tt.valid)
			}
		})
	}
}

func TestSplitSizeArgs(t *testing.T) {
	tests := []struct {
		name    string
		split   SplitSize
		want    []string
		wantErr bool
	}{
		{"zero", SplitSize{0, 0}, nil, false},
		{"cells", SplitSize{Cells: 15, Percent: 0}, []string{"-l", "15"}, false},
		{"percent", SplitSize{Cells: 0, Percent: 30}, []string{"-l", "30%"}, false},
		{"both set", SplitSize{Cells: 10, Percent: 20}, nil, true},
		{"negative cells", SplitSize{Cells: -1, Percent: 0}, nil, true},
		{"negative percent", SplitSize{Cells: 0, Percent: -5}, nil, true},
		{"percent over 100", SplitSize{Cells: 0, Percent: 101}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := tt.split.args()
			if (err != nil) != tt.wantErr {
				t.Fatalf("SplitSize%+v.args() error = %v, wantErr %v", tt.split, err, tt.wantErr)
			}

			if !reflect.DeepEqual(args, tt.want) {
				t.Errorf("SplitSize%+v.args() = %v, want %v", tt.split, args, tt.want)
			}
		})
	}
}

func TestLayoutConstants(t *testing.T) {
	layouts := []Layout{
		EvenHorizontal,
		EvenVertical,
		MainHorizontal,
		MainVertical,
		Tiled,
	}

	for _, l := range layouts {
		if l == "" {
			t.Error("expected non-empty layout constant")
		}
	}
}
