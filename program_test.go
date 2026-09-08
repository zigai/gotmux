package tmux

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func executableFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\nprintf '%s\\000' \"$#\" \"$@\"\n"
	if e := os.WriteFile(path, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestExecArgvExact(t *testing.T) {
	for _, name := range []string{"program", "has space;$dollar", "-leading"} {
		path := executableFixture(t, name)
		for _, args := range [][]string{nil, {""}, {"", "a b", "';$()", "#{pane_id}", "\xff"}} {
			argv, e := Exec(path, args...).argv()
			if e != nil {
				t.Fatal(e)
			}
			if len(argv) < 2 {
				t.Fatal("single operand would invoke tmux shell interpretation")
			}
			out, e := exec.Command(argv[0], argv[1:]...).Output()
			if e != nil {
				t.Fatal(e)
			}
			parts := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
			if string(parts[0]) != string(rune('0'+len(args))) {
				t.Fatalf("argc: %q", out)
			}
			if len(args) > 0 {
				got := make([]string, len(parts)-1)
				for i := range got {
					got[i] = string(parts[i+1])
				}
				if !reflect.DeepEqual(got, args) {
					t.Fatalf("argv got %#v want %#v", got, args)
				}
			}
		}
	}
}
func TestProgramCopiesArgs(t *testing.T) {
	args := []string{"before"}
	p := Exec("/bin/echo", args...)
	args[0] = "after"
	a, e := p.argv()
	if e != nil || a[1] != "before" {
		t.Fatal(a, e)
	}
}
func TestEnvironmentLauncher(t *testing.T) {
	value := "\"'$() ; #{pane_id}\nline"
	flags, argv, e := programArgs("", map[string]string{"DATA": value, "PATH": "/different", "EMPTY": ""}, Exec("/bin/sh", "-c", `printf '%s\000%s\000%s' "$DATA" "$PATH" "$EMPTY"`))
	if e != nil {
		t.Fatal(e)
	}
	if len(flags) != 0 {
		t.Fatal("persistent -e flags", flags)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SHELL=/bin/sh"}
	out, e := cmd.Output()
	if e != nil || !bytes.Equal(out, []byte(value+"\x00/different\x00")) {
		t.Fatalf("%q %v", out, e)
	}
}
func TestShellEnvironmentLauncher(t *testing.T) {
	_, argv, e := programArgs("", map[string]string{"SHELL": "/not-the-executable", "DATA": "literal"}, Shell(`printf '%s %s' "$SHELL" "$DATA"`))
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"SHELL=/bin/sh"}
	out, e := cmd.Output()
	if e != nil || string(out) != "/not-the-executable literal" {
		t.Fatalf("%q %v", out, e)
	}
}
func TestUnsupportedDefaultProgramEnvironment(t *testing.T) {
	_, _, e := programArgs("", map[string]string{"A": "b"}, Program{})
	if !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
}
func TestProgramValidation(t *testing.T) {
	for _, p := range []Program{Exec(""), Exec("/bin/echo", "x\x00"), Shell("\x00")} {
		if _, e := p.argv(); e == nil {
			t.Fatal("invalid program")
		}
	}
	if _, _, e := programArgs("", map[string]string{"BAD=KEY": "x"}, Exec("/bin/true")); e == nil {
		t.Fatal("env name")
	}
}
