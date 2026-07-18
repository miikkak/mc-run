package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRun_ExitCodePassthrough(t *testing.T) {
	sup := New(Options{})
	code, err := sup.Run(context.Background(), []string{"sh", "-c", "exit 7"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestRun_SIGTERM_StopViaStdinFallback(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "out")

	// Reads lines from stdin and appends them to outFile; exits cleanly once
	// it sees the stop command, simulating a server responding to "stop".
	script := `while IFS= read -r line; do
  printf '%s\n' "$line" >> "$1"
  if [ "$line" = "stop" ]; then
    exit 0
  fi
done`

	sup := New(Options{StopCommand: "stop", StopDuration: 5 * time.Second})

	runDone := make(chan struct {
		code int
		err  error
	}, 1)
	go func() {
		code, err := sup.Run(context.Background(), []string{"sh", "-c", script, "sh", outFile})
		runDone <- struct {
			code int
			err  error
		}{code, err}
	}()

	// Give the child a moment to start and the signal handler to register
	// before we deliver SIGTERM to our own test process.
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}

	select {
	case res := <-runDone:
		if res.err != nil {
			t.Fatalf("Run: %v", res.err)
		}
		if res.code != 0 {
			t.Fatalf("exit code = %d, want 0", res.code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after SIGTERM")
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), "stop\n") {
		t.Fatalf("child did not receive stop command, got %q", got)
	}
}

func TestRun_StopDurationExceeded_SIGKILLsChild(t *testing.T) {
	sup := New(Options{
		StopCommand:  "stop",
		StopDuration: 200 * time.Millisecond,
	})

	var mu sync.Mutex
	var result struct {
		code int
		err  error
	}
	done := make(chan struct{})
	go func() {
		code, err := sup.Run(context.Background(), []string{"sh", "-c", "trap '' TERM; while true; do sleep 1; done"})
		mu.Lock()
		result.code, result.err = code, err
		mu.Unlock()
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after stop duration should have elapsed")
	}

	mu.Lock()
	defer mu.Unlock()
	if result.err != nil {
		t.Fatalf("Run: %v", result.err)
	}
	// 128 + SIGKILL(9) = 137, matching shell/OOM-kill exit code conventions.
	if result.code != 137 {
		t.Fatalf("exit code = %d, want 137 (SIGKILL)", result.code)
	}
}

func TestRun_NoCommand(t *testing.T) {
	sup := New(Options{})
	if _, err := sup.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty argv, got nil")
	}
}
