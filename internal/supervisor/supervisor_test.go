package supervisor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestRun_SIGINT_StopViaStdinFallback(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "out")

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

	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
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
		t.Fatal("Run did not return after SIGINT")
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(got), "stop\n") {
		t.Fatalf("child did not receive stop command, got %q", got)
	}
}

// TestRun_StopDurationExceeded_KillsWholeProcessGroup guards against only
// the direct child being killed: the child here forks a grandchild (via a
// backgrounded shell loop) that ignores TERM and would survive as an orphan
// if only the direct child's PID were SIGKILLed instead of its whole
// process group.
func TestRun_StopDurationExceeded_KillsWholeProcessGroup(t *testing.T) {
	grandchildPIDFile := filepath.Join(t.TempDir(), "grandchild-pid")

	script := `trap '' TERM
(trap '' TERM; echo $$ > "$1"; while true; do sleep 1; done) &
wait`

	sup := New(Options{
		StopCommand:  "stop",
		StopDuration: 200 * time.Millisecond,
	})

	done := make(chan struct{})
	go func() {
		_, _ = sup.Run(context.Background(), []string{"sh", "-c", script, "sh", grandchildPIDFile})
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

	// Give the grandchild PID file time to appear and the kill to land.
	deadline := time.Now().Add(2 * time.Second)
	var pidBytes []byte
	for time.Now().Before(deadline) {
		var err error
		pidBytes, err = os.ReadFile(grandchildPIDFile)
		if err == nil && len(pidBytes) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pidBytes) == 0 {
		t.Fatal("grandchild never wrote its PID file")
	}

	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		t.Fatalf("parse grandchild pid: %v", err)
	}

	// Poll briefly: the kill is delivered as soon as the stop duration
	// elapses, but the grandchild's actual termination is asynchronous.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // grandchild is gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild pid %d still alive after process-group kill", pid)
}

// TestRun_StopDurationNotBlockedByStop guards against gracefulStop's kill
// timer being starved by a slow-to-return stop() call (e.g. a long
// StopServerAnnounceDelay, or in production an unresponsive RCON connection
// or a child that isn't reading its stdin): stop() runs concurrently with
// the StopDuration timer, so the timer must still fire close to
// StopDuration even while stop() is still in the middle of its own,
// considerably longer, wait.
func TestRun_StopDurationNotBlockedByStop(t *testing.T) {
	sup := New(Options{
		StopCommand:             "stop",
		StopDuration:            200 * time.Millisecond,
		StopServerAnnounceDelay: 5 * time.Second,
	})

	start := time.Now()
	done := make(chan struct{})
	go func() {
		_, _ = sup.Run(context.Background(), []string{"sh", "-c", "trap '' TERM; while true; do sleep 1; done"})
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly: StopDuration's kill timer appears blocked by the announce delay in stop()")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run took %v, want well under the 5s announce delay (StopDuration should have killed the child first)", elapsed)
	}
}

// TestGracefulStop_StopGoroutineCancelledOnTimeout guards against a
// goroutine leak: stop() runs in its own goroutine (see
// TestRun_StopDurationNotBlockedByStop) so a slow stop() can't block the
// kill timer, but that goroutine must not be left running after
// gracefulStop returns. Here StopDuration (200ms) is far shorter than
// StopServerAnnounceDelay (5s), so the kill timer fires and gracefulStop
// returns while stop() is still in the middle of its announce-delay wait;
// gracefulStop must cancel stop()'s context so that wait unblocks promptly
// instead of the goroutine lingering for the rest of the 5s delay.
func TestGracefulStop_StopGoroutineCancelledOnTimeout(t *testing.T) {
	sup := New(Options{
		StopCommand:             "stop",
		StopDuration:            200 * time.Millisecond,
		StopServerAnnounceDelay: 5 * time.Second,
	})

	baseline := runtime.NumGoroutine()

	done := make(chan struct{})
	go func() {
		_, _ = sup.Run(context.Background(), []string{"sh", "-c", "trap '' TERM; while true; do sleep 1; done"})
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly")
	}

	// The stop() goroutine's announce-delay wait (5s) must be cancelled
	// promptly when gracefulStop returns, not left running — poll for the
	// goroutine count to settle back down well before that 5s would
	// otherwise elapse.
	deadline := time.Now().Add(1 * time.Second)
	for {
		if n := runtime.NumGoroutine(); n <= baseline {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("goroutine count still %d (baseline %d) 1s after Run returned — stop()'s goroutine appears to have leaked", n, baseline)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRun_NoCommand(t *testing.T) {
	sup := New(Options{})
	if _, err := sup.Run(context.Background(), nil); err == nil {
		t.Fatal("expected error for empty argv, got nil")
	}
}
