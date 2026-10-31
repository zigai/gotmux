package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("TMUX_GO_PROCESS_FIXTURE") == "1" {
		mode := os.Args[1]
		switch mode {
		case "streams":
			fmt.Print("stdout\x00\xff\n")
			fmt.Fprint(os.Stderr, "stderr\n")
			os.Exit(7)
		case "flood":
			for range 10000 {
				fmt.Print("01234567890123456789")
				fmt.Fprint(os.Stderr, "abcdefghijklmnopqrstuvwxyz")
			}
		case "wait":
			fmt.Print("started\n")
			time.Sleep(30 * time.Second)
		case "echo":
			echoInput()
		case "env":
			fmt.Printf("%s\n", os.Getenv("ONLY_THIS"))

			d, _ := os.Getwd()
			fmt.Print(d)
		case "success":
			fmt.Print("ok")
		default:
			runHandoffFixture(mode)
		}

		os.Exit(0)
	}

	os.Exit(m.Run())
}

func echoInput() {
	data := make([]byte, 1024)
	for {
		n, e := os.Stdin.Read(data)
		if n > 0 {
			_, _ = os.Stdout.Write(data[:n])
		}

		if e != nil {
			return
		}
	}
}

func runHandoffFixture(mode string) {
	switch mode {
	case "handoff":
		startHolder()
		fmt.Print("ok")
	case "handoff-wait":
		startHolder()
		time.Sleep(30 * time.Second)
	case "hold":
		holdOutput()
	}
}

func startHolder() {
	exe, e := os.Executable()
	if e != nil {
		os.Exit(3)
	}

	holder := exec.CommandContext(context.Background(), exe, "hold")
	holder.Env = os.Environ()
	holder.Stdout = os.Stdout

	if e := holder.Start(); e != nil {
		os.Exit(3)
	}
}

func holdOutput() {
	for range 1500 {
		if _, e := os.Stdout.Write([]byte{0}); e != nil {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func testRunner(t *testing.T, concurrent int) *runner {
	t.Helper()

	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return newRunner(
		exe,
		[]string{"TMUX_GO_PROCESS_FIXTURE=1", "ONLY_THIS=hello", "GOCOVERDIR=" + t.TempDir()},
		dir,
		concurrent,
	)
}

func TestRunnerStreamsAndStatus(t *testing.T) {
	r, started, err := testRunner(
		t,
		2,
	).run(context.Background(), []string{"streams"}, nil, 100, 100)
	if !started || r.ExitCode != 7 || err == nil ||
		!bytes.Equal(r.Stdout, []byte("stdout\x00\xff\n")) ||
		string(r.Stderr) != "stderr\n" {
		t.Fatalf("%#v started=%v err=%v", r, started, err)
	}
}

func TestRunnerBinaryInput(t *testing.T) {
	data := []byte("\x00\xff\r\n;$#{pane_id}")

	r, _, err := testRunner(t, 1).run(context.Background(), []string{"echo"}, data, 100, 100)
	if err != nil || !bytes.Equal(r.Stdout, data) {
		t.Fatalf("%#v err=%v", r, err)
	}
}

func TestRunnerBoundedStreams(t *testing.T) {
	r, _, err := testRunner(t, 1).run(context.Background(), []string{"flood"}, nil, 63, 71)
	if !errors.Is(err, ErrOutputLimit) || len(r.Stdout) > 63 || len(r.Stderr) > 71 {
		t.Fatalf("%#v err=%v", r, err)
	}
}

func TestRunnerTimeoutAndReadmission(t *testing.T) {
	r := testRunner(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	out, started, err := r.run(ctx, []string{"wait"}, nil, 100, 100)
	if !errors.Is(err, context.DeadlineExceeded) || !started {
		t.Fatalf("%#v started=%v err=%v", out, started, err)
	}

	out, _, err = r.run(context.Background(), []string{"success"}, nil, 100, 100)
	if err != nil || string(out.Stdout) != "ok" {
		t.Fatalf("next result %#v err=%v", out, err)
	}
}

func TestRunnerReturnsWhenCanceledWhileAnotherProcessHoldsItsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	begin := time.Now()

	_, started, err := testRunner(t, 1).run(ctx, []string{"handoff-wait"}, nil, 1<<20, 100)
	if !started || !errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("started=%v err=%v", started, err)
	}

	if elapsed := time.Since(begin); elapsed >= shutdownBudget/2 {
		t.Fatalf("canceled command took %v; unwanted output must not be waited for", elapsed)
	}
}

func TestRunnerBoundsTheWaitForOutputHeldAfterExit(t *testing.T) {
	begin := time.Now()

	out, _, err := testRunner(t, 1).run(context.Background(), []string{"handoff"}, nil, 1<<20, 100)
	if !errors.Is(err, ErrShutdownIncomplete) || !bytes.HasPrefix(out.Stdout, []byte("ok")) ||
		out.ExitCode != 0 {
		t.Fatalf("%#v err=%v", out, err)
	}

	if elapsed := time.Since(begin); elapsed < shutdownBudget || elapsed > 2*shutdownBudget {
		t.Fatalf("held output was waited for %v, want about %v", elapsed, shutdownBudget)
	}
}

func TestRunnerCanceledAdmission(t *testing.T) {
	r := testRunner(t, 1)
	if !r.slots.TryAcquire(1) {
		t.Fatal("admission")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, started, err := r.run(ctx, []string{"success"}, nil, 100, 100)
	r.slots.Release(1)

	if started || !errors.Is(err, context.Canceled) || out.ExitCode != -1 {
		t.Fatalf("%#v started=%v err=%v", out, started, err)
	}
}

func TestRunnerEnvironmentAndDirectory(t *testing.T) {
	r := testRunner(t, 1)

	out, _, err := r.run(context.Background(), []string{"env"}, nil, 4096, 4096)
	if err != nil {
		t.Fatalf("%#v err=%v", out, err)
	}

	lines := strings.SplitN(string(out.Stdout), "\n", 2)
	if len(lines) != 2 || lines[0] != "hello" {
		t.Fatalf("unexpected stdout: %q", string(out.Stdout))
	}

	actualDir, err := filepath.EvalSymlinks(lines[1])
	if err != nil {
		t.Fatalf("EvalSymlinks(actualDir): %v", err)
	}

	expectedDir, err := filepath.EvalSymlinks(r.dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(expectedDir): %v", err)
	}

	if actualDir != expectedDir {
		t.Fatalf("dir = %q, want %q", actualDir, expectedDir)
	}
}

func TestBufferConcurrentAccounting(t *testing.T) {
	var wg sync.WaitGroup

	b := newBuffer(1024, nil)

	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			for range 100 {
				_, _ = b.Write([]byte(strings.Repeat(strconv.Itoa(i), 10)))
			}
		}(i)
	}

	wg.Wait()

	out := b.Bytes()
	if len(out) != 1024 || !b.Overflowed() {
		t.Fatal("buffer accounting")
	}

	out[0] ^= 1
	if bytes.Equal(out, b.Bytes()) {
		t.Fatal("aliased bytes")
	}
}
