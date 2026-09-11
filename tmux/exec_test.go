package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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
			data := make([]byte, 1024)
			for {
				n, e := os.Stdin.Read(data)
				if n > 0 {
					_, _ = os.Stdout.Write(data[:n])
				}

				if e != nil {
					break
				}
			}
		case "env":
			fmt.Printf("%s\n", os.Getenv("ONLY_THIS"))

			d, _ := os.Getwd()
			fmt.Print(d)
		case "success":
			fmt.Print("ok")
		}

		os.Exit(0)
	}

	os.Exit(m.Run())
}

func testRunner(t *testing.T, concurrent int) *runner {
	t.Helper()

	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}

	return newRunner(exe, []string{"TMUX_GO_PROCESS_FIXTURE=1", "ONLY_THIS=hello", "GOCOVERDIR=" + t.TempDir()}, t.TempDir(), concurrent)
}

func TestRunnerStreamsAndStatus(t *testing.T) {
	r, started, err := testRunner(t, 2).run(context.Background(), []string{"streams"}, nil, 100, 100)
	if !started || r.ExitCode != 7 || err == nil || !bytes.Equal(r.Stdout, []byte("stdout\x00\xff\n")) || string(r.Stderr) != "stderr\n" {
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
	if err != nil || string(out.Stdout) != "hello\n"+r.dir {
		t.Fatalf("%#v err=%v", out, err)
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
