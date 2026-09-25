package tmux

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseShellEnvironmentDecodesEscapedMultilineValues(t *testing.T) {
	data := []byte("A=\"one\ntwo=2\n-B\"; export A;\nunset C;\nD=\"q\\\" d\\$ t\\` s\\\\\"; export D;\nE=\"\"; export E;\n")

	got, err := parseShellEnvironment(data, true)
	if err != nil {
		t.Fatal(err)
	}

	want := []EnvironmentEntry{
		{Name: "A", Value: EnvironmentValue{Value: PresentValue("one\ntwo=2\n-B"), Unset: false, Hidden: true}},
		{Name: "C", Value: EnvironmentValue{Value: UnavailableValue[string](), Unset: true, Hidden: true}},
		{Name: "D", Value: EnvironmentValue{Value: PresentValue("q\" d$ t` s\\"), Unset: false, Hidden: true}},
		{Name: "E", Value: EnvironmentValue{Value: PresentValue(""), Unset: false, Hidden: true}},
	}

	if diff := cmp.Diff(want, got, cmp.AllowUnexported(PresentValue(""))); diff != "" {
		t.Fatalf("entries mismatch (-want +got):\n%s", diff)
	}
}

func TestParseShellEnvironmentRejectsMalformedRecords(t *testing.T) {
	for name, data := range map[string]string{
		"plain record":        "A=value\n",
		"unterminated value":  "A=\"value\n",
		"dangling escape":     "A=\"value\\",
		"mismatched export":   "A=\"value\"; export B;\n",
		"missing export":      "A=\"value\"\n",
		"invalid name":        "1A=\"value\"; export 1A;\n",
		"unterminated unset":  "unset A",
		"invalid unset name":  "unset A-B;\n",
		"trailing garbage":    "A=\"v\"; export A;\njunk",
		"continuation header": "    --color=\"x\"; export     --color;\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseShellEnvironment([]byte(data), false); !errors.Is(err, ErrProtocol) {
				t.Fatalf("parseShellEnvironment(%q) error = %v, want ErrProtocol", data, err)
			}
		})
	}
}
